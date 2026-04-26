package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/0xc0de1ab/pangaea/internal/common"
	"github.com/0xc0de1ab/pangaea/internal/config"
	"github.com/0xc0de1ab/pangaea/internal/jwtauth"
	"github.com/0xc0de1ab/pangaea/internal/logging"
	"github.com/0xc0de1ab/pangaea/internal/notifier"
	"github.com/0xc0de1ab/pangaea/internal/pki"
	"github.com/0xc0de1ab/pangaea/internal/transport"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"

	// Register the claude-credentials-json-format via init side-effect.
	_ "github.com/0xc0de1ab/pangaea/pkg/formats/claudecreds"
)

type testPKI struct {
	caCert, serverCert, serverKey string
	clientACert, clientAKey       string
	clientBCert, clientBKey       string
}

// mintPKI creates an ECDSA CA + one server cert (SAN: 127.0.0.1 + localhost)
// + two client certs (CNs "node-a" and "node-b") in tmp dir.
func mintPKI(t *testing.T) testPKI {
	t.Helper()
	dir := t.TempDir()
	caExpiry := time.Now().Add(2 * time.Hour)
	leafExpiry := time.Now().Add(time.Hour)
	_, err := pki.NewCA(dir, "test-ca", caExpiry)
	require.NoError(t, err)

	ca, err := pki.LoadCA(dir+"/ca.crt", dir+"/ca.key")
	require.NoError(t, err)

	sub := dir + "/server"
	san := pki.SAN{IPs: []net.IP{net.ParseIP("127.0.0.1")}, DNS: []string{"localhost"}}
	require.NoError(t, pki.IssueServer(ca, sub, "test-server", san, leafExpiry))
	subA := dir + "/client-a"
	require.NoError(t, pki.IssueClient(ca, subA, "node-a", leafExpiry))
	subB := dir + "/client-b"
	require.NoError(t, pki.IssueClient(ca, subB, "node-b", leafExpiry))

	return testPKI{
		caCert:      dir + "/ca.crt",
		serverCert:  sub + "/server.crt",
		serverKey:   sub + "/server.key",
		clientACert: subA + "/client.crt",
		clientAKey:  subA + "/client.key",
		clientBCert: subB + "/client.crt",
		clientBKey:  subB + "/client.key",
	}
}

// startTestServer stands up an httptest TLS server running our router.
func startTestServer(t *testing.T, p testPKI, ps config.ProfileStore) *httptest.Server {
	t.Helper()
	hub := NewHub(ps, logging.New(logging.Options{Level: "error"}))
	return startTestServerWithHub(t, p, ps, hub)
}

func startTestServerWithHub(t *testing.T, p testPKI, ps config.ProfileStore, hub *Hub) *httptest.Server {
	t.Helper()
	cfg := &config.ServerConfig{
		AuthMode: config.AuthModeMTLS,
		PKI: config.PKIPaths{
			CACert:     p.caCert,
			ServerCert: p.serverCert,
			ServerKey:  p.serverKey,
		},
	}
	tlsCfg, err := pki.ServerTLSConfig(p.caCert, p.serverCert, p.serverKey, true)
	require.NoError(t, err)
	auth, err := newAuthenticator(cfg)
	require.NoError(t, err)
	router := newRouter(hub, ps, "test", auth, logging.New(logging.Options{Level: "error"}))
	srv := httptest.NewUnstartedServer(router)
	srv.TLS = tlsCfg
	srv.StartTLS()
	return srv
}

func startJWTTestServer(t *testing.T, p testPKI, ps config.ProfileStore, secretPath string) *httptest.Server {
	t.Helper()
	cfg := &config.ServerConfig{
		AuthMode: config.AuthModeJWT,
		PKI: config.PKIPaths{
			CACert:     p.caCert,
			ServerCert: p.serverCert,
			ServerKey:  p.serverKey,
		},
		JWT: config.JWTServerConfig{
			SecretKeyFile: secretPath,
			Issuer:        "test-issuer",
			Audience:      "test-audience",
			AuthTimeout:   2 * time.Second,
		},
	}
	tlsCfg, err := pki.ServerTLSConfig(p.caCert, p.serverCert, p.serverKey, false)
	require.NoError(t, err)
	auth, err := newAuthenticator(cfg)
	require.NoError(t, err)
	hub := NewHub(ps, logging.New(logging.Options{Level: "error"}))
	router := newRouter(hub, ps, "test", auth, logging.New(logging.Options{Level: "error"}))
	srv := httptest.NewUnstartedServer(router)
	srv.TLS = tlsCfg
	srv.StartTLS()
	return srv
}

func dialClient(t *testing.T, srv *httptest.Server, p testPKI, cert, key, profile string) *websocket.Conn {
	t.Helper()
	tlsCfg, err := pki.ClientTLSConfig(p.caCert, cert, key, "localhost")
	require.NoError(t, err)
	u, err := url.Parse(srv.URL)
	require.NoError(t, err)
	wsURL := fmt.Sprintf("wss://localhost:%s/ws/profile/%s", u.Port(), profile)
	d := &websocket.Dialer{TLSClientConfig: tlsCfg, HandshakeTimeout: 3 * time.Second}
	conn, _, err := d.Dial(wsURL, nil)
	require.NoError(t, err)
	return conn
}

func dialJWTClient(t *testing.T, srv *httptest.Server, p testPKI, profile string, headers map[string]string) *websocket.Conn {
	t.Helper()
	tlsCfg, err := pki.ClientTLSConfig(p.caCert, "", "", "localhost")
	require.NoError(t, err)
	u, err := url.Parse(srv.URL)
	require.NoError(t, err)
	wsURL := fmt.Sprintf("wss://localhost:%s/ws/profile/%s", u.Port(), profile)
	d := &websocket.Dialer{TLSClientConfig: tlsCfg, HandshakeTimeout: 3 * time.Second}
	httpHeaders := make(http.Header)
	for k, v := range headers {
		httpHeaders.Set(k, v)
	}
	conn, _, err := d.Dial(wsURL, httpHeaders)
	require.NoError(t, err)
	return conn
}

func writeEnv(t *testing.T, c *websocket.Conn, kind transport.Kind, payload any) {
	t.Helper()
	raw, err := transport.Marshal(kind, common.EnvelopeV, transport.NewID(), time.Now(), payload)
	require.NoError(t, err)
	require.NoError(t, c.WriteMessage(websocket.TextMessage, raw))
}

func readEnv(t *testing.T, c *websocket.Conn) transport.Envelope {
	t.Helper()
	_ = c.SetReadDeadline(time.Now().Add(3 * time.Second))
	_, data, err := c.ReadMessage()
	require.NoError(t, err)
	env, err := transport.Unmarshal(data)
	require.NoError(t, err)
	return env
}

// sampleCreds returns a well-formed claude-credentials-json-format blob with
// the given expiry.
func sampleCreds(exp time.Time, accessTail string) []byte {
	body := map[string]any{
		"claudeAiOauth": map[string]any{
			"accessToken":      "sk-ant-oat01-" + strings.Repeat("X", 40) + accessTail,
			"refreshToken":     "sk-ant-ort01-" + strings.Repeat("Y", 40),
			"expiresAt":        exp.UnixMilli(),
			"scopes":           []string{"user:inference", "user:profile"},
			"subscriptionType": "max",
		},
	}
	b, _ := json.Marshal(body)
	return b
}

func testProfile(allowed []string, cooldown time.Duration) *config.ProfilesFile {
	return &config.ProfilesFile{Profiles: []config.Profile{{
		Name:           "p1",
		Format:         "claude-credentials-json-format",
		Dir:            "/tmp",
		AllowedClients: allowed,
		Validate:       config.ValidateSpec{Strategy: "expires_at_max"},
		Propagate:      config.PropagateSpec{Mode: config.PropagateModeStaleOnly, Cooldown: cooldown},
	}}}
}

// TestServer_TruthPushFromANewerToB walks the §17.2(a) scenario.
func TestServer_TruthPushFromANewerToB(t *testing.T) {
	p := mintPKI(t)
	ps := config.NewProfileStore(testProfile([]string{"node-a", "node-b"}, 0))
	srv := startTestServer(t, p, ps)
	defer srv.Close()

	a := dialClient(t, srv, p, p.clientACert, p.clientAKey, "p1")
	defer a.Close()
	b := dialClient(t, srv, p, p.clientBCert, p.clientBKey, "p1")
	defer b.Close()

	writeEnv(t, a, transport.KindHello, transport.Hello{NodeID: "node-a", AgentVersion: "0.1", OS: "linux"})
	writeEnv(t, b, transport.KindHello, transport.Hello{NodeID: "node-b", AgentVersion: "0.1", OS: "linux"})
	require.Equal(t, transport.KindWelcome, readEnv(t, a).Type)
	require.Equal(t, transport.KindWelcome, readEnv(t, b).Type)

	older := sampleCreds(time.Now().Add(10*time.Minute), "OLDB")
	writeEnv(t, b, transport.KindSnapshotReport, transport.SnapshotReport{
		Profile: "p1", Path: "/tmp/b", Format: "claude-credentials-json-format",
		RawB64: base64.StdEncoding.EncodeToString(older), RawSize: len(older),
		LiveCheck: transport.LiveCheckMeta{Performed: false},
	})
	newer := sampleCreds(time.Now().Add(2*time.Hour), "NEWA")
	writeEnv(t, a, transport.KindSnapshotReport, transport.SnapshotReport{
		Profile: "p1", Path: "/tmp/a", Format: "claude-credentials-json-format",
		RawB64: base64.StdEncoding.EncodeToString(newer), RawSize: len(newer),
		LiveCheck: transport.LiveCheckMeta{Performed: false},
	})

	env := readEnv(t, b)
	require.Equal(t, transport.KindTruthPush, env.Type)
	var push transport.TruthPush
	require.NoError(t, json.Unmarshal(env.Payload, &push))
	require.Equal(t, "p1", push.Profile)
	require.Equal(t, "/tmp/.credentials.json", push.TargetPath)
	got, err := base64.StdEncoding.DecodeString(push.RawB64)
	require.NoError(t, err)
	require.Equal(t, newer, got)
}

func TestServer_PropagationNotifierEmitsSourceAndTargets(t *testing.T) {
	p := mintPKI(t)
	ps := config.NewProfileStore(testProfile([]string{"node-a", "node-b"}, 0))
	hub := NewHub(ps, logging.New(logging.Options{Level: "error"}))
	events := make(chan notifier.TruthRecord, 8)
	hub.SetPropagationNotifier(func(_ context.Context, r notifier.TruthRecord) {
		select {
		case events <- r:
		default:
		}
	})
	srv := startTestServerWithHub(t, p, ps, hub)
	defer srv.Close()

	a := dialClient(t, srv, p, p.clientACert, p.clientAKey, "p1")
	defer a.Close()
	b := dialClient(t, srv, p, p.clientBCert, p.clientBKey, "p1")
	defer b.Close()

	writeEnv(t, a, transport.KindHello, transport.Hello{NodeID: "node-a", AgentVersion: "0.1", OS: "linux"})
	writeEnv(t, b, transport.KindHello, transport.Hello{NodeID: "node-b", AgentVersion: "0.1", OS: "linux"})
	require.Equal(t, transport.KindWelcome, readEnv(t, a).Type)
	require.Equal(t, transport.KindWelcome, readEnv(t, b).Type)

	older := sampleCreds(time.Now().Add(10*time.Minute), "OLDB")
	writeEnv(t, b, transport.KindSnapshotReport, transport.SnapshotReport{
		Profile: "p1", Path: "/tmp/b", Format: "claude-credentials-json-format",
		RawB64: base64.StdEncoding.EncodeToString(older), RawSize: len(older),
		LiveCheck: transport.LiveCheckMeta{Performed: false},
	})
	newer := sampleCreds(time.Now().Add(2*time.Hour), "NEWA")
	writeEnv(t, a, transport.KindSnapshotReport, transport.SnapshotReport{
		Profile: "p1", Path: "/tmp/a", Format: "claude-credentials-json-format",
		RawB64: base64.StdEncoding.EncodeToString(newer), RawSize: len(newer),
		LiveCheck: transport.LiveCheckMeta{Performed: false},
	})

	require.Equal(t, transport.KindTruthPush, readEnv(t, b).Type)

	deadline := time.After(3 * time.Second)
	for {
		select {
		case evt := <-events:
			if evt.EventKind != "" {
				continue
			}
			require.Equal(t, "p1", evt.Profile)
			require.Equal(t, "claude-credentials-json-format", evt.Format)
			require.Equal(t, "node-a", evt.SourceNode)
			require.Equal(t, []string{"node-b"}, evt.TargetNodes)
			require.Equal(t, []string{"node-a", "node-b"}, evt.Nodes)
			require.NotEmpty(t, evt.Fingerprint)
			require.NotEmpty(t, evt.RawB64)
			return
		case <-deadline:
			t.Fatal("timed out waiting for propagation notification")
		}
	}
}

func TestServer_PropagationNotifierEmitsForLateStaleJoiner(t *testing.T) {
	p := mintPKI(t)
	ps := config.NewProfileStore(testProfile([]string{"node-a", "node-b"}, 0))
	hub := NewHub(ps, logging.New(logging.Options{Level: "error"}))
	events := make(chan notifier.TruthRecord, 8)
	hub.SetPropagationNotifier(func(_ context.Context, r notifier.TruthRecord) {
		select {
		case events <- r:
		default:
		}
	})
	srv := startTestServerWithHub(t, p, ps, hub)
	defer srv.Close()

	a := dialClient(t, srv, p, p.clientACert, p.clientAKey, "p1")
	defer a.Close()
	writeEnv(t, a, transport.KindHello, transport.Hello{NodeID: "node-a", AgentVersion: "0.1", OS: "linux"})
	require.Equal(t, transport.KindWelcome, readEnv(t, a).Type)

	newer := sampleCreds(time.Now().Add(2*time.Hour), "NEWA")
	writeEnv(t, a, transport.KindSnapshotReport, transport.SnapshotReport{
		Profile: "p1", Path: "/tmp/a", Format: "claude-credentials-json-format",
		RawB64: base64.StdEncoding.EncodeToString(newer), RawSize: len(newer),
		LiveCheck: transport.LiveCheckMeta{Performed: false},
	})

	quietUntil := time.After(250 * time.Millisecond)
	for {
		select {
		case evt := <-events:
			if evt.EventKind != "" {
				continue
			}
			t.Fatalf("unexpected early propagation notification without targets: %+v", evt)
		case <-quietUntil:
			goto lateJoiner
		}
	}

lateJoiner:
	b := dialClient(t, srv, p, p.clientBCert, p.clientBKey, "p1")
	defer b.Close()
	writeEnv(t, b, transport.KindHello, transport.Hello{NodeID: "node-b", AgentVersion: "0.1", OS: "linux"})
	require.Equal(t, transport.KindWelcome, readEnv(t, b).Type)

	older := sampleCreds(time.Now().Add(10*time.Minute), "OLDB")
	writeEnv(t, b, transport.KindSnapshotReport, transport.SnapshotReport{
		Profile: "p1", Path: "/tmp/b", Format: "claude-credentials-json-format",
		RawB64: base64.StdEncoding.EncodeToString(older), RawSize: len(older),
		LiveCheck: transport.LiveCheckMeta{Performed: false},
	})

	require.Equal(t, transport.KindTruthPush, readEnv(t, b).Type)

	deadline := time.After(3 * time.Second)
	for {
		select {
		case evt := <-events:
			if evt.EventKind != "" {
				continue
			}
			require.Equal(t, "p1", evt.Profile)
			require.Equal(t, "claude-credentials-json-format", evt.Format)
			require.Equal(t, "node-a", evt.SourceNode)
			require.Equal(t, []string{"node-b"}, evt.TargetNodes)
			require.NotEmpty(t, evt.Fingerprint)
			return
		case <-deadline:
			t.Fatal("timed out waiting for late-join propagation notification")
		}
	}
}

func TestServer_TruthRestoredAndLostNotifications(t *testing.T) {
	p := mintPKI(t)
	ps := config.NewProfileStore(testProfile([]string{"node-a"}, 0))
	hub := NewHub(ps, logging.New(logging.Options{Level: "error"}))
	events := make(chan notifier.TruthRecord, 16)
	hub.SetPropagationNotifier(func(_ context.Context, r notifier.TruthRecord) {
		select {
		case events <- r:
		default:
		}
	})
	srv := startTestServerWithHub(t, p, ps, hub)
	defer srv.Close()

	a := dialClient(t, srv, p, p.clientACert, p.clientAKey, "p1")
	writeEnv(t, a, transport.KindHello, transport.Hello{NodeID: "node-a", AgentVersion: "0.1", OS: "linux"})
	require.Equal(t, transport.KindWelcome, readEnv(t, a).Type)

	newer := sampleCreds(time.Now().Add(2*time.Hour), "NEWA")
	writeEnv(t, a, transport.KindSnapshotReport, transport.SnapshotReport{
		Profile: "p1", Path: "/tmp/a", Format: "claude-credentials-json-format",
		RawB64: base64.StdEncoding.EncodeToString(newer), RawSize: len(newer),
		LiveCheck: transport.LiveCheckMeta{Performed: false},
	})

	deadline := time.After(3 * time.Second)
	for {
		select {
		case evt := <-events:
			if evt.EventKind != notifier.EventTruthRestored {
				continue
			}
			require.Equal(t, "p1", evt.Profile)
			require.Equal(t, "node-a", evt.SourceNode)
			require.Equal(t, []string{"node-a"}, evt.Nodes)
			require.NotEmpty(t, evt.RawB64)
			goto disconnect
		case <-deadline:
			t.Fatal("timed out waiting for truth restored notification")
		}
	}

disconnect:
	require.NoError(t, a.Close())

	deadline = time.After(3 * time.Second)
	for {
		select {
		case evt := <-events:
			if evt.EventKind != notifier.EventTruthLost {
				continue
			}
			require.Equal(t, "p1", evt.Profile)
			require.NotEmpty(t, evt.Fingerprint)
			return
		case <-deadline:
			t.Fatal("timed out waiting for truth lost notification")
		}
	}
}

// TestServer_CNNotAllowed verifies rejection for a CN absent from ACL.
func TestServer_CNNotAllowed(t *testing.T) {
	p := mintPKI(t)
	ps := config.NewProfileStore(testProfile([]string{"node-a"}, 0)) // node-b not allowed
	srv := startTestServer(t, p, ps)
	defer srv.Close()

	b := dialClient(t, srv, p, p.clientBCert, p.clientBKey, "p1")
	defer b.Close()
	writeEnv(t, b, transport.KindHello, transport.Hello{NodeID: "node-b", AgentVersion: "0.1", OS: "linux"})

	_ = b.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, _, err := b.ReadMessage()
	require.Error(t, err)
	ce, ok := err.(*websocket.CloseError)
	require.True(t, ok, "expected CloseError, got %T: %v", err, err)
	require.Equal(t, websocket.ClosePolicyViolation, ce.Code)
}

// TestServer_UnknownProfile verifies 404 before upgrade.
func TestServer_UnknownProfile(t *testing.T) {
	p := mintPKI(t)
	ps := config.NewProfileStore(testProfile([]string{"node-a"}, 0))
	srv := startTestServer(t, p, ps)
	defer srv.Close()

	clientTLS, err := pki.ClientTLSConfig(p.caCert, p.clientACert, p.clientAKey, "localhost")
	require.NoError(t, err)
	u, _ := url.Parse(srv.URL)
	httpClient := &http.Client{Transport: &http.Transport{TLSClientConfig: clientTLS}}
	resp, err := httpClient.Get(fmt.Sprintf("https://localhost:%s/ws/profile/unknown", u.Port()))
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusNotFound, resp.StatusCode)
}

// TestServer_DisplaceSameCN verifies that a second connection from the same
// CN displaces the first (specs §E.9 corner).
func TestServer_DisplaceSameCN(t *testing.T) {
	p := mintPKI(t)
	ps := config.NewProfileStore(testProfile([]string{"node-a"}, 0))
	srv := startTestServer(t, p, ps)
	defer srv.Close()

	a1 := dialClient(t, srv, p, p.clientACert, p.clientAKey, "p1")
	defer a1.Close()
	writeEnv(t, a1, transport.KindHello, transport.Hello{NodeID: "node-a", AgentVersion: "0.1", OS: "linux"})
	require.Equal(t, transport.KindWelcome, readEnv(t, a1).Type)

	a2 := dialClient(t, srv, p, p.clientACert, p.clientAKey, "p1")
	defer a2.Close()
	writeEnv(t, a2, transport.KindHello, transport.Hello{NodeID: "node-a", AgentVersion: "0.1", OS: "linux"})
	require.Equal(t, transport.KindWelcome, readEnv(t, a2).Type)

	_ = a1.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, _, err := a1.ReadMessage()
	require.Error(t, err)
	if ce, ok := err.(*websocket.CloseError); ok {
		require.Equal(t, websocket.ClosePolicyViolation, ce.Code)
	}
}

func TestServer_JWTAuthorizationHeader(t *testing.T) {
	p := mintPKI(t)
	ps := config.NewProfileStore(testProfile(nil, 0))
	secretDir := t.TempDir()
	secret, err := jwtauth.GenerateSecret()
	require.NoError(t, err)
	secretPath := filepath.Join(secretDir, "jwt.secret")
	require.NoError(t, jwtauth.WriteSecretFile(secretPath, secret))
	srv := startJWTTestServer(t, p, ps, secretPath)
	defer srv.Close()

	tokenA, err := jwtauth.Issue(secret, "node-a", []string{"p1"}, "test-issuer", "test-audience", time.Now(), time.Hour)
	require.NoError(t, err)
	tokenB, err := jwtauth.Issue(secret, "node-b", []string{"p1"}, "test-issuer", "test-audience", time.Now(), time.Hour)
	require.NoError(t, err)

	a := dialJWTClient(t, srv, p, "p1", map[string]string{"Authorization": "Bearer " + tokenA})
	defer a.Close()
	b := dialJWTClient(t, srv, p, "p1", map[string]string{"Authorization": "Bearer " + tokenB})
	defer b.Close()

	writeEnv(t, a, transport.KindHello, transport.Hello{NodeID: "node-a", AgentVersion: "0.1", OS: "linux"})
	writeEnv(t, b, transport.KindHello, transport.Hello{NodeID: "node-b", AgentVersion: "0.1", OS: "linux"})
	require.Equal(t, transport.KindWelcome, readEnv(t, a).Type)
	require.Equal(t, transport.KindWelcome, readEnv(t, b).Type)
}

func TestServer_JWTFirstFrameFallback(t *testing.T) {
	p := mintPKI(t)
	ps := config.NewProfileStore(testProfile(nil, 0))
	secretDir := t.TempDir()
	secret, err := jwtauth.GenerateSecret()
	require.NoError(t, err)
	secretPath := filepath.Join(secretDir, "jwt.secret")
	require.NoError(t, jwtauth.WriteSecretFile(secretPath, secret))
	srv := startJWTTestServer(t, p, ps, secretPath)
	defer srv.Close()

	token, err := jwtauth.Issue(secret, "node-a", []string{"p1"}, "test-issuer", "test-audience", time.Now(), time.Hour)
	require.NoError(t, err)

	conn := dialJWTClient(t, srv, p, "p1", nil)
	defer conn.Close()
	writeEnv(t, conn, transport.KindAuthJWT, transport.AuthJWT{Token: token})
	writeEnv(t, conn, transport.KindHello, transport.Hello{NodeID: "node-a", AgentVersion: "0.1", OS: "linux"})
	require.Equal(t, transport.KindWelcome, readEnv(t, conn).Type)
}
