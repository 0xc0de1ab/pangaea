package common

// Viper / flag key names. Keep these in one place so the CLI layer and the
// config loader reference identical strings.
const (
	// Server config keys (server.yaml).
	KeyServerListen         = "listen"
	KeyServerPKICA          = "pki.ca_cert"
	KeyServerPKICert        = "pki.server_cert"
	KeyServerPKIKey         = "pki.server_key"
	KeyServerLogLevel       = "log.level"
	KeyServerLogFormat      = "log.format"
	KeyServerProfilesFile   = "profiles_file"
	KeyServerSelfNodeEnable = "self_node.enabled"
	KeyServerSelfNodeCert   = "self_node.client_cert"
	KeyServerSelfNodeKey    = "self_node.client_key"

	// Client config keys (client.yaml).
	KeyClientServer           = "server"
	KeyClientProfile          = "profile"
	KeyClientNodeID           = "node_id"
	KeyClientPKICA            = "pki.ca_cert"
	KeyClientPKICert          = "pki.client_cert"
	KeyClientPKIKey           = "pki.client_key"
	KeyClientReconnectInitial = "reconnect.initial_delay"
	KeyClientReconnectJitter  = "reconnect.jitter"
	KeyClientReconnectMax     = "reconnect.max_delay"
	KeyClientLogLevel         = "log.level"
	KeyClientLogFormat        = "log.format"

	// CLI flag names (shared across cobra + viper via flagsbinder).
	FlagConfig     = "config"
	FlagAlsoClient = "also-client"
	FlagLogLevel   = "log-level"
	FlagLogFormat  = "log-format"
	FlagServer     = "server"
	FlagProfile    = "profile"
	FlagNodeID     = "node-id"
	FlagCAPath     = "ca"
	FlagOutDir     = "out"
	FlagSAN        = "san"
	FlagFailFast   = "fail-fast"
	FlagCommonName = "cn"

	// Environment variable prefix for viper.
	EnvPrefix = "CCS"
)
