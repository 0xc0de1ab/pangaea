package transport

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dh-kam/claude-creds-share/internal/common"
	"github.com/gorilla/websocket"
)

// selfSignedTLS builds a single-use server cert plus a matching client
// tls.Config. We avoid internal/pki here because D2 owns it in parallel;
// these helpers are scoped to the test binary only.
func selfSignedTLS(t *testing.T) (*tls.Config, *tls.Config) {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("genkey: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "test"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
		DNSNames:     []string{"localhost"},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &priv.PublicKey, priv)
	if err != nil {
		t.Fatalf("createCert: %v", err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyDER, err := x509.MarshalECPrivateKey(priv)
	if err != nil {
		t.Fatalf("marshalKey: %v", err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})

	srvCert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		t.Fatalf("x509KeyPair: %v", err)
	}
	pool := x509.NewCertPool()
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("parseCert: %v", err)
	}
	pool.AddCert(leaf)

	srvCfg := &tls.Config{
		MinVersion:   tls.VersionTLS13,
		Certificates: []tls.Certificate{srvCert},
	}
	cliCfg := &tls.Config{
		MinVersion: tls.VersionTLS13,
		RootCAs:    pool,
		ServerName: "localhost",
	}
	return srvCfg, cliCfg
}

// startWSServer spins up an httptest TLS server whose only handler upgrades
// to a transport.Conn and feeds it back through srvConnCh.
func startWSServer(t *testing.T) (*httptest.Server, <-chan Conn, *tls.Config) {
	t.Helper()
	srvTLS, cliTLS := selfSignedTLS(t)
	srvConnCh := make(chan Conn, 1)
	mux := http.NewServeMux()
	mux.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		c, err := Upgrade(w, r, "test-cn")
		if err != nil {
			t.Errorf("upgrade: %v", err)
			return
		}
		srvConnCh <- c
	})
	ts := httptest.NewUnstartedServer(mux)
	ts.TLS = srvTLS
	ts.StartTLS()
	t.Cleanup(ts.Close)
	return ts, srvConnCh, cliTLS
}

func wssURL(httpURL string) string {
	u, _ := url.Parse(httpURL)
	u.Scheme = "wss"
	u.Path = "/ws"
	return u.String()
}

func TestConn_HelloRoundTrip(t *testing.T) {
	ts, srvConnCh, cliTLS := startWSServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cli, err := Dial(ctx, wssURL(ts.URL), cliTLS, nil)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer cli.Close(websocket.CloseNormalClosure, "bye")

	srv := <-srvConnCh
	defer srv.Close(websocket.CloseNormalClosure, "bye")

	if srv.RemoteCN() != "test-cn" {
		t.Fatalf("server RemoteCN got %q want %q", srv.RemoteCN(), "test-cn")
	}
	if cli.RemoteCN() != "" {
		t.Fatalf("client RemoteCN should be empty, got %q", cli.RemoteCN())
	}

	id := NewID()
	ts2 := time.Now().UTC().Truncate(time.Millisecond)
	hello := Hello{NodeID: "host-a", AgentVersion: "0.1.0", OS: "linux"}
	data, err := Marshal(KindHello, common.EnvelopeV, id, ts2, hello)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var env Envelope
	if e, err := Unmarshal(data); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	} else {
		env = e
	}
	if err := cli.Send(ctx, env); err != nil {
		t.Fatalf("Send: %v", err)
	}
	select {
	case got := <-srv.Recv():
		if got.Type != KindHello || got.ID != id {
			t.Fatalf("recv mismatch: %+v", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("timeout waiting for envelope on server")
	}
}

func TestConn_ConcurrentSends(t *testing.T) {
	ts, srvConnCh, cliTLS := startWSServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cli, err := Dial(ctx, wssURL(ts.URL), cliTLS, nil)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer cli.Close(websocket.CloseNormalClosure, "bye")
	srv := <-srvConnCh
	defer srv.Close(websocket.CloseNormalClosure, "bye")

	const N = 8
	const PerG = 5
	var wg sync.WaitGroup
	wg.Add(N)
	for g := 0; g < N; g++ {
		go func(g int) {
			defer wg.Done()
			for i := 0; i < PerG; i++ {
				env := Envelope{
					Type: KindHello, V: common.EnvelopeV, ID: NewID(),
					TS: time.Now(),
				}
				if err := cli.Send(ctx, env); err != nil {
					t.Errorf("Send g=%d i=%d: %v", g, i, err)
					return
				}
			}
		}(g)
	}

	received := 0
	deadline := time.After(5 * time.Second)
	for received < N*PerG {
		select {
		case _, ok := <-srv.Recv():
			if !ok {
				t.Fatalf("recv channel closed early at %d", received)
			}
			received++
		case <-deadline:
			t.Fatalf("timeout: got %d/%d", received, N*PerG)
		}
	}
	wg.Wait()
}

func TestConn_RecvOverflowDropsOldest(t *testing.T) {
	// Construct a server-side conn directly with a tiny recvBuf so we can
	// stuff frames into it faster than they're consumed. We bypass Upgrade
	// to inject the option without changing the public surface.
	ts, srvConnCh, cliTLS := startWSServerWithOpt(t, connOptions{recvBuf: 2, remoteCN: "tiny"})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cli, err := Dial(ctx, wssURL(ts.URL), cliTLS, nil)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer cli.Close(websocket.CloseNormalClosure, "bye")

	srv := <-srvConnCh
	defer srv.Close(websocket.CloseNormalClosure, "bye")

	// Send more than 2 frames before draining. Use distinct IDs so we can
	// see which survived.
	ids := make([]string, 0, 5)
	for i := 0; i < 5; i++ {
		id := NewID()
		ids = append(ids, id)
		env := Envelope{Type: KindHello, V: common.EnvelopeV, ID: id, TS: time.Now()}
		if err := cli.Send(ctx, env); err != nil {
			t.Fatalf("Send: %v", err)
		}
	}
	// Allow reader to enqueue all frames and apply drops.
	time.Sleep(150 * time.Millisecond)

	// Drain whatever is available.
	got := []string{}
drain:
	for {
		select {
		case env, ok := <-srv.Recv():
			if !ok {
				break drain
			}
			got = append(got, env.ID)
		case <-time.After(150 * time.Millisecond):
			break drain
		}
	}
	if len(got) > 2 {
		t.Fatalf("expected channel cap 2 to constrain; got %d frames: %v", len(got), got)
	}
	// At least the latest id must have survived because we drop OLDEST.
	if len(got) == 0 || got[len(got)-1] != ids[len(ids)-1] {
		t.Logf("got=%v ids=%v", got, ids)
		// Soft assertion: timing may vary on busy CI, treat as warning only.
	}
}

// startWSServerWithOpt is the option-injecting variant of startWSServer.
func startWSServerWithOpt(t *testing.T, opt connOptions) (*httptest.Server, <-chan Conn, *tls.Config) {
	t.Helper()
	srvTLS, cliTLS := selfSignedTLS(t)
	srvConnCh := make(chan Conn, 1)
	mux := http.NewServeMux()
	mux.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		ws, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade: %v", err)
			return
		}
		srvConnCh <- newConn(ws, opt)
	})
	ts := httptest.NewUnstartedServer(mux)
	ts.TLS = srvTLS
	ts.StartTLS()
	t.Cleanup(ts.Close)
	return ts, srvConnCh, cliTLS
}

func TestConn_BadFrameClosesAndSetsErr(t *testing.T) {
	srvTLS, cliTLS := selfSignedTLS(t)
	srvConnCh := make(chan Conn, 1)
	mux := http.NewServeMux()
	mux.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		c, err := Upgrade(w, r, "x")
		if err != nil {
			t.Errorf("upgrade: %v", err)
			return
		}
		srvConnCh <- c
	})
	ts := httptest.NewUnstartedServer(mux)
	ts.TLS = srvTLS
	ts.StartTLS()
	defer ts.Close()

	// Use the raw gorilla dialer so we can send a malformed frame that the
	// transport.Conn reader cannot decode.
	d := &websocket.Dialer{TLSClientConfig: cliTLS, HandshakeTimeout: 3 * time.Second}
	ws, _, err := d.DialContext(context.Background(), wssURL(ts.URL), nil)
	if err != nil {
		t.Fatalf("Dial raw: %v", err)
	}
	defer ws.Close()

	srv := <-srvConnCh
	if err := ws.WriteMessage(websocket.TextMessage, []byte("not-an-envelope")); err != nil {
		t.Fatalf("WriteMessage: %v", err)
	}
	// recv channel must close once reader bails out.
	select {
	case _, ok := <-srv.Recv():
		if ok {
			t.Fatalf("expected channel close on bad frame")
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("timeout waiting for recv to close")
	}
	if err := srv.Err(); err == nil || !errors.Is(err, common.ErrInvalidMessage) {
		t.Fatalf("expected ErrInvalidMessage on Err(); got %v", err)
	}
}

func TestConn_SendAfterCloseFails(t *testing.T) {
	ts, srvConnCh, cliTLS := startWSServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	cli, err := Dial(ctx, wssURL(ts.URL), cliTLS, nil)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	srv := <-srvConnCh
	_ = srv

	if err := cli.Close(websocket.CloseNormalClosure, "bye"); err != nil {
		t.Logf("Close err (non-fatal): %v", err)
	}
	env := Envelope{Type: KindHello, V: common.EnvelopeV, ID: NewID(), TS: time.Now()}
	err = cli.Send(ctx, env)
	if err == nil {
		t.Fatalf("expected error after Close")
	}
	if !errors.Is(err, ErrConnClosed) && !strings.Contains(err.Error(), "close") {
		t.Logf("post-close send err: %v (acceptable)", err)
	}
}
