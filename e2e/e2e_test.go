package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/0xc0de1ab/pangaea/internal/client"
	"github.com/0xc0de1ab/pangaea/internal/config"
	"github.com/0xc0de1ab/pangaea/internal/jwtauth"
	"github.com/0xc0de1ab/pangaea/internal/logging"
	"github.com/0xc0de1ab/pangaea/internal/pki"
	"github.com/0xc0de1ab/pangaea/internal/reversebridge"
	"github.com/0xc0de1ab/pangaea/internal/server"
	_ "github.com/0xc0de1ab/pangaea/pkg/formats/claudecreds"
	"golang.org/x/sync/errgroup"
)

func TestE2E_MTLSReplication(t *testing.T) {
	t.Parallel()
	env := newE2EEnv(t, config.AuthModeMTLS)
	env.seed(t, sampleCreds(time.Now().Add(2*time.Hour), "NEW"), sampleCreds(time.Now().Add(5*time.Minute), "OLD"))
	run := env.start(t, startOptions{startClients: true})
	waitForFileContent(t, env.pathB, mustReadFile(t, env.pathA))
	run.stopAndWait(t)
}

func TestE2E_MTLSAbsentTargetBackfill(t *testing.T) {
	t.Parallel()
	env := newE2EEnv(t, config.AuthModeMTLS)
	newer := sampleCreds(time.Now().Add(2*time.Hour), "NEW")
	env.seed(t, newer, nil)
	run := env.start(t, startOptions{startClients: true})
	waitForFileContent(t, env.pathB, newer)
	run.stopAndWait(t)
}

func TestE2E_MTLSRestoreAfterDelete(t *testing.T) {
	t.Parallel()
	env := newE2EEnv(t, config.AuthModeMTLS)
	newer := sampleCreds(time.Now().Add(2*time.Hour), "NEW")
	env.seed(t, newer, sampleCreds(time.Now().Add(5*time.Minute), "OLD"))
	run := env.start(t, startOptions{startClients: true})
	waitForFileContent(t, env.pathB, newer)
	if err := os.Remove(env.pathB); err != nil {
		t.Fatal(err)
	}
	waitForFileContent(t, env.pathB, newer)
	run.stopAndWait(t)
}

func TestE2E_MTLSReverseReplication(t *testing.T) {
	t.Parallel()
	env := newE2EEnv(t, config.AuthModeMTLS)
	env.seed(t, sampleCreds(time.Now().Add(2*time.Hour), "NEW"), sampleCreds(time.Now().Add(5*time.Minute), "OLD"))

	reverseAddr := reserveAddr(t)
	env.clientB.Reverse = config.ReverseConfig{
		Listen: reverseAddr,
		PKI: config.ReversePKIPaths{
			CACert:     env.pki.caCert,
			ServerCert: env.pki.reverseServerCert,
			ServerKey:  env.pki.reverseServerKey,
		},
		AllowedPeers: []string{"bridge"},
	}
	env.server.SelfNode = config.SelfNodeConfig{
		Enabled:    true,
		ClientCert: env.pki.bridgeCert,
		ClientKey:  env.pki.bridgeKey,
	}
	profiles := env.ps.List()
	profiles[0].ReverseTargets = []config.ReverseTarget{{
		NodeID: "node-b",
		URL:    "wss://localhost" + reverseAddr[strings.LastIndex(reverseAddr, ":"):],
	}}
	env.ps = config.NewProfileStore(&config.ProfilesFile{Profiles: profiles})

	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	log := logging.New(logging.Options{Level: "error"})
	eg, egCtx := errgroup.WithContext(ctx)

	statusSock := filepath.Join(t.TempDir(), "pangaea-reverse.sock")
	eg.Go(func() error {
		return server.Run(egCtx, env.server, env.ps, server.Options{
			ServerVersion: "test",
			StatusSocket:  statusSock,
		}, log)
	})
	waitForHealth(t, env.server.Listen, env.pki, true)
	eg.Go(func() error {
		return client.Run(egCtx, env.clientA, client.Options{AgentVersion: "test"}, log)
	})
	eg.Go(func() error {
		return client.RunReverse(egCtx, env.clientB, client.Options{AgentVersion: "test"}, log)
	})
	eg.Go(func() error {
		return reversebridge.Run(egCtx, env.server, env.ps, reversebridge.Options{
			SocketPath: statusSock,
		}, log)
	})

	waitForFileContent(t, env.pathB, mustReadFile(t, env.pathA))
	cancel()
	if err := eg.Wait(); err != nil && err != context.Canceled {
		t.Fatalf("run: %v", err)
	}
}

func TestE2E_ServerStartsAfterClientsEventuallyConverges(t *testing.T) {
	t.Parallel()
	env := newE2EEnv(t, config.AuthModeMTLS)
	newer := sampleCreds(time.Now().Add(2*time.Hour), "NEW")
	env.seed(t, newer, sampleCreds(time.Now().Add(5*time.Minute), "OLD"))
	run := env.start(t, startOptions{
		startClients: true,
		serverDelay:  500 * time.Millisecond,
	})
	waitForFileContent(t, env.pathB, newer)
	run.stopAndWait(t)
}

func TestE2E_JWTFirstFrameReplication(t *testing.T) {
	t.Parallel()
	env := newE2EEnv(t, config.AuthModeJWT)
	env.clientA.AuthMode = config.AuthModeJWT
	env.clientA.PKI.ClientCert = ""
	env.clientA.PKI.ClientKey = ""
	env.clientA.JWT.TokenFile = env.writeToken(t, "node-a")
	env.clientA.JWT.SendVia = config.JWTSendViaFirstFrame

	env.clientB.AuthMode = config.AuthModeJWT
	env.clientB.PKI.ClientCert = ""
	env.clientB.PKI.ClientKey = ""
	env.clientB.JWT.TokenFile = env.writeToken(t, "node-b")
	env.clientB.JWT.SendVia = config.JWTSendViaFirstFrame

	env.seed(t, sampleCreds(time.Now().Add(2*time.Hour), "NEW"), sampleCreds(time.Now().Add(5*time.Minute), "OLD"))
	run := env.start(t, startOptions{startClients: true})
	waitForFileContent(t, env.pathB, mustReadFile(t, env.pathA))
	run.stopAndWait(t)
}

func TestE2E_JWTHeaderReplication(t *testing.T) {
	t.Parallel()
	env := newE2EEnv(t, config.AuthModeJWT)
	env.clientA.AuthMode = config.AuthModeJWT
	env.clientA.PKI.ClientCert = ""
	env.clientA.PKI.ClientKey = ""
	env.clientA.JWT.TokenFile = env.writeToken(t, "node-a")
	env.clientA.JWT.SendVia = config.JWTSendViaHeader

	env.clientB.AuthMode = config.AuthModeJWT
	env.clientB.PKI.ClientCert = ""
	env.clientB.PKI.ClientKey = ""
	env.clientB.JWT.TokenFile = env.writeToken(t, "node-b")
	env.clientB.JWT.SendVia = config.JWTSendViaHeader

	env.seed(t, sampleCreds(time.Now().Add(2*time.Hour), "NEW"), sampleCreds(time.Now().Add(5*time.Minute), "OLD"))
	run := env.start(t, startOptions{startClients: true})
	waitForFileContent(t, env.pathB, mustReadFile(t, env.pathA))
	run.stopAndWait(t)
}

func TestE2E_MTLSIdentityMismatchRejected(t *testing.T) {
	t.Parallel()
	env := newE2EEnv(t, config.AuthModeMTLS)
	run := env.start(t, startOptions{startClients: false})
	defer run.stopAndWait(t)

	bad := cloneClientConfig(env.clientA)
	bad.NodeID = "wrong-node"
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	err := client.Run(ctx, bad, client.Options{AgentVersion: "test", FailFast: true}, logging.New(logging.Options{Level: "error"}))
	if err == nil {
		t.Fatalf("expected mtls identity mismatch to fail")
	}
}

func TestE2E_JWTProfileDeniedRejected(t *testing.T) {
	t.Parallel()
	env := newE2EEnv(t, config.AuthModeJWT)
	run := env.start(t, startOptions{startClients: false})
	defer run.stopAndWait(t)

	bad := cloneClientConfig(env.clientA)
	bad.AuthMode = config.AuthModeJWT
	bad.PKI.ClientCert = ""
	bad.PKI.ClientKey = ""
	bad.JWT.TokenFile = env.writeTokenForProfiles(t, "node-a", []string{"other-profile"})
	bad.JWT.SendVia = config.JWTSendViaHeader

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	err := client.Run(ctx, bad, client.Options{AgentVersion: "test", FailFast: true}, logging.New(logging.Options{Level: "error"}))
	if err == nil {
		t.Fatalf("expected jwt profile denial to fail")
	}
}

func TestE2E_SessionDisplacementKeepsReplacementLive(t *testing.T) {
	env := newE2EEnv(t, config.AuthModeMTLS)
	older := sampleCreds(time.Now().Add(5*time.Minute), "OLD")
	newer := sampleCreds(time.Now().Add(2*time.Hour), "NEW")
	env.seed(t, older, older)
	run := env.start(t, startOptions{startClients: false})
	defer run.stopAndWait(t)

	clientCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	log := logging.New(logging.Options{Level: "error"})

	// Make the first profile session the one that will be displaced. The
	// mediator must outlive request-scoped WebSocket contexts.
	b1 := cloneClientConfig(env.clientB)
	b1Dir := t.TempDir()
	b1.Profiles[0].Dir = b1Dir
	writeMaybeFile(t, filepath.Join(b1Dir, ".credentials.json"), older)
	b1Done := startClientAsync(clientCtx, b1, client.Options{AgentVersion: "test", FailFast: true}, log)

	time.Sleep(500 * time.Millisecond)

	startClientAsync(clientCtx, cloneClientConfig(env.clientB), client.Options{AgentVersion: "test"}, log)
	waitForClientExit(t, b1Done, 5*time.Second)
	writeMaybeFile(t, env.pathA, newer)
	startClientAsync(clientCtx, cloneClientConfig(env.clientA), client.Options{AgentVersion: "test"}, log)
	waitForFileContent(t, env.pathB, newer)

	// Make node-b diverge after the replacement session has already proven it
	// can receive truth from node-a. Re-writing node-a with the same truth
	// should still re-push to stale members under stale_only mode.
	writeMaybeFile(t, env.pathB, sampleCreds(time.Now().Add(4*time.Minute), "OLD2"))

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		writeMaybeFile(t, env.pathA, newer)
		if bytes.Equal(mustReadFile(t, env.pathB), newer) {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	waitForFileContent(t, env.pathB, newer)
}

func TestE2E_HealthTLSMatrix(t *testing.T) {
	t.Parallel()

	mtlsEnv := newE2EEnv(t, config.AuthModeMTLS)
	mtlsRun := mtlsEnv.start(t, startOptions{startClients: false})
	if status, err := getHealthStatus(mtlsEnv.server.Listen, mtlsEnv.pki, true); err != nil || status != http.StatusOK {
		t.Fatalf("mtls health with cert = (%d, %v), want 200", status, err)
	}
	if _, err := getHealthStatus(mtlsEnv.server.Listen, mtlsEnv.pki, false); err == nil {
		t.Fatalf("expected mtls health without client cert to fail")
	}
	mtlsRun.stopAndWait(t)

	jwtEnv := newE2EEnv(t, config.AuthModeJWT)
	jwtRun := jwtEnv.start(t, startOptions{startClients: false})
	if status, err := getHealthStatus(jwtEnv.server.Listen, jwtEnv.pki, false); err != nil || status != http.StatusOK {
		t.Fatalf("jwt health without cert = (%d, %v), want 200", status, err)
	}
	jwtRun.stopAndWait(t)
}

type e2eEnv struct {
	server  *config.ServerConfig
	ps      config.ProfileStore
	clientA *config.ClientConfig
	clientB *config.ClientConfig
	pathA   string
	pathB   string
	metaA   string
	metaB   string
	pki     testPKI
	secret  []byte
}

type startOptions struct {
	startClients bool
	serverDelay  time.Duration
}

type runningEnv struct {
	cancel context.CancelFunc
	done   chan error
}

type testPKI struct {
	caCert, serverCert, serverKey string
	clientACert, clientAKey       string
	clientBCert, clientBKey       string
	bridgeCert, bridgeKey         string
	reverseServerCert             string
	reverseServerKey              string
}

func newE2EEnv(t *testing.T, mode config.AuthMode) *e2eEnv {
	t.Helper()
	p := mintPKI(t)
	addr := reserveAddr(t)
	rootA := t.TempDir()
	rootB := t.TempDir()
	dirA := filepath.Join(rootA, ".claude")
	dirB := filepath.Join(rootB, ".claude")
	if err := os.MkdirAll(dirA, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dirB, 0o755); err != nil {
		t.Fatal(err)
	}
	pathA := filepath.Join(dirA, ".credentials.json")
	pathB := filepath.Join(dirB, ".credentials.json")
	metaA := filepath.Join(rootA, ".claude.json")
	metaB := filepath.Join(rootB, ".claude.json")
	writeClaudeMeta(t, metaA, "acct-1")
	writeClaudeMeta(t, metaB, "acct-1")

	profiles := config.NewProfileStore(&config.ProfilesFile{
		Profiles: []config.Profile{{
			Name:           "p1",
			Format:         "claude-credentials-json-format",
			Dir:            t.TempDir(),
			AllowedClients: []string{"node-a", "node-b"},
			Validate:       config.ValidateSpec{Strategy: "expires_at_max"},
			Propagate:      config.PropagateSpec{Mode: config.PropagateModeStaleOnly},
		}},
	})

	serverCfg := &config.ServerConfig{
		Listen:   addr,
		AuthMode: mode,
		PKI: config.PKIPaths{
			CACert:     p.caCert,
			ServerCert: p.serverCert,
			ServerKey:  p.serverKey,
		},
	}

	clientA := &config.ClientConfig{
		Server:   "wss://" + strings.Replace(addr, "127.0.0.1", "localhost", 1),
		AuthMode: mode,
		NodeID:   "node-a",
		Profiles: []config.ProfileBinding{{
			Name:            "p1",
			Format:          "claude-credentials-json-format",
			Dir:             dirA,
			AccountMetaPath: metaA,
		}},
		PKI: config.ClientPKIPaths{
			CACert:     p.caCert,
			ClientCert: p.clientACert,
			ClientKey:  p.clientAKey,
		},
		Reconnect: config.ReconnectConfig{
			InitialDelay: 50 * time.Millisecond,
			Jitter:       10 * time.Millisecond,
			MaxDelay:     500 * time.Millisecond,
		},
	}
	clientB := &config.ClientConfig{
		Server:   clientA.Server,
		AuthMode: mode,
		NodeID:   "node-b",
		Profiles: []config.ProfileBinding{{
			Name:            "p1",
			Format:          "claude-credentials-json-format",
			Dir:             dirB,
			AccountMetaPath: metaB,
		}},
		PKI: config.ClientPKIPaths{
			CACert:     p.caCert,
			ClientCert: p.clientBCert,
			ClientKey:  p.clientBKey,
		},
		Reconnect: clientA.Reconnect,
	}

	env := &e2eEnv{
		server:  serverCfg,
		ps:      profiles,
		clientA: clientA,
		clientB: clientB,
		pathA:   pathA,
		pathB:   pathB,
		metaA:   metaA,
		metaB:   metaB,
		pki:     p,
	}
	if mode == config.AuthModeJWT {
		secret, err := jwtauth.GenerateSecret()
		if err != nil {
			t.Fatal(err)
		}
		secretPath := filepath.Join(t.TempDir(), "jwt.secret")
		if err := jwtauth.WriteSecretFile(secretPath, secret); err != nil {
			t.Fatal(err)
		}
		env.secret = secret
		env.server.JWT = config.JWTServerConfig{
			SecretKeyFile: secretPath,
			Issuer:        "e2e-issuer",
			Audience:      "e2e-audience",
			AuthTimeout:   3 * time.Second,
		}
	}
	return env
}

func (e *e2eEnv) writeToken(t *testing.T, nodeID string) string {
	t.Helper()
	return e.writeTokenForProfiles(t, nodeID, []string{"p1"})
}

func (e *e2eEnv) writeTokenForProfiles(t *testing.T, nodeID string, profiles []string) string {
	t.Helper()
	token, err := jwtauth.Issue(e.secret, nodeID, profiles, e.server.JWT.Issuer, e.server.JWT.Audience, time.Now(), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), nodeID+".jwt")
	if err := os.WriteFile(path, []byte(token+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func (e *e2eEnv) seed(t *testing.T, a, b []byte) {
	t.Helper()
	writeMaybeFile(t, e.pathA, a)
	writeMaybeFile(t, e.pathB, b)
}

func (e *e2eEnv) start(t *testing.T, opts startOptions) *runningEnv {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	log := logging.New(logging.Options{Level: "error"})
	eg, egCtx := errgroup.WithContext(ctx)
	done := make(chan error, 1)

	startServer := func() {
		eg.Go(func() error {
			return server.Run(egCtx, e.server, e.ps, server.Options{ServerVersion: "test"}, log)
		})
		waitForHealth(t, e.server.Listen, e.pki, e.server.AuthMode == config.AuthModeMTLS)
	}
	startClients := func() {
		eg.Go(func() error {
			return client.Run(egCtx, e.clientA, client.Options{AgentVersion: "test"}, log)
		})
		eg.Go(func() error {
			return client.Run(egCtx, e.clientB, client.Options{AgentVersion: "test"}, log)
		})
	}

	if opts.serverDelay > 0 {
		if opts.startClients {
			startClients()
		}
		time.Sleep(opts.serverDelay)
		startServer()
	} else {
		startServer()
		if opts.startClients {
			startClients()
		}
	}

	go func() {
		done <- eg.Wait()
	}()
	return &runningEnv{
		cancel: cancel,
		done:   done,
	}
}

func (r *runningEnv) stopAndWait(t *testing.T) {
	t.Helper()
	r.cancel()
	if err := <-r.done; err != nil {
		t.Fatalf("run: %v", err)
	}
}

func writeMaybeFile(t *testing.T, path string, data []byte) {
	t.Helper()
	if data == nil {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
		return
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func waitForHealth(t *testing.T, addr string, p testPKI, useClientCert bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		status, err := getHealthStatus(addr, p, useClientCert)
		if err == nil && status == http.StatusOK {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("server at %s did not become healthy", addr)
}

func getHealthStatus(addr string, p testPKI, useClientCert bool) (int, error) {
	cert, key := "", ""
	if useClientCert {
		cert, key = p.clientACert, p.clientAKey
	}
	tlsCfg, err := pki.ClientTLSConfig(p.caCert, cert, key, "localhost")
	if err != nil {
		return 0, err
	}
	client := &http.Client{Transport: &http.Transport{TLSClientConfig: tlsCfg}, Timeout: time.Second}
	resp, err := client.Get("https://localhost" + addr[strings.LastIndex(addr, ":"):] + "/healthz")
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	return resp.StatusCode, nil
}

func mustReadFile(t *testing.T, path string) []byte {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func writeClaudeMeta(t *testing.T, path, accountUUID string) {
	t.Helper()
	body := map[string]any{
		"oauthAccount": map[string]any{
			"accountUuid":  accountUUID,
			"emailAddress": accountUUID + "@example.test",
		},
	}
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
}

func cloneClientConfig(in *config.ClientConfig) *config.ClientConfig {
	out := *in
	out.Profiles = append([]config.ProfileBinding(nil), in.Profiles...)
	out.PKI = in.PKI
	out.JWT = in.JWT
	out.Reconnect = in.Reconnect
	return &out
}

func startClientAsync(ctx context.Context, cfg *config.ClientConfig, opts client.Options, log *slog.Logger) <-chan error {
	done := make(chan error, 1)
	go func() {
		done <- client.Run(ctx, cfg, opts, log)
	}()
	return done
}

func waitForClientExit(t *testing.T, done <-chan error, timeout time.Duration) {
	t.Helper()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case err := <-done:
		if err == nil {
			t.Fatalf("expected displaced client to exit with an error")
		}
	case <-timer.C:
		t.Fatalf("client did not exit within %s", timeout)
	}
}

func waitForFileContent(t *testing.T, path string, want []byte) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		got, err := os.ReadFile(path)
		if err == nil && string(got) == string(want) {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	got, _ := os.ReadFile(path)
	t.Fatalf("file %s did not converge; got=%q", path, string(got))
}

func reserveAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().String()
}

func mintPKI(t *testing.T) testPKI {
	t.Helper()
	dir := t.TempDir()
	caExpiry := time.Now().Add(2 * time.Hour)
	leafExpiry := time.Now().Add(time.Hour)
	_, err := pki.NewCA(dir, "test-ca", caExpiry)
	if err != nil {
		t.Fatal(err)
	}
	ca, err := pki.LoadCA(filepath.Join(dir, "ca.crt"), filepath.Join(dir, "ca.key"))
	if err != nil {
		t.Fatal(err)
	}
	serverDir := filepath.Join(dir, "server")
	san := pki.SAN{IPs: []net.IP{net.ParseIP("127.0.0.1")}, DNS: []string{"localhost"}}
	if err := pki.IssueServer(ca, serverDir, "test-server", san, leafExpiry); err != nil {
		t.Fatal(err)
	}
	clientADir := filepath.Join(dir, "client-a")
	if err := pki.IssueClient(ca, clientADir, "node-a", leafExpiry); err != nil {
		t.Fatal(err)
	}
	clientBDir := filepath.Join(dir, "client-b")
	if err := pki.IssueClient(ca, clientBDir, "node-b", leafExpiry); err != nil {
		t.Fatal(err)
	}
	bridgeDir := filepath.Join(dir, "bridge")
	if err := pki.IssueClient(ca, bridgeDir, "bridge", leafExpiry); err != nil {
		t.Fatal(err)
	}
	reverseDir := filepath.Join(dir, "reverse-server")
	reverseSAN := pki.SAN{IPs: []net.IP{net.ParseIP("127.0.0.1")}, DNS: []string{"localhost"}}
	if err := pki.IssueServer(ca, reverseDir, "reverse-server", reverseSAN, leafExpiry); err != nil {
		t.Fatal(err)
	}
	return testPKI{
		caCert:            filepath.Join(dir, "ca.crt"),
		serverCert:        filepath.Join(serverDir, "server.crt"),
		serverKey:         filepath.Join(serverDir, "server.key"),
		clientACert:       filepath.Join(clientADir, "client.crt"),
		clientAKey:        filepath.Join(clientADir, "client.key"),
		clientBCert:       filepath.Join(clientBDir, "client.crt"),
		clientBKey:        filepath.Join(clientBDir, "client.key"),
		bridgeCert:        filepath.Join(bridgeDir, "client.crt"),
		bridgeKey:         filepath.Join(bridgeDir, "client.key"),
		reverseServerCert: filepath.Join(reverseDir, "server.crt"),
		reverseServerKey:  filepath.Join(reverseDir, "server.key"),
	}
}

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
	raw, _ := json.Marshal(body)
	return raw
}
