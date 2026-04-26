package reversebridge

import (
	"bufio"
	"context"
	"io"
	"net"
	"net/url"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/0xc0de1ab/pangaea/internal/common"
	"github.com/0xc0de1ab/pangaea/internal/config"
	"github.com/0xc0de1ab/pangaea/internal/pki"
	"github.com/gorilla/websocket"
	sshconfig "github.com/kevinburke/ssh_config"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
	"golang.org/x/crypto/ssh/knownhosts"
)

const (
	defaultManagedConfigPath = "$HOME/pangaea-client.yaml"
	defaultManagedCommand    = "pangaeactl"
	defaultManagedListenAddr = "127.0.0.1:0"
)

type sshResolvedNode struct {
	NodeID       string
	Alias        string
	User         string
	Host         string
	Port         int
	ReverseAddr  string
	Command      string
	ConfigPath   string
	IdentityFile []string
	KnownHosts   []string
	AgentSocket  string
	StrictHost   string
}

func bridgeOnceSSH(ctx context.Context, serverCfg *config.ServerConfig, opts Options, target bridgeTarget) error {
	node, err := resolveSSHNode(serverCfg.SSHNodes, target.NodeID)
	if err != nil {
		return err
	}
	sshClient, err := dialSSH(ctx, node)
	if err != nil {
		return err
	}
	defer sshClient.Close()

	var (
		session     *ssh.Session
		sessionWait <-chan error
		reverseAddr = node.ReverseAddr
	)
	if reverseAddr == "" {
		var err error
		session, reverseAddr, sessionWait, err = startManagedReverseClient(ctx, sshClient, node, target.Profile)
		if err != nil {
			return err
		}
		defer stopManagedSession(session)
	}

	remoteConn, err := dialRemoteOverSSH(ctx, sshClient, serverCfg, target, reverseAddr)
	if err != nil {
		return err
	}
	defer remoteConn.Close()

	localConn, err := dialLocalAttach(ctx, opts.SocketPath, target)
	if err != nil {
		return err
	}
	defer localConn.Close()

	errCh := make(chan error, 3)
	go proxyWebsocket(localConn, remoteConn, errCh)
	go proxyWebsocket(remoteConn, localConn, errCh)
	if sessionWait != nil {
		go func() {
			if err := <-sessionWait; err != nil {
				errCh <- err
			}
		}()
	}

	select {
	case <-ctx.Done():
		return nil
	case err := <-errCh:
		return err
	}
}

func resolveSSHNode(nodes []config.SSHNodeConfig, nodeID string) (sshResolvedNode, error) {
	for _, node := range nodes {
		if node.NodeID != nodeID {
			continue
		}
		return materializeSSHNode(node)
	}
	return sshResolvedNode{}, common.Wrap(nil, common.ErrConfigInvalid, "ssh node %q not found in server.ssh_nodes", nodeID)
}

func materializeSSHNode(node config.SSHNodeConfig) (sshResolvedNode, error) {
	loginUser, alias := splitSSHTarget(node.Target)
	if alias == "" {
		return sshResolvedNode{}, common.Wrap(nil, common.ErrConfigInvalid, "ssh_nodes[%s].target is invalid", node.NodeID)
	}
	resolved := sshResolvedNode{
		NodeID:      node.NodeID,
		Alias:       alias,
		User:        loginUser,
		Host:        alias,
		Port:        22,
		ReverseAddr: strings.TrimSpace(node.ReverseAddr),
		Command:     strings.TrimSpace(node.Command),
		ConfigPath:  strings.TrimSpace(node.ConfigPath),
		StrictHost:  "yes",
	}
	if resolved.Command == "" {
		resolved.Command = defaultManagedCommand
	}
	if resolved.ConfigPath == "" {
		resolved.ConfigPath = defaultManagedConfigPath
	}
	if node.UseSSHConfig {
		if hostName, err := sshconfig.GetStrict(alias, "HostName"); err == nil && strings.TrimSpace(hostName) != "" {
			resolved.Host = strings.TrimSpace(hostName)
		}
		if resolved.User == "" {
			if userName, err := sshconfig.GetStrict(alias, "User"); err == nil && strings.TrimSpace(userName) != "" {
				resolved.User = strings.TrimSpace(userName)
			}
		}
		if node.Port == 0 {
			if portVal, err := sshconfig.GetStrict(alias, "Port"); err == nil && strings.TrimSpace(portVal) != "" {
				p, convErr := strconv.Atoi(strings.TrimSpace(portVal))
				if convErr != nil {
					return sshResolvedNode{}, common.Wrap(convErr, common.ErrConfigInvalid, "ssh_nodes[%s].port from ssh config", node.NodeID)
				}
				resolved.Port = p
			}
		}
		if strict, err := sshconfig.GetStrict(alias, "StrictHostKeyChecking"); err == nil && strings.TrimSpace(strict) != "" {
			resolved.StrictHost = strings.ToLower(strings.TrimSpace(strict))
		}
		if socket, err := sshconfig.GetStrict(alias, "IdentityAgent"); err == nil && strings.TrimSpace(socket) != "" {
			resolved.AgentSocket = strings.TrimSpace(socket)
		}
		if files, err := sshconfig.GetAllStrict(alias, "UserKnownHostsFile"); err == nil {
			for _, file := range files {
				resolved.KnownHosts = append(resolved.KnownHosts, strings.Fields(file)...)
			}
		}
		if files, err := sshconfig.GetAllStrict(alias, "IdentityFile"); err == nil {
			resolved.IdentityFile = append(resolved.IdentityFile, files...)
		}
	}
	if node.Port > 0 {
		resolved.Port = node.Port
	}
	if resolved.User == "" {
		if current, err := user.Current(); err == nil && current.Username != "" {
			resolved.User = current.Username
		}
	}
	if len(resolved.KnownHosts) == 0 {
		resolved.KnownHosts = append(resolved.KnownHosts,
			filepath.Join("~", ".ssh", "known_hosts"),
			filepath.Join("~", ".ssh", "known_hosts2"),
		)
	}
	if resolved.AgentSocket == "" || resolved.AgentSocket == "SSH_AUTH_SOCK" {
		resolved.AgentSocket = os.Getenv("SSH_AUTH_SOCK")
	}
	resolved.KnownHosts = dedupeStrings(expandLocalPaths(resolved.KnownHosts))
	resolved.IdentityFile = dedupeStrings(expandLocalPaths(append(resolved.IdentityFile, defaultIdentityFiles()...)))
	return resolved, nil
}

func dialSSH(ctx context.Context, node sshResolvedNode) (*ssh.Client, error) {
	auth, err := sshAuthMethods(node)
	if err != nil {
		return nil, err
	}
	hostKeyCallback, err := sshHostKeyCallback(node)
	if err != nil {
		return nil, err
	}
	cfg := &ssh.ClientConfig{
		User:            node.User,
		Auth:            auth,
		HostKeyCallback: hostKeyCallback,
		HostKeyAlgorithms: []string{
			ssh.CertAlgoED25519v01,
			ssh.CertAlgoECDSA256v01,
			ssh.CertAlgoECDSA384v01,
			ssh.CertAlgoECDSA521v01,
			ssh.CertAlgoRSASHA512v01,
			ssh.CertAlgoRSASHA256v01,
			ssh.KeyAlgoED25519,
			ssh.KeyAlgoECDSA256,
			ssh.KeyAlgoECDSA384,
			ssh.KeyAlgoECDSA521,
			ssh.KeyAlgoRSASHA512,
			ssh.KeyAlgoRSASHA256,
			ssh.KeyAlgoRSA,
		},
	}
	addr := net.JoinHostPort(node.Host, strconv.Itoa(node.Port))
	var d net.Dialer
	rawConn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, err
	}
	cc, chans, reqs, err := ssh.NewClientConn(rawConn, addr, cfg)
	if err != nil {
		_ = rawConn.Close()
		return nil, err
	}
	return ssh.NewClient(cc, chans, reqs), nil
}

func sshAuthMethods(node sshResolvedNode) ([]ssh.AuthMethod, error) {
	var methods []ssh.AuthMethod
	if node.AgentSocket != "" {
		if conn, err := net.Dial("unix", node.AgentSocket); err == nil {
			ag := agent.NewClient(conn)
			methods = append(methods, ssh.PublicKeysCallback(ag.Signers))
		}
	}
	for _, path := range node.IdentityFile {
		if path == "" {
			continue
		}
		key, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		signer, err := ssh.ParsePrivateKey(key)
		if err != nil {
			continue
		}
		methods = append(methods, ssh.PublicKeys(signer))
	}
	if len(methods) == 0 {
		return nil, common.Wrap(nil, common.ErrConfigInvalid, "no usable SSH auth method found for target %q", node.Alias)
	}
	return methods, nil
}

func sshHostKeyCallback(node sshResolvedNode) (ssh.HostKeyCallback, error) {
	files := existingFiles(node.KnownHosts)
	switch node.StrictHost {
	case "no", "off":
		return ssh.InsecureIgnoreHostKey(), nil
	case "yes", "ask", "accept-new", "":
		if len(files) == 0 {
			return nil, common.Wrap(nil, common.ErrConfigInvalid, "no known_hosts files found for SSH target %q", node.Alias)
		}
		return knownhosts.New(files...)
	default:
		if len(files) == 0 {
			return nil, common.Wrap(nil, common.ErrConfigInvalid, "unsupported StrictHostKeyChecking=%q for SSH target %q", node.StrictHost, node.Alias)
		}
		return knownhosts.New(files...)
	}
}

func startManagedReverseClient(ctx context.Context, client *ssh.Client, node sshResolvedNode, profile string) (*ssh.Session, string, <-chan error, error) {
	session, err := client.NewSession()
	if err != nil {
		return nil, "", nil, err
	}
	stdout, err := session.StdoutPipe()
	if err != nil {
		_ = session.Close()
		return nil, "", nil, err
	}
	stderr, err := session.StderrPipe()
	if err != nil {
		_ = session.Close()
		return nil, "", nil, err
	}
	if err := session.Start(buildManagedRemoteCommand(node, profile)); err != nil {
		_ = session.Close()
		return nil, "", nil, err
	}
	go io.Copy(io.Discard, stderr)
	reader := bufio.NewReader(stdout)
	addrCh := make(chan struct {
		addr string
		err  error
	}, 1)
	go func() {
		line, err := reader.ReadString('\n')
		if err != nil {
			addrCh <- struct {
				addr string
				err  error
			}{"", err}
			return
		}
		addrCh <- struct {
			addr string
			err  error
		}{strings.TrimSpace(line), nil}
		go io.Copy(io.Discard, reader)
	}()
	select {
	case <-ctx.Done():
		_ = session.Close()
		return nil, "", nil, ctx.Err()
	case res := <-addrCh:
		if res.err != nil {
			_ = session.Close()
			return nil, "", nil, res.err
		}
		waitCh := make(chan error, 1)
		go func() { waitCh <- session.Wait() }()
		return session, res.addr, waitCh, nil
	}
}

func stopManagedSession(session *ssh.Session) {
	if session == nil {
		return
	}
	_ = session.Signal(ssh.SIGTERM)
	_ = session.Close()
}

func dialRemoteOverSSH(ctx context.Context, sshClient *ssh.Client, serverCfg *config.ServerConfig, target bridgeTarget, reverseAddr string) (*websocket.Conn, error) {
	host, _, err := net.SplitHostPort(reverseAddr)
	if err != nil {
		return nil, err
	}
	u := &url.URL{
		Scheme: "wss",
		Host:   reverseAddr,
		Path:   common.DefaultReverseWSPath + target.Profile,
	}
	tlsCfg, err := pki.ClientTLSConfig(
		serverCfg.PKI.CACert,
		serverCfg.SelfNode.ClientCert,
		serverCfg.SelfNode.ClientKey,
		host,
	)
	if err != nil {
		return nil, err
	}
	d := &websocket.Dialer{
		TLSClientConfig:  tlsCfg,
		HandshakeTimeout: common.WriteTimeout,
		NetDialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			conn, err := sshClient.Dial("tcp", reverseAddr)
			if err != nil {
				return nil, err
			}
			return noDeadlineConn{Conn: conn}, nil
		},
	}
	conn, _, err := d.DialContext(ctx, u.String(), nil)
	return conn, err
}

func buildManagedRemoteCommand(node sshResolvedNode, profile string) string {
	var b strings.Builder
	b.WriteString("command -v ")
	b.WriteString(shellQuote(node.Command))
	b.WriteString(" >/dev/null 2>&1 || { echo missing-command >&2; exit 127; }; ")
	b.WriteString("exec ")
	b.WriteString(shellQuote(node.Command))
	b.WriteString(" reverse-client")
	b.WriteString(" -c ")
	b.WriteString(shellPathExpr(node.ConfigPath))
	b.WriteString(" --profile ")
	b.WriteString(shellQuote(profile))
	b.WriteString(" --listen ")
	b.WriteString(shellQuote(defaultManagedListenAddr))
	b.WriteString(" --print-listen-addr")
	b.WriteString(" --log-level warn --log-format json")
	return "sh -lc " + shellQuote(b.String())
}

func splitSSHTarget(target string) (string, string) {
	target = strings.TrimSpace(target)
	if target == "" {
		return "", ""
	}
	if idx := strings.LastIndex(target, "@"); idx >= 0 {
		return target[:idx], target[idx+1:]
	}
	return "", target
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'"'"'`) + "'"
}

func shellPathExpr(path string) string {
	switch {
	case strings.HasPrefix(path, "~/"):
		return `"$HOME"` + shellQuote(path[1:])
	case strings.HasPrefix(path, "$HOME/"):
		return `"$HOME"` + shellQuote(path[len("$HOME"):])
	default:
		return shellQuote(path)
	}
}

func expandLocalPaths(paths []string) []string {
	out := make([]string, 0, len(paths))
	for _, path := range paths {
		path = strings.TrimSpace(path)
		if path == "" {
			continue
		}
		expanded, err := config.ExpandPath(path)
		if err != nil {
			continue
		}
		out = append(out, expanded)
	}
	return out
}

func defaultIdentityFiles() []string {
	return []string{
		filepath.Join("~", ".ssh", "id_ed25519"),
		filepath.Join("~", ".ssh", "id_ecdsa"),
		filepath.Join("~", ".ssh", "id_rsa"),
	}
}

func dedupeStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func existingFiles(paths []string) []string {
	out := make([]string, 0, len(paths))
	for _, path := range paths {
		if path == "" {
			continue
		}
		if _, err := os.Stat(path); err == nil {
			out = append(out, path)
		}
	}
	return out
}

type noDeadlineConn struct {
	net.Conn
}

func (c noDeadlineConn) SetDeadline(time.Time) error      { return nil }
func (c noDeadlineConn) SetReadDeadline(time.Time) error  { return nil }
func (c noDeadlineConn) SetWriteDeadline(time.Time) error { return nil }
