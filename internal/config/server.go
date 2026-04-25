package config

import (
	"bytes"
	"errors"
	"os"

	"gopkg.in/yaml.v3"

	"github.com/dh-kam/claude-creds-share/internal/common"
)

// ServerConfig mirrors specs §6.1.
type ServerConfig struct {
	Listen       string         `yaml:"listen"`
	PKI          PKIPaths       `yaml:"pki"`
	Log          LogConfig      `yaml:"log"`
	ProfilesFile string         `yaml:"profiles_file"`
	SelfNode     SelfNodeConfig `yaml:"self_node"`
}

// PKIPaths is the server's view of PKI material on disk.
type PKIPaths struct {
	CACert     string `yaml:"ca_cert"`
	ServerCert string `yaml:"server_cert"`
	ServerKey  string `yaml:"server_key"`
}

// LogConfig is the structured-logging knobs slice shared across configs.
type LogConfig struct {
	Level  string `yaml:"level"`
	Format string `yaml:"format"`
}

// SelfNodeConfig configures whether the server process additionally runs as
// its own client agent (the `--also-client` machinery in specs §15.3).
type SelfNodeConfig struct {
	Enabled    bool   `yaml:"enabled"`
	ClientCert string `yaml:"client_cert"`
	ClientKey  string `yaml:"client_key"`
}

// LoadServer parses server.yaml and validates it. Path fields are run through
// ExpandPath so downstream code receives concrete filesystem paths.
func LoadServer(path string) (*ServerConfig, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, common.Wrap(err, common.ErrConfigInvalid, common.MsgConfigMissing, path)
		}
		return nil, common.Wrap(err, common.ErrConfigInvalid, "read %s", path)
	}

	var c ServerConfig
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true)
	if err := dec.Decode(&c); err != nil {
		return nil, common.Wrap(err, common.ErrConfigInvalid, "parse %s", path)
	}

	if err := validateServer(&c); err != nil {
		return nil, err
	}
	if err := expandServerPaths(&c); err != nil {
		return nil, err
	}
	return &c, nil
}

func validateServer(c *ServerConfig) error {
	if c.Listen == "" {
		return common.Wrap(nil, common.ErrConfigInvalid, "server.listen is required")
	}
	if c.PKI.CACert == "" || c.PKI.ServerCert == "" || c.PKI.ServerKey == "" {
		return common.Wrap(nil, common.ErrConfigInvalid, "server.pki.{ca_cert,server_cert,server_key} are required")
	}
	if c.ProfilesFile == "" {
		return common.Wrap(nil, common.ErrConfigInvalid, "server.profiles_file is required")
	}
	if c.SelfNode.Enabled {
		if c.SelfNode.ClientCert == "" || c.SelfNode.ClientKey == "" {
			return common.Wrap(nil, common.ErrConfigInvalid, "self_node.client_cert and self_node.client_key are required when enabled")
		}
	}
	return nil
}

func expandServerPaths(c *ServerConfig) error {
	for _, p := range []*string{&c.PKI.CACert, &c.PKI.ServerCert, &c.PKI.ServerKey, &c.ProfilesFile, &c.SelfNode.ClientCert, &c.SelfNode.ClientKey} {
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
