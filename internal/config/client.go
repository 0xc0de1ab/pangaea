package config

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/0xc0de1ab/pangaea/internal/common"
)

// ClientConfig mirrors specs §6.3.
//
// Profiles is the MVP extension to the spec: each binding pairs a server-side
// profile name with the local config directory and the format used to
// interpret it. The spec leaves this implicit (clients would reuse
// profiles.yaml);
// carrying it client-side avoids forcing clients to share profiles.yaml with
// the server. self-client callers fill the list from the resolved server
// profile. One client process opens one WebSocket session per binding.
type ClientConfig struct {
	Server      string            `yaml:"server"`
	AuthMode    AuthMode          `yaml:"auth_mode"`
	JWT         JWTClientConfig   `yaml:"jwt"`
	Reverse     ReverseConfig     `yaml:"reverse"`
	NodeID      string            `yaml:"node_id"`
	Profiles    []ProfileBinding  `yaml:"profiles"`
	PKI         ClientPKIPaths    `yaml:"pki"`
	Reconnect   ReconnectConfig   `yaml:"reconnect"`
	Maintenance MaintenanceConfig `yaml:"maintenance"`
	Log         LogConfig         `yaml:"log"`
}

// ProfileBinding describes one server profile the client wants to participate
// in. Format is the registered formats.Format name (e.g.
// "claude-credentials-json-format"); dir is the local config directory for
// that profile.
//
// AccountMetaPath is an optional path to a sibling file the format uses to
// derive an account identifier when the credentials file alone does not
// carry one (e.g. claude credentials.json — account info lives in
// ~/.claude.json). Formats that can derive the account from the credentials
// file directly (codex, gemini) ignore this field.
type ProfileBinding struct {
	Name            string   `yaml:"name"`
	Format          string   `yaml:"format"`
	Dir             string   `yaml:"dir"`
	WatchFiles      []string `yaml:"watch_files"`
	AccountMetaPath string   `yaml:"account_meta_path"`
}

// ClientPKIPaths is the client's view of PKI material on disk.
type ClientPKIPaths struct {
	CACert     string `yaml:"ca_cert"`
	ClientCert string `yaml:"client_cert"`
	ClientKey  string `yaml:"client_key"`
}

// ReverseConfig controls the optional reverse-client listener. In this mode
// the node does not dial the hub directly; instead it exposes a TLS
// WebSocket endpoint the server-side reverse connector can reach and bridge
// into the local hub over a unix attach socket.
type ReverseConfig struct {
	Listen       string          `yaml:"listen"`
	PKI          ReversePKIPaths `yaml:"pki"`
	AllowedPeers []string        `yaml:"allowed_peers"`
}

// ReversePKIPaths is the TLS server identity used by the reverse-client
// listener.
type ReversePKIPaths struct {
	CACert     string `yaml:"ca_cert"`
	ServerCert string `yaml:"server_cert"`
	ServerKey  string `yaml:"server_key"`
}

// ReconnectConfig captures the client's exponential-backoff reconnect policy.
// All three durations are required to be positive; defaults from common kick
// in for any field left at zero.
type ReconnectConfig struct {
	InitialDelay time.Duration `yaml:"initial_delay"`
	Jitter       time.Duration `yaml:"jitter"`
	MaxDelay     time.Duration `yaml:"max_delay"`
}

// rawReconnectConfig accepts duration strings; yaml.v3 has no built-in
// time.Duration support.
type rawReconnectConfig struct {
	InitialDelay string `yaml:"initial_delay"`
	Jitter       string `yaml:"jitter"`
	MaxDelay     string `yaml:"max_delay"`
}

// MaintenanceConfig controls client-side background maintenance tasks.
type MaintenanceConfig struct {
	CLIUpgrade CLIUpgradeConfig `yaml:"cli_upgrade"`
}

// CLIUpgradeConfig controls periodic official CLI package upgrades.
type CLIUpgradeConfig struct {
	Enabled      bool          `yaml:"enabled"`
	InitialDelay time.Duration `yaml:"-"`
	Interval     time.Duration `yaml:"-"`
}

type rawMaintenanceConfig struct {
	CLIUpgrade rawCLIUpgradeConfig `yaml:"cli_upgrade"`
}

type rawCLIUpgradeConfig struct {
	Enabled      *bool  `yaml:"enabled"`
	InitialDelay string `yaml:"initial_delay"`
	Interval     string `yaml:"interval"`
}

type rawClientConfig struct {
	Server      string               `yaml:"server"`
	AuthMode    AuthMode             `yaml:"auth_mode"`
	JWT         JWTClientConfig      `yaml:"jwt"`
	Reverse     ReverseConfig        `yaml:"reverse"`
	NodeID      string               `yaml:"node_id"`
	Profiles    []ProfileBinding     `yaml:"profiles"`
	PKI         ClientPKIPaths       `yaml:"pki"`
	Reconnect   rawReconnectConfig   `yaml:"reconnect"`
	Maintenance rawMaintenanceConfig `yaml:"maintenance"`
	Log         LogConfig            `yaml:"log"`
}

const wssScheme = "wss://"

// LoadClient parses client.yaml and validates it.
func LoadClient(path string) (*ClientConfig, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, common.Wrap(err, common.ErrConfigInvalid, common.MsgConfigMissing, path)
		}
		return nil, common.Wrap(err, common.ErrConfigInvalid, "read %s", path)
	}

	var rc rawClientConfig
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true)
	if err := dec.Decode(&rc); err != nil {
		return nil, common.Wrap(err, common.ErrConfigInvalid, "parse %s", path)
	}

	c := &ClientConfig{
		Server:   rc.Server,
		AuthMode: rc.AuthMode,
		JWT:      rc.JWT,
		Reverse:  rc.Reverse,
		NodeID:   rc.NodeID,
		Profiles: append([]ProfileBinding(nil), rc.Profiles...),
		PKI:      rc.PKI,
		Log:      rc.Log,
	}
	for i := range c.Profiles {
		c.Profiles[i].WatchFiles = append([]string(nil), c.Profiles[i].WatchFiles...)
	}
	rec, err := convertReconnect(rc.Reconnect)
	if err != nil {
		return nil, err
	}
	c.Reconnect = rec
	maintenance, err := convertMaintenance(rc.Maintenance)
	if err != nil {
		return nil, err
	}
	c.Maintenance = maintenance

	if err := validateClient(c); err != nil {
		return nil, err
	}
	if err := expandClientPaths(c); err != nil {
		return nil, err
	}
	return c, nil
}

func convertReconnect(rc rawReconnectConfig) (ReconnectConfig, error) {
	out := ReconnectConfig{}
	parsed, err := parseDurOrZero(rc.InitialDelay, "reconnect.initial_delay")
	if err != nil {
		return out, err
	}
	out.InitialDelay = parsed

	parsed, err = parseDurOrZero(rc.Jitter, "reconnect.jitter")
	if err != nil {
		return out, err
	}
	out.Jitter = parsed

	parsed, err = parseDurOrZero(rc.MaxDelay, "reconnect.max_delay")
	if err != nil {
		return out, err
	}
	out.MaxDelay = parsed

	if out.InitialDelay == 0 {
		out.InitialDelay = common.ReconnectInitial
	}
	if out.Jitter == 0 {
		out.Jitter = common.ReconnectJitter
	}
	if out.MaxDelay == 0 {
		out.MaxDelay = common.ReconnectMax
	}
	return out, nil
}

func convertMaintenance(rc rawMaintenanceConfig) (MaintenanceConfig, error) {
	cli, err := convertCLIUpgrade(rc.CLIUpgrade)
	if err != nil {
		return MaintenanceConfig{}, err
	}
	return MaintenanceConfig{CLIUpgrade: cli}, nil
}

func convertCLIUpgrade(rc rawCLIUpgradeConfig) (CLIUpgradeConfig, error) {
	out := CLIUpgradeConfig{
		Enabled:      true,
		InitialDelay: 10 * time.Minute,
		Interval:     24 * time.Hour,
	}
	if rc.Enabled != nil {
		out.Enabled = *rc.Enabled
	}
	parsed, err := parseDurOrZero(rc.InitialDelay, "maintenance.cli_upgrade.initial_delay")
	if err != nil {
		return out, err
	}
	if parsed > 0 {
		out.InitialDelay = parsed
	}
	parsed, err = parseDurOrZero(rc.Interval, "maintenance.cli_upgrade.interval")
	if err != nil {
		return out, err
	}
	if parsed > 0 {
		out.Interval = parsed
	}
	return out, nil
}

func parseDurOrZero(s, label string) (time.Duration, error) {
	if s == "" {
		return 0, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, common.Wrap(err, common.ErrConfigInvalid, "%s: %q", label, s)
	}
	if d < 0 {
		return 0, common.Wrap(nil, common.ErrConfigInvalid, "%s must be non-negative", label)
	}
	return d, nil
}

func validateClient(c *ClientConfig) error {
	mode, err := normalizeAuthMode(c.AuthMode)
	if err != nil {
		return err
	}
	c.AuthMode = mode
	sendVia, err := normalizeJWTSendVia(c.JWT.SendVia)
	if err != nil {
		return err
	}
	c.JWT.SendVia = sendVia
	if c.Server == "" || !strings.HasPrefix(c.Server, wssScheme) {
		return common.Wrap(nil, common.ErrConfigInvalid, "client.server must start with %q", wssScheme)
	}
	if c.NodeID == "" {
		return common.Wrap(nil, common.ErrConfigInvalid, "client.node_id is required")
	}
	if len(c.Profiles) == 0 {
		return common.Wrap(nil, common.ErrConfigInvalid, "client.profiles must list at least one binding")
	}
	seen := make(map[string]struct{}, len(c.Profiles))
	for i, p := range c.Profiles {
		if p.Name == "" {
			return common.Wrap(nil, common.ErrConfigInvalid, "client.profiles[%d].name is required", i)
		}
		if _, dup := seen[p.Name]; dup {
			return common.Wrap(nil, common.ErrConfigInvalid, "client.profiles: duplicate name %q", p.Name)
		}
		seen[p.Name] = struct{}{}
		if p.Dir == "" {
			return common.Wrap(nil, common.ErrConfigInvalid, "client.profiles[%d] (%s): dir must not be empty", i, p.Name)
		}
	}
	if c.PKI.CACert == "" {
		return common.Wrap(nil, common.ErrConfigInvalid, "client.pki.ca_cert is required")
	}
	if c.AuthMode == AuthModeMTLS {
		if c.PKI.ClientCert == "" || c.PKI.ClientKey == "" {
			return common.Wrap(nil, common.ErrConfigInvalid, "client.pki.{client_cert,client_key} are required when auth_mode=mtls")
		}
	}
	if c.AuthMode == AuthModeJWT {
		if c.JWT.TokenEnv == "" && c.JWT.TokenFile == "" {
			return common.Wrap(nil, common.ErrConfigInvalid, "client.jwt.token_env or client.jwt.token_file is required when auth_mode=jwt")
		}
	}
	return nil
}

func expandClientPaths(c *ClientConfig) error {
	for _, p := range []*string{
		&c.PKI.CACert,
		&c.PKI.ClientCert,
		&c.PKI.ClientKey,
		&c.JWT.TokenFile,
		&c.Reverse.PKI.CACert,
		&c.Reverse.PKI.ServerCert,
		&c.Reverse.PKI.ServerKey,
	} {
		if *p == "" {
			continue
		}
		v, err := ExpandPath(*p)
		if err != nil {
			return err
		}
		*p = v
	}
	for i := range c.Profiles {
		v, err := ExpandPath(c.Profiles[i].Dir)
		if err != nil {
			return err
		}
		c.Profiles[i].Dir = v
		if c.Profiles[i].AccountMetaPath != "" {
			v, err := ExpandPathFromDir(c.Profiles[i].Dir, c.Profiles[i].AccountMetaPath)
			if err != nil {
				return err
			}
			c.Profiles[i].AccountMetaPath = filepath.Clean(v)
		}
		for j, raw := range c.Profiles[i].WatchFiles {
			v, err := ExpandPathFromDir(c.Profiles[i].Dir, raw)
			if err != nil {
				return err
			}
			c.Profiles[i].WatchFiles[j] = filepath.Clean(v)
		}
	}
	return nil
}
