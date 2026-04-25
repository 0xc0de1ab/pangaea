package config

import (
	"bytes"
	"errors"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/dh-kam/claude-creds-share/internal/common"
)

// ClientConfig mirrors specs §6.3.
type ClientConfig struct {
	Server    string          `yaml:"server"`
	Profile   string          `yaml:"profile"`
	NodeID    string          `yaml:"node_id"`
	PKI       ClientPKIPaths  `yaml:"pki"`
	Reconnect ReconnectConfig `yaml:"reconnect"`
	Log       LogConfig       `yaml:"log"`
}

// ClientPKIPaths is the client's view of PKI material on disk.
type ClientPKIPaths struct {
	CACert     string `yaml:"ca_cert"`
	ClientCert string `yaml:"client_cert"`
	ClientKey  string `yaml:"client_key"`
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

type rawClientConfig struct {
	Server    string             `yaml:"server"`
	Profile   string             `yaml:"profile"`
	NodeID    string             `yaml:"node_id"`
	PKI       ClientPKIPaths     `yaml:"pki"`
	Reconnect rawReconnectConfig `yaml:"reconnect"`
	Log       LogConfig          `yaml:"log"`
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
		Server:  rc.Server,
		Profile: rc.Profile,
		NodeID:  rc.NodeID,
		PKI:     rc.PKI,
		Log:     rc.Log,
	}
	rec, err := convertReconnect(rc.Reconnect)
	if err != nil {
		return nil, err
	}
	c.Reconnect = rec

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
	if c.Server == "" || !strings.HasPrefix(c.Server, wssScheme) {
		return common.Wrap(nil, common.ErrConfigInvalid, "client.server must start with %q", wssScheme)
	}
	if c.Profile == "" {
		return common.Wrap(nil, common.ErrConfigInvalid, "client.profile is required")
	}
	if c.NodeID == "" {
		return common.Wrap(nil, common.ErrConfigInvalid, "client.node_id is required")
	}
	if c.PKI.CACert == "" || c.PKI.ClientCert == "" || c.PKI.ClientKey == "" {
		return common.Wrap(nil, common.ErrConfigInvalid, "client.pki.{ca_cert,client_cert,client_key} are required")
	}
	return nil
}

func expandClientPaths(c *ClientConfig) error {
	for _, p := range []*string{&c.PKI.CACert, &c.PKI.ClientCert, &c.PKI.ClientKey} {
		if *p == "" {
			continue
		}
		v, err := ExpandPath(*p)
		if err != nil {
			return err
		}
		*p = v
	}
	return nil
}
