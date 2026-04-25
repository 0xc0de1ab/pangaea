package pki

import (
	"crypto/tls"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dh-kam/claude-creds-share/internal/common"
)

func mustNewCA(t *testing.T) (*CA, string) {
	t.Helper()
	dir := t.TempDir()
	ca, err := NewCA(dir, "test-ca", time.Now().Add(2*time.Hour))
	if err != nil {
		t.Fatalf("NewCA: %v", err)
	}
	return ca, dir
}

func TestNewCA_WritesArtifacts(t *testing.T) {
	ca, dir := mustNewCA(t)
	if !ca.Cert.IsCA {
		t.Fatalf("expected CA flag set")
	}
	for _, f := range []string{caCertFile, caKeyFile} {
		st, err := os.Stat(filepath.Join(dir, f))
		if err != nil {
			t.Fatalf("missing %s: %v", f, err)
		}
		if f == caKeyFile && st.Mode().Perm() != keyFileMode {
			t.Fatalf("ca.key mode = %v, want %v", st.Mode().Perm(), os.FileMode(keyFileMode))
		}
	}
	loaded, err := LoadCA(filepath.Join(dir, caCertFile), filepath.Join(dir, caKeyFile))
	if err != nil {
		t.Fatalf("LoadCA: %v", err)
	}
	if loaded.Cert.Subject.CommonName != "test-ca" {
		t.Fatalf("CN = %q", loaded.Cert.Subject.CommonName)
	}
}

func TestNewCA_NotAfterInPast(t *testing.T) {
	_, err := NewCA(t.TempDir(), "x", time.Now().Add(-1*time.Hour))
	if !errors.Is(err, common.ErrConfigInvalid) {
		t.Fatalf("err = %v, want ErrConfigInvalid", err)
	}
}

func TestIssueServer_EmptySAN(t *testing.T) {
	ca, _ := mustNewCA(t)
	err := IssueServer(ca, t.TempDir(), "srv", SAN{}, time.Now().Add(time.Hour))
	if !errors.Is(err, common.ErrConfigInvalid) {
		t.Fatalf("err = %v, want ErrConfigInvalid", err)
	}
}

func TestIssueServer_PastNotAfter(t *testing.T) {
	ca, _ := mustNewCA(t)
	err := IssueServer(ca, t.TempDir(), "srv", SAN{DNS: []string{"x"}}, time.Now().Add(-1*time.Minute))
	if !errors.Is(err, common.ErrConfigInvalid) {
		t.Fatalf("err = %v", err)
	}
}

func TestIssueServer_BackupOnReissue(t *testing.T) {
	ca, _ := mustNewCA(t)
	out := t.TempDir()
	if err := IssueServer(ca, out, "srv", SAN{DNS: []string{"localhost"}}, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := IssueServer(ca, out, "srv", SAN{DNS: []string{"localhost"}}, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(out)
	bakSeen := false
	for _, e := range entries {
		if strings.Contains(e.Name(), ".bak.") {
			bakSeen = true
		}
	}
	if !bakSeen {
		t.Fatalf("expected at least one .bak.* file in %s; got %v", out, entries)
	}
}

func TestIssueServer_IPv6AndWildcard(t *testing.T) {
	ca, _ := mustNewCA(t)
	out := t.TempDir()
	san := SAN{
		IPs: []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")},
		DNS: []string{"*.local", "host.example"},
	}
	if err := IssueServer(ca, out, "srv", san, time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("IssueServer: %v", err)
	}
	cert, err := loadCertFile(filepath.Join(out, serverCertFile))
	if err != nil {
		t.Fatal(err)
	}
	if len(cert.IPAddresses) != 2 {
		t.Fatalf("IP SAN count = %d", len(cert.IPAddresses))
	}
	gotWild := false
	for _, d := range cert.DNSNames {
		if d == "*.local" {
			gotWild = true
		}
	}
	if !gotWild {
		t.Fatalf("wildcard DNS missing in %v", cert.DNSNames)
	}
}

func TestLoadCAPool_BadFile(t *testing.T) {
	bad := filepath.Join(t.TempDir(), "not-a-pem")
	if err := os.WriteFile(bad, []byte("nope"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := LoadCAPool(bad)
	if !errors.Is(err, common.ErrConfigInvalid) {
		t.Fatalf("err = %v", err)
	}
}

func TestLoadCAPool_Missing(t *testing.T) {
	_, err := LoadCAPool(filepath.Join(t.TempDir(), "nope.pem"))
	if !errors.Is(err, common.ErrConfigInvalid) {
		t.Fatalf("err = %v", err)
	}
}

func TestPeerCN_NilState(t *testing.T) {
	_, err := PeerCN(nil)
	if !errors.Is(err, common.ErrCNMismatch) {
		t.Fatalf("err = %v", err)
	}
}

func TestPeerCN_NoVerifiedChain(t *testing.T) {
	state := &tls.ConnectionState{}
	_, err := PeerCN(state)
	if !errors.Is(err, common.ErrCNMismatch) {
		t.Fatalf("err = %v", err)
	}
}

// End-to-end: spin up an httptest TLS server with the issued server
// keypair + ClientCA pool, then dial it with the issued client cert. A
// successful handshake exercises ServerTLSConfig + ClientTLSConfig.
func TestRoundTrip_HTTPSHandshake(t *testing.T) {
	ca, caDir := mustNewCA(t)
	srvDir := filepath.Join(caDir, "server")
	cliDir := filepath.Join(caDir, "client")
	if err := IssueServer(ca, srvDir, "server", SAN{IPs: []net.IP{net.ParseIP("127.0.0.1")}, DNS: []string{"localhost"}}, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := IssueClient(ca, cliDir, "host-a", time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}

	caCert := filepath.Join(caDir, caCertFile)
	srvTLS, err := ServerTLSConfig(caCert, filepath.Join(srvDir, serverCertFile), filepath.Join(srvDir, serverKeyFile))
	if err != nil {
		t.Fatal(err)
	}
	cliTLS, err := ClientTLSConfig(caCert, filepath.Join(cliDir, clientCertFile), filepath.Join(cliDir, clientKeyFile), "localhost")
	if err != nil {
		t.Fatal(err)
	}

	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cn, err := PeerCN(r.TLS)
		if err != nil {
			t.Errorf("PeerCN: %v", err)
		}
		if cn != "host-a" {
			t.Errorf("CN = %q, want host-a", cn)
		}
		_, _ = io.WriteString(w, "ok")
	}))
	srv.TLS = srvTLS
	srv.StartTLS()
	defer srv.Close()

	// Rewrite host into the SAN-matching name.
	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	_, port, _ := net.SplitHostPort(u.Host)
	u.Host = "localhost:" + port

	transport := &http.Transport{TLSClientConfig: cliTLS}
	client := &http.Client{Transport: transport, Timeout: 5 * time.Second}
	resp, err := client.Get(u.String())
	if err != nil {
		t.Fatalf("client GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
}

func TestServerTLSConfig_BadKeypair(t *testing.T) {
	_, err := ServerTLSConfig(filepath.Join(t.TempDir(), "missing"), "x", "y")
	if !errors.Is(err, common.ErrConfigInvalid) {
		t.Fatalf("err = %v", err)
	}
}
