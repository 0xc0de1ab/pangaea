package config

import (
	"bytes"
	"errors"
	"os"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/0xc0de1ab/pangaea/internal/common"
)

// ServerConfig mirrors specs §6.1.
type ServerConfig struct {
	Listen       string          `yaml:"listen"`
	AuthMode     AuthMode        `yaml:"auth_mode"`
	PKI          PKIPaths        `yaml:"pki"`
	JWT          JWTServerConfig `yaml:"jwt"`
	SSHNodes     []SSHNodeConfig `yaml:"ssh_nodes"`
	Log          LogConfig       `yaml:"log"`
	ProfilesFile string          `yaml:"profiles_file"`
	SelfNode     SelfNodeConfig  `yaml:"self_node"`
	Notifier     NotifierConfig  `yaml:"notifier"`
}

// NotifierConfig configures out-of-band reporting. Each sink is
// independently optional; any combination can be enabled and they all
// fan out the same per-(profile, account) summary. An unset notifier
// just means no out-of-band traffic.
type NotifierConfig struct {
	Telegram   TelegramConfig   `yaml:"telegram"`
	Slack      SlackConfig      `yaml:"slack"`
	Discord    DiscordConfig    `yaml:"discord"`
	Mattermost MattermostConfig `yaml:"mattermost"`
	Ntfy       NtfyConfig       `yaml:"ntfy"`
	Teams      TeamsConfig      `yaml:"teams"`
}

// TelegramConfig describes the Telegram bot wiring. BotTokenEnv names an
// environment variable that holds the bot secret (we deliberately do not
// accept the token literally in YAML so it cannot be committed). Routes
// pin (profile, account) tuples to specific chat IDs; the first match
// wins. DefaultChatID is the fallback when no route matches.
type TelegramConfig struct {
	Enabled             bool            `yaml:"enabled"`
	BotTokenEnv         string          `yaml:"bot_token_env"`
	DefaultChatID       string          `yaml:"default_chat_id"`
	Interval            time.Duration   `yaml:"-"`
	IntervalRaw         string          `yaml:"interval"`
	ProbeTimeout        time.Duration   `yaml:"-"`
	ProbeTimeoutRaw     string          `yaml:"probe_timeout"`
	DisableNotification bool            `yaml:"disable_notification"`
	Routes              []TelegramRoute `yaml:"routes"`
	// Endpoint overrides the default api.telegram.org host. Empty means
	// the public host. Useful for self-hosted bot proxies and tests.
	Endpoint string `yaml:"endpoint"`
}

// TelegramRoute pins one (profile, account) to one chat. Empty fields act
// as wildcards.
type TelegramRoute struct {
	Profile string `yaml:"profile"`
	Account string `yaml:"account"`
	ChatID  string `yaml:"chat_id"`
}

// SlackConfig describes the Slack incoming-webhook fan-out target. Webhook
// URLs are secrets and are only accepted via env-var indirection.
type SlackConfig struct {
	Enabled              bool          `yaml:"enabled"`
	DefaultWebhookURLEnv string        `yaml:"default_webhook_url_env"`
	Interval             time.Duration `yaml:"-"`
	IntervalRaw          string        `yaml:"interval"`
	ProbeTimeout         time.Duration `yaml:"-"`
	ProbeTimeoutRaw      string        `yaml:"probe_timeout"`
	Routes               []SlackRoute  `yaml:"routes"`
}

// SlackRoute pins one (profile, account) to one webhook URL. The URL
// itself is read from the named environment variable (so YAML never
// stores the secret).
type SlackRoute struct {
	Profile       string `yaml:"profile"`
	Account       string `yaml:"account"`
	WebhookURLEnv string `yaml:"webhook_url_env"`
}

// DiscordConfig — Discord channel webhooks.
type DiscordConfig struct {
	Enabled              bool           `yaml:"enabled"`
	DefaultWebhookURLEnv string         `yaml:"default_webhook_url_env"`
	Interval             time.Duration  `yaml:"-"`
	IntervalRaw          string         `yaml:"interval"`
	ProbeTimeout         time.Duration  `yaml:"-"`
	ProbeTimeoutRaw      string         `yaml:"probe_timeout"`
	Routes               []DiscordRoute `yaml:"routes"`
}

// DiscordRoute — webhook URL via env var, like Slack.
type DiscordRoute struct {
	Profile       string `yaml:"profile"`
	Account       string `yaml:"account"`
	WebhookURLEnv string `yaml:"webhook_url_env"`
}

// MattermostConfig — Mattermost incoming webhooks.
type MattermostConfig struct {
	Enabled              bool              `yaml:"enabled"`
	DefaultWebhookURLEnv string            `yaml:"default_webhook_url_env"`
	Interval             time.Duration     `yaml:"-"`
	IntervalRaw          string            `yaml:"interval"`
	ProbeTimeout         time.Duration     `yaml:"-"`
	ProbeTimeoutRaw      string            `yaml:"probe_timeout"`
	Routes               []MattermostRoute `yaml:"routes"`
}

// MattermostRoute — same shape as DiscordRoute.
type MattermostRoute struct {
	Profile       string `yaml:"profile"`
	Account       string `yaml:"account"`
	WebhookURLEnv string `yaml:"webhook_url_env"`
}

// NtfyConfig — ntfy.sh / self-hosted topics. AuthTokenEnv (optional) names
// the env var holding a Bearer token for access-controlled topics.
type NtfyConfig struct {
	Enabled            bool          `yaml:"enabled"`
	DefaultTopicURLEnv string        `yaml:"default_topic_url_env"`
	AuthTokenEnv       string        `yaml:"auth_token_env"`
	Priority           int           `yaml:"priority"`
	Tags               string        `yaml:"tags"`
	Interval           time.Duration `yaml:"-"`
	IntervalRaw        string        `yaml:"interval"`
	ProbeTimeout       time.Duration `yaml:"-"`
	ProbeTimeoutRaw    string        `yaml:"probe_timeout"`
	Routes             []NtfyRoute   `yaml:"routes"`
}

// NtfyRoute pins (profile, account) to one topic URL via env var.
type NtfyRoute struct {
	Profile     string `yaml:"profile"`
	Account     string `yaml:"account"`
	TopicURLEnv string `yaml:"topic_url_env"`
}

// TeamsConfig — Microsoft Teams (legacy Office 365 connector) incoming
// webhooks. ThemeColor is a hex string without leading `#`.
type TeamsConfig struct {
	Enabled              bool          `yaml:"enabled"`
	DefaultWebhookURLEnv string        `yaml:"default_webhook_url_env"`
	ThemeColor           string        `yaml:"theme_color"`
	Interval             time.Duration `yaml:"-"`
	IntervalRaw          string        `yaml:"interval"`
	ProbeTimeout         time.Duration `yaml:"-"`
	ProbeTimeoutRaw      string        `yaml:"probe_timeout"`
	Routes               []TeamsRoute  `yaml:"routes"`
}

// TeamsRoute — same shape as DiscordRoute.
type TeamsRoute struct {
	Profile       string `yaml:"profile"`
	Account       string `yaml:"account"`
	WebhookURLEnv string `yaml:"webhook_url_env"`
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

// SSHNodeConfig registers one SSH-reachable node the reverse bridge may use.
// Managed mode starts a remote reverse-client when ReverseAddr is empty.
type SSHNodeConfig struct {
	NodeID       string `yaml:"node_id"`
	Target       string `yaml:"target"`
	Port         int    `yaml:"port"`
	UseSSHConfig bool   `yaml:"use_ssh_config"`
	ReverseAddr  string `yaml:"reverse_addr"`
	Command      string `yaml:"command"`
	ConfigPath   string `yaml:"config_path"`
}

func (n *SSHNodeConfig) UnmarshalYAML(unmarshal func(any) error) error {
	aux := struct {
		NodeID       string `yaml:"node_id"`
		Target       string `yaml:"target"`
		Port         int    `yaml:"port"`
		UseSSHConfig *bool  `yaml:"use_ssh_config"`
		ReverseAddr  string `yaml:"reverse_addr"`
		Command      string `yaml:"command"`
		ConfigPath   string `yaml:"config_path"`
	}{}
	if err := unmarshal(&aux); err != nil {
		return err
	}
	n.NodeID = aux.NodeID
	n.Target = aux.Target
	n.Port = aux.Port
	n.ReverseAddr = aux.ReverseAddr
	n.Command = aux.Command
	n.ConfigPath = aux.ConfigPath
	if aux.UseSSHConfig == nil {
		n.UseSSHConfig = true
	} else {
		n.UseSSHConfig = *aux.UseSSHConfig
	}
	return nil
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
	if err := postProcessNotifier(&c.Notifier); err != nil {
		return nil, err
	}
	return &c, nil
}

func postProcessNotifier(n *NotifierConfig) error {
	t := &n.Telegram
	if t.IntervalRaw != "" {
		d, err := time.ParseDuration(t.IntervalRaw)
		if err != nil {
			return common.Wrap(err, common.ErrConfigInvalid, "notifier.telegram.interval %q", t.IntervalRaw)
		}
		t.Interval = d
	}
	if t.ProbeTimeoutRaw != "" {
		d, err := time.ParseDuration(t.ProbeTimeoutRaw)
		if err != nil {
			return common.Wrap(err, common.ErrConfigInvalid, "notifier.telegram.probe_timeout %q", t.ProbeTimeoutRaw)
		}
		t.ProbeTimeout = d
	}
	if t.Enabled {
		if t.BotTokenEnv == "" {
			return common.Wrap(nil, common.ErrConfigInvalid, "notifier.telegram.bot_token_env is required when telegram is enabled")
		}
		if t.DefaultChatID == "" && len(t.Routes) == 0 {
			return common.Wrap(nil, common.ErrConfigInvalid, "notifier.telegram needs default_chat_id or at least one route")
		}
	}

	if err := postProcessSlack(&n.Slack); err != nil {
		return err
	}
	if err := postProcessDiscord(&n.Discord); err != nil {
		return err
	}
	if err := postProcessMattermost(&n.Mattermost); err != nil {
		return err
	}
	if err := postProcessNtfy(&n.Ntfy); err != nil {
		return err
	}
	if err := postProcessTeams(&n.Teams); err != nil {
		return err
	}
	return nil
}

func postProcessSlack(s *SlackConfig) error {
	if err := parseDur(&s.Interval, s.IntervalRaw, "notifier.slack.interval"); err != nil {
		return err
	}
	if err := parseDur(&s.ProbeTimeout, s.ProbeTimeoutRaw, "notifier.slack.probe_timeout"); err != nil {
		return err
	}
	if s.Enabled {
		if s.DefaultWebhookURLEnv == "" && len(s.Routes) == 0 {
			return common.Wrap(nil, common.ErrConfigInvalid, "notifier.slack needs default_webhook_url_env or at least one route")
		}
		for i, r := range s.Routes {
			if r.WebhookURLEnv == "" {
				return common.Wrap(nil, common.ErrConfigInvalid, "notifier.slack.routes[%d].webhook_url_env is required", i)
			}
		}
	}
	return nil
}

func postProcessDiscord(d *DiscordConfig) error {
	if err := parseDur(&d.Interval, d.IntervalRaw, "notifier.discord.interval"); err != nil {
		return err
	}
	if err := parseDur(&d.ProbeTimeout, d.ProbeTimeoutRaw, "notifier.discord.probe_timeout"); err != nil {
		return err
	}
	if d.Enabled {
		if d.DefaultWebhookURLEnv == "" && len(d.Routes) == 0 {
			return common.Wrap(nil, common.ErrConfigInvalid, "notifier.discord needs default_webhook_url_env or at least one route")
		}
		for i, r := range d.Routes {
			if r.WebhookURLEnv == "" {
				return common.Wrap(nil, common.ErrConfigInvalid, "notifier.discord.routes[%d].webhook_url_env is required", i)
			}
		}
	}
	return nil
}

func postProcessMattermost(m *MattermostConfig) error {
	if err := parseDur(&m.Interval, m.IntervalRaw, "notifier.mattermost.interval"); err != nil {
		return err
	}
	if err := parseDur(&m.ProbeTimeout, m.ProbeTimeoutRaw, "notifier.mattermost.probe_timeout"); err != nil {
		return err
	}
	if m.Enabled {
		if m.DefaultWebhookURLEnv == "" && len(m.Routes) == 0 {
			return common.Wrap(nil, common.ErrConfigInvalid, "notifier.mattermost needs default_webhook_url_env or at least one route")
		}
		for i, r := range m.Routes {
			if r.WebhookURLEnv == "" {
				return common.Wrap(nil, common.ErrConfigInvalid, "notifier.mattermost.routes[%d].webhook_url_env is required", i)
			}
		}
	}
	return nil
}

func postProcessNtfy(n *NtfyConfig) error {
	if err := parseDur(&n.Interval, n.IntervalRaw, "notifier.ntfy.interval"); err != nil {
		return err
	}
	if err := parseDur(&n.ProbeTimeout, n.ProbeTimeoutRaw, "notifier.ntfy.probe_timeout"); err != nil {
		return err
	}
	if n.Enabled {
		if n.DefaultTopicURLEnv == "" && len(n.Routes) == 0 {
			return common.Wrap(nil, common.ErrConfigInvalid, "notifier.ntfy needs default_topic_url_env or at least one route")
		}
		for i, r := range n.Routes {
			if r.TopicURLEnv == "" {
				return common.Wrap(nil, common.ErrConfigInvalid, "notifier.ntfy.routes[%d].topic_url_env is required", i)
			}
		}
		if n.Priority != 0 && (n.Priority < 1 || n.Priority > 5) {
			return common.Wrap(nil, common.ErrConfigInvalid, "notifier.ntfy.priority must be 1..5 (got %d)", n.Priority)
		}
	}
	return nil
}

func postProcessTeams(t *TeamsConfig) error {
	if err := parseDur(&t.Interval, t.IntervalRaw, "notifier.teams.interval"); err != nil {
		return err
	}
	if err := parseDur(&t.ProbeTimeout, t.ProbeTimeoutRaw, "notifier.teams.probe_timeout"); err != nil {
		return err
	}
	if t.Enabled {
		if t.DefaultWebhookURLEnv == "" && len(t.Routes) == 0 {
			return common.Wrap(nil, common.ErrConfigInvalid, "notifier.teams needs default_webhook_url_env or at least one route")
		}
		for i, r := range t.Routes {
			if r.WebhookURLEnv == "" {
				return common.Wrap(nil, common.ErrConfigInvalid, "notifier.teams.routes[%d].webhook_url_env is required", i)
			}
		}
	}
	return nil
}

// parseDur parses raw into *out using time.ParseDuration. Empty raw is
// a no-op (the previously-set zero default is preserved). The label is
// embedded in any error.
func parseDur(out *time.Duration, raw, label string) error {
	if raw == "" {
		return nil
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		return common.Wrap(err, common.ErrConfigInvalid, "%s %q", label, raw)
	}
	*out = d
	return nil
}

func validateServer(c *ServerConfig) error {
	mode, err := normalizeAuthMode(c.AuthMode)
	if err != nil {
		return err
	}
	c.AuthMode = mode
	if c.JWT.AuthTimeoutRaw != "" {
		d, err := time.ParseDuration(c.JWT.AuthTimeoutRaw)
		if err != nil {
			return common.Wrap(err, common.ErrConfigInvalid, "server.jwt.auth_timeout %q", c.JWT.AuthTimeoutRaw)
		}
		if d <= 0 {
			return common.Wrap(nil, common.ErrConfigInvalid, "server.jwt.auth_timeout must be positive")
		}
		c.JWT.AuthTimeout = d
	} else {
		c.JWT.AuthTimeout = common.ReadTimeout
	}
	if c.Listen == "" {
		return common.Wrap(nil, common.ErrConfigInvalid, "server.listen is required")
	}
	if c.PKI.ServerCert == "" || c.PKI.ServerKey == "" {
		return common.Wrap(nil, common.ErrConfigInvalid, "server.pki.{server_cert,server_key} are required")
	}
	if c.AuthMode == AuthModeMTLS && c.PKI.CACert == "" {
		return common.Wrap(nil, common.ErrConfigInvalid, "server.pki.ca_cert is required when auth_mode=mtls")
	}
	if c.ProfilesFile == "" {
		return common.Wrap(nil, common.ErrConfigInvalid, "server.profiles_file is required")
	}
	if err := validateSSHNodes(c.SSHNodes); err != nil {
		return err
	}
	if c.SelfNode.Enabled {
		if c.SelfNode.ClientCert == "" || c.SelfNode.ClientKey == "" {
			return common.Wrap(nil, common.ErrConfigInvalid, "self_node.client_cert and self_node.client_key are required when enabled")
		}
	}
	if c.AuthMode == AuthModeJWT {
		if c.JWT.SecretKeyFile == "" {
			return common.Wrap(nil, common.ErrConfigInvalid, "server.jwt.secret_key_file is required when auth_mode=jwt")
		}
		if c.JWT.Issuer == "" {
			return common.Wrap(nil, common.ErrConfigInvalid, "server.jwt.issuer is required when auth_mode=jwt")
		}
		if c.JWT.Audience == "" {
			return common.Wrap(nil, common.ErrConfigInvalid, "server.jwt.audience is required when auth_mode=jwt")
		}
	}
	return nil
}

func expandServerPaths(c *ServerConfig) error {
	for _, p := range []*string{&c.PKI.CACert, &c.PKI.ServerCert, &c.PKI.ServerKey, &c.JWT.SecretKeyFile, &c.ProfilesFile, &c.SelfNode.ClientCert, &c.SelfNode.ClientKey} {
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

func validateSSHNodes(nodes []SSHNodeConfig) error {
	seen := make(map[string]struct{}, len(nodes))
	for _, node := range nodes {
		if node.NodeID == "" {
			return common.Wrap(nil, common.ErrConfigInvalid, "ssh_nodes.node_id is required")
		}
		if node.Target == "" {
			return common.Wrap(nil, common.ErrConfigInvalid, "ssh_nodes[%s].target is required", node.NodeID)
		}
		if node.Port < 0 || node.Port > 65535 {
			return common.Wrap(nil, common.ErrConfigInvalid, "ssh_nodes[%s].port must be 0 or between 1 and 65535", node.NodeID)
		}
		if _, dup := seen[node.NodeID]; dup {
			return common.Wrap(nil, common.ErrConfigInvalid, "duplicate ssh_nodes.node_id %q", node.NodeID)
		}
		seen[node.NodeID] = struct{}{}
	}
	return nil
}
