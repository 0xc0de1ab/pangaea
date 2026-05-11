package main

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"os"
	"os/user"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/0xc0de1ab/pangaea/internal/common"
	"github.com/0xc0de1ab/pangaea/internal/config"
	"github.com/0xc0de1ab/pangaea/internal/jwtauth"
	"github.com/0xc0de1ab/pangaea/internal/pki"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

const (
	defaultSetupServerOut = "./deploy/server"
	defaultSetupClientOut = "./deploy/client"

	defaultCACommonName  = "pangaeactl Root CA"
	defaultCAYears       = 10
	defaultLeafYears     = 1
	defaultJWTIssuer     = "pangaea"
	defaultJWTAudience   = "pangaea"
	defaultTelegramEnv   = "TELEGRAM_API_TOKEN"
	defaultJWTTokenEnv   = "PANGAEA_JWT_TOKEN"
	defaultLogLevel      = "info"
	defaultLogFormat     = "json"
	defaultPropagateMode = config.PropagateModeStaleOnly
)

type setupProvider struct {
	Key              string
	Label            string
	Format           string
	DefaultDir       string
	DefaultWatch     []string
	AccountMetaPath  string
	ValidateStrategy string
	LiveCheck        bool
}

var setupProviders = map[string]setupProvider{
	"claude": {
		Key:              "claude",
		Label:            "Claude CLI",
		Format:           "claude-credentials-json-format",
		DefaultDir:       "~/.claude",
		DefaultWatch:     []string{".credentials.json", "~/.claude.json", ".config.json"},
		AccountMetaPath:  "~/.claude.json",
		ValidateStrategy: "expires_at_max",
		LiveCheck:        false,
	},
	"codex": {
		Key:              "codex",
		Label:            "Codex CLI",
		Format:           "codex-auth-json-format",
		DefaultDir:       "~/.codex",
		DefaultWatch:     []string{"auth.json"},
		ValidateStrategy: "jwt_exp_max",
		LiveCheck:        false,
	},
	"gemini": {
		Key:              "gemini",
		Label:            "Gemini CLI",
		Format:           "gemini-oauth-creds-json-format",
		DefaultDir:       "~/.gemini",
		DefaultWatch:     []string{"oauth_creds.json"},
		ValidateStrategy: "expiry_date_max",
		LiveCheck:        false,
	},
}

type promptUI struct {
	in  *bufio.Reader
	out io.Writer
}

type setupProfile struct {
	Name           string
	Provider       setupProvider
	Dir            string
	AllowedClients []string
}

type serverSetupSpec struct {
	BaseDir        string
	ConfigPath     string
	ProfilesPath   string
	Listen         string
	AuthMode       config.AuthMode
	TLSHost        string
	AdditionalSANs string
	CACommonName   string
	CAYears        int
	LeafYears      int
	CACertPath     string
	CAKeyPath      string
	ServerCertPath string
	ServerKeyPath  string
	JWTSecretPath  string
	JWTIssuer      string
	JWTAudience    string
	JWTFallback    bool
	Profiles       []setupProfile
	InitialNodes   []string

	IssueClientCerts bool
	IssueJWTTokens   bool

	TelegramEnabled      bool
	TelegramEnvVar       string
	TelegramDefaultChat  string
	TelegramSilent       bool
	ServerEnvFilePath    string
	GenerateSystemd      bool
	SystemdUser          string
	SystemdUnitPath      string
	AdvertisedClientHint string
}

type clientSetupSpec struct {
	BaseDir         string
	ConfigPath      string
	ServerURL       string
	AuthMode        config.AuthMode
	NodeID          string
	CACertPath      string
	ClientCertPath  string
	ClientKeyPath   string
	JWTTokenFile    string
	JWTTokenEnv     string
	JWTSendVia      string
	Profiles        []setupProfile
	GenerateSystemd bool
	SystemdUser     string
	SystemdUnitPath string
	ClientEnvPath   string
}

type serverSetupResult struct {
	ConfigPath       string
	ProfilesPath     string
	CACertPath       string
	ServerCertPath   string
	JWTSecretPath    string
	IssuedClientDirs map[string]string
	IssuedJWTTokens  map[string]string
	EnvFilePath      string
	SystemdUnitPath  string
}

type clientSetupResult struct {
	ConfigPath      string
	CACertPath      string
	ClientCertPath  string
	ClientKeyPath   string
	JWTTokenFile    string
	EnvFilePath     string
	SystemdUnitPath string
}

func newSetupCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "setup",
		Short:         common.CLIShortSetup,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	cmd.AddCommand(newSetupServerCmd())
	cmd.AddCommand(newSetupClientCmd())
	return cmd
}

func newSetupServerCmd() *cobra.Command {
	var outDir string
	cmd := &cobra.Command{
		Use:           "server",
		Short:         "interactively bootstrap server PKI/JWT/config files",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runSetupServer(cmd, outDir)
		},
	}
	cmd.Flags().StringVar(&outDir, common.FlagOutDir, defaultSetupServerOut, "output directory for generated server assets")
	return cmd
}

func newSetupClientCmd() *cobra.Command {
	var outDir string
	cmd := &cobra.Command{
		Use:           "client",
		Short:         "interactively bootstrap client config and auth material paths",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runSetupClient(cmd, outDir)
		},
	}
	cmd.Flags().StringVar(&outDir, common.FlagOutDir, defaultSetupClientOut, "output directory for generated client assets")
	return cmd
}

func runSetupServer(cmd *cobra.Command, outDir string) error {
	baseDir, err := ensureAbsDir(outDir)
	if err != nil {
		return err
	}
	ui := newPromptUI(cmd)
	fmt.Fprintf(cmd.OutOrStdout(), "Writing server assets under %s\n\n", baseDir)

	authMode, err := ui.choice("Auth mode", []string{string(config.AuthModeMTLS), string(config.AuthModeJWT)}, string(config.AuthModeMTLS))
	if err != nil {
		return err
	}
	listen, err := ui.required("Listen address", fmt.Sprintf("0.0.0.0:%d", common.DefaultPort))
	if err != nil {
		return err
	}
	defaultTLSHost := localHostname()
	tlsHost, err := ui.required("TLS host / SAN for the backend server certificate", defaultTLSHost)
	if err != nil {
		return err
	}
	additionalSANs, err := ui.string("Additional SANs (optional; comma-separated IP:... or DNS:...)", "")
	if err != nil {
		return err
	}
	caCN, err := ui.required("CA common name", defaultCACommonName)
	if err != nil {
		return err
	}
	caYears, err := ui.int("CA validity in years", defaultCAYears)
	if err != nil {
		return err
	}
	leafYears, err := ui.int("Leaf certificate validity in years", defaultLeafYears)
	if err != nil {
		return err
	}
	initialNodes, err := ui.list("Initial client node IDs (optional; comma-separated)", nil)
	if err != nil {
		return err
	}
	profiles, err := promptServerProfiles(ui, initialNodes)
	if err != nil {
		return err
	}

	spec := serverSetupSpec{
		BaseDir:              baseDir,
		ConfigPath:           filepath.Join(baseDir, "pangaea-server.yaml"),
		ProfilesPath:         filepath.Join(baseDir, "profiles.yaml"),
		Listen:               listen,
		AuthMode:             config.AuthMode(authMode),
		TLSHost:              tlsHost,
		AdditionalSANs:       additionalSANs,
		CACommonName:         caCN,
		CAYears:              caYears,
		LeafYears:            leafYears,
		CACertPath:           filepath.Join(baseDir, "pki", "ca.crt"),
		CAKeyPath:            filepath.Join(baseDir, "pki", "ca.key"),
		ServerCertPath:       filepath.Join(baseDir, "pki", "server", "server.crt"),
		ServerKeyPath:        filepath.Join(baseDir, "pki", "server", "server.key"),
		JWTSecretPath:        filepath.Join(baseDir, "jwt.secret"),
		Profiles:             profiles,
		InitialNodes:         initialNodes,
		ServerEnvFilePath:    filepath.Join(baseDir, "pangaea-server.env"),
		AdvertisedClientHint: fmt.Sprintf("wss://%s", net.JoinHostPort(tlsHost, listenPort(listen))),
	}

	if spec.AuthMode == config.AuthModeJWT {
		spec.JWTIssuer, err = ui.required("JWT issuer", defaultJWTIssuer)
		if err != nil {
			return err
		}
		spec.JWTAudience, err = ui.required("JWT audience", defaultJWTAudience)
		if err != nil {
			return err
		}
		spec.JWTFallback, err = ui.bool("Allow auth.jwt first-frame fallback when Authorization is missing", true)
		if err != nil {
			return err
		}
		spec.IssueJWTTokens, err = ui.bool("Issue initial JWT tokens for the listed node IDs now", len(spec.InitialNodes) > 0)
		if err != nil {
			return err
		}
	} else {
		spec.IssueClientCerts, err = ui.bool("Issue initial client certificates for the listed node IDs now", len(spec.InitialNodes) > 0)
		if err != nil {
			return err
		}
	}

	spec.TelegramEnabled, err = ui.bool("Enable Telegram notifications", false)
	if err != nil {
		return err
	}
	if spec.TelegramEnabled {
		spec.TelegramEnvVar, err = ui.required("Telegram bot token env var name", defaultTelegramEnv)
		if err != nil {
			return err
		}
		spec.TelegramDefaultChat, err = ui.required("Telegram default chat ID", "")
		if err != nil {
			return err
		}
		spec.TelegramSilent, err = ui.bool("Send Telegram notifications silently", false)
		if err != nil {
			return err
		}
	}

	spec.GenerateSystemd, err = ui.bool("Generate a systemd service unit for the server", false)
	if err != nil {
		return err
	}
	if spec.GenerateSystemd {
		spec.SystemdUser, err = ui.required("systemd service user", currentUsername())
		if err != nil {
			return err
		}
		spec.SystemdUnitPath, err = ui.required("systemd unit output path", filepath.Join(baseDir, "systemd", "pangaea-server.service"))
		if err != nil {
			return err
		}
	}

	fmt.Fprintln(cmd.OutOrStdout())
	fmt.Fprintln(cmd.OutOrStdout(), "Generating PKI/config files...")
	result, err := executeServerSetup(spec)
	if err != nil {
		return err
	}
	printServerSetupSummary(cmd.OutOrStdout(), spec, result)
	return nil
}

func runSetupClient(cmd *cobra.Command, outDir string) error {
	baseDir, err := ensureAbsDir(outDir)
	if err != nil {
		return err
	}
	ui := newPromptUI(cmd)
	fmt.Fprintf(cmd.OutOrStdout(), "Writing client assets under %s\n\n", baseDir)

	authMode, err := ui.choice("Auth mode", []string{string(config.AuthModeMTLS), string(config.AuthModeJWT)}, string(config.AuthModeMTLS))
	if err != nil {
		return err
	}
	serverURL, err := ui.required("Server URL", fmt.Sprintf("wss://hub.local:%d", common.DefaultPort))
	if err != nil {
		return err
	}
	nodeID, err := ui.required("Node ID", localHostname())
	if err != nil {
		return err
	}
	profiles, err := promptClientProfiles(ui)
	if err != nil {
		return err
	}

	spec := clientSetupSpec{
		BaseDir:    baseDir,
		ConfigPath: filepath.Join(baseDir, "pangaea-client.yaml"),
		ServerURL:  serverURL,
		AuthMode:   config.AuthMode(authMode),
		NodeID:     nodeID,
		CACertPath: filepath.Join(baseDir, "pki", "ca.crt"),
		Profiles:   profiles,
	}

	if spec.AuthMode == config.AuthModeMTLS {
		spec.ClientCertPath, err = ui.required("Client certificate path", filepath.Join(baseDir, "pki", "client.crt"))
		if err != nil {
			return err
		}
		spec.ClientKeyPath, err = ui.required("Client key path", filepath.Join(baseDir, "pki", "client.key"))
		if err != nil {
			return err
		}
	} else {
		tokenSource, err := ui.choice("JWT token source", []string{"file", "env"}, "file")
		if err != nil {
			return err
		}
		if tokenSource == "file" {
			spec.JWTTokenFile, err = ui.required("JWT token file path", filepath.Join(baseDir, "jwt.token"))
			if err != nil {
				return err
			}
		} else {
			spec.JWTTokenEnv, err = ui.required("JWT token env var name", defaultJWTTokenEnv)
			if err != nil {
				return err
			}
			spec.ClientEnvPath = filepath.Join(baseDir, "pangaea-client.env")
		}
		spec.JWTSendVia, err = ui.choice("JWT send mode", []string{config.JWTSendViaAuto, config.JWTSendViaHeader, config.JWTSendViaFirstFrame}, config.JWTSendViaAuto)
		if err != nil {
			return err
		}
	}

	spec.GenerateSystemd, err = ui.bool("Generate a systemd service unit for this client", false)
	if err != nil {
		return err
	}
	if spec.GenerateSystemd {
		spec.SystemdUser, err = ui.required("systemd service user", currentUsername())
		if err != nil {
			return err
		}
		spec.SystemdUnitPath, err = ui.required("systemd unit output path", filepath.Join(baseDir, "systemd", defaultClientUnitName(nodeID)))
		if err != nil {
			return err
		}
	}

	fmt.Fprintln(cmd.OutOrStdout())
	fmt.Fprintln(cmd.OutOrStdout(), "Generating client config...")
	result, err := executeClientSetup(spec)
	if err != nil {
		return err
	}
	printClientSetupSummary(cmd.OutOrStdout(), spec, result)
	return nil
}

func executeServerSetup(spec serverSetupSpec) (*serverSetupResult, error) {
	if err := os.MkdirAll(spec.BaseDir, 0o755); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Join(spec.BaseDir, "pki", "server"), 0o755); err != nil {
		return nil, err
	}
	caDir := filepath.Dir(spec.CACertPath)
	ca, err := pki.NewCA(caDir, spec.CACommonName, time.Now().AddDate(spec.CAYears, 0, 0))
	if err != nil {
		return nil, err
	}
	san, err := buildServerSAN(spec.TLSHost, spec.AdditionalSANs)
	if err != nil {
		return nil, err
	}
	if err := pki.IssueServer(ca, filepath.Dir(spec.ServerCertPath), spec.TLSHost, san, time.Now().AddDate(spec.LeafYears, 0, 0)); err != nil {
		return nil, err
	}

	profilesYAML, err := renderProfilesYAML(spec.Profiles)
	if err != nil {
		return nil, err
	}
	if err := writeTextFile(spec.ProfilesPath, profilesYAML, 0o644); err != nil {
		return nil, err
	}
	serverYAML, err := renderServerYAML(spec)
	if err != nil {
		return nil, err
	}
	if err := writeTextFile(spec.ConfigPath, serverYAML, 0o644); err != nil {
		return nil, err
	}

	result := &serverSetupResult{
		ConfigPath:       spec.ConfigPath,
		ProfilesPath:     spec.ProfilesPath,
		CACertPath:       spec.CACertPath,
		ServerCertPath:   spec.ServerCertPath,
		IssuedClientDirs: map[string]string{},
		IssuedJWTTokens:  map[string]string{},
	}

	if spec.TelegramEnabled {
		envBody := fmt.Sprintf("# Fill in before starting the server\n%s=\n", spec.TelegramEnvVar)
		if err := writeTextFile(spec.ServerEnvFilePath, envBody, 0o600); err != nil {
			return nil, err
		}
		result.EnvFilePath = spec.ServerEnvFilePath
	}

	if spec.AuthMode == config.AuthModeMTLS && spec.IssueClientCerts {
		for _, nodeID := range spec.InitialNodes {
			outDir := filepath.Join(spec.BaseDir, "issued-clients", nodeID)
			if err := pki.IssueClient(ca, outDir, nodeID, time.Now().AddDate(spec.LeafYears, 0, 0)); err != nil {
				return nil, err
			}
			if err := copyFile(spec.CACertPath, filepath.Join(outDir, "ca.crt"), 0o644); err != nil {
				return nil, err
			}
			result.IssuedClientDirs[nodeID] = outDir
		}
	}

	if spec.AuthMode == config.AuthModeJWT {
		secret, err := jwtauth.GenerateSecret()
		if err != nil {
			return nil, err
		}
		if err := os.MkdirAll(filepath.Dir(spec.JWTSecretPath), 0o755); err != nil {
			return nil, err
		}
		if err := jwtauth.WriteSecretFile(spec.JWTSecretPath, secret); err != nil {
			return nil, err
		}
		result.JWTSecretPath = spec.JWTSecretPath
		if spec.IssueJWTTokens {
			for _, nodeID := range spec.InitialNodes {
				allowed, err := allowedProfilesForNode(spec.Profiles, nodeID)
				if err != nil {
					return nil, err
				}
				token, err := jwtauth.Issue(secret, nodeID, allowed, spec.JWTIssuer, spec.JWTAudience, time.Now(), 30*24*time.Hour)
				if err != nil {
					return nil, err
				}
				tokenPath := filepath.Join(spec.BaseDir, "issued-jwt", nodeID+".token")
				if err := writeTextFile(tokenPath, token+"\n", 0o600); err != nil {
					return nil, err
				}
				result.IssuedJWTTokens[nodeID] = tokenPath
			}
		}
	}

	if spec.GenerateSystemd {
		unit := renderSystemdUnit(systemdUnitSpec{
			Description: fmt.Sprintf("pangaeactl server (%s)", spec.AuthMode),
			User:        spec.SystemdUser,
			WorkingDir:  spec.BaseDir,
			ExecStart:   fmt.Sprintf("%s serve -c %s", currentExecutable(), shellQuote(spec.ConfigPath)),
			EnvFile:     result.EnvFilePath,
		})
		if err := writeTextFile(spec.SystemdUnitPath, unit, 0o644); err != nil {
			return nil, err
		}
		result.SystemdUnitPath = spec.SystemdUnitPath
	}
	return result, nil
}

func executeClientSetup(spec clientSetupSpec) (*clientSetupResult, error) {
	if err := os.MkdirAll(spec.BaseDir, 0o755); err != nil {
		return nil, err
	}
	clientYAML, err := renderClientYAML(spec)
	if err != nil {
		return nil, err
	}
	if err := writeTextFile(spec.ConfigPath, clientYAML, 0o644); err != nil {
		return nil, err
	}
	result := &clientSetupResult{
		ConfigPath:     spec.ConfigPath,
		CACertPath:     spec.CACertPath,
		ClientCertPath: spec.ClientCertPath,
		ClientKeyPath:  spec.ClientKeyPath,
		JWTTokenFile:   spec.JWTTokenFile,
	}
	if spec.JWTTokenEnv != "" {
		body := fmt.Sprintf("# Fill in before starting the client\n%s=\n", spec.JWTTokenEnv)
		if err := writeTextFile(spec.ClientEnvPath, body, 0o600); err != nil {
			return nil, err
		}
		result.EnvFilePath = spec.ClientEnvPath
	}
	if spec.GenerateSystemd {
		unit := renderSystemdUnit(systemdUnitSpec{
			Description: fmt.Sprintf("pangaeactl client %s (%s)", spec.NodeID, spec.AuthMode),
			User:        spec.SystemdUser,
			WorkingDir:  spec.BaseDir,
			ExecStart:   fmt.Sprintf("%s connect -c %s", currentExecutable(), shellQuote(spec.ConfigPath)),
			EnvFile:     result.EnvFilePath,
		})
		if err := writeTextFile(spec.SystemdUnitPath, unit, 0o644); err != nil {
			return nil, err
		}
		result.SystemdUnitPath = spec.SystemdUnitPath
	}
	return result, nil
}

func promptServerProfiles(ui *promptUI, defaultAllowed []string) ([]setupProfile, error) {
	var profiles []setupProfile
	for {
		addPrompt := "Add a profile"
		addDefault := true
		if len(profiles) > 0 {
			addPrompt = "Add another profile"
			addDefault = false
		}
		ok, err := ui.bool(addPrompt, addDefault)
		if err != nil {
			return nil, err
		}
		if !ok {
			if len(profiles) == 0 {
				fmt.Fprintln(ui.out, "At least one profile is required.")
				continue
			}
			return profiles, nil
		}
		name, err := ui.required("Profile name", "")
		if err != nil {
			return nil, err
		}
		providerKey, err := ui.choice("Provider", []string{"claude", "codex", "gemini"}, "claude")
		if err != nil {
			return nil, err
		}
		provider := setupProviders[providerKey]
		dir, err := ui.required(fmt.Sprintf("%s credentials directory", provider.Label), provider.DefaultDir)
		if err != nil {
			return nil, err
		}
		allowed, err := ui.list("Allowed client node IDs for this profile", defaultAllowed)
		if err != nil {
			return nil, err
		}
		if len(allowed) == 0 {
			fmt.Fprintln(ui.out, "allowed_clients must not be empty.")
			continue
		}
		profiles = append(profiles, setupProfile{
			Name:           name,
			Provider:       provider,
			Dir:            dir,
			AllowedClients: allowed,
		})
	}
}

func promptClientProfiles(ui *promptUI) ([]setupProfile, error) {
	var profiles []setupProfile
	for {
		addPrompt := "Add a profile"
		addDefault := true
		if len(profiles) > 0 {
			addPrompt = "Add another profile"
			addDefault = false
		}
		ok, err := ui.bool(addPrompt, addDefault)
		if err != nil {
			return nil, err
		}
		if !ok {
			if len(profiles) == 0 {
				fmt.Fprintln(ui.out, "At least one profile is required.")
				continue
			}
			return profiles, nil
		}
		name, err := ui.required("Profile name", "")
		if err != nil {
			return nil, err
		}
		providerKey, err := ui.choice("Provider", []string{"claude", "codex", "gemini"}, "claude")
		if err != nil {
			return nil, err
		}
		provider := setupProviders[providerKey]
		dir, err := ui.required(fmt.Sprintf("%s credentials directory", provider.Label), provider.DefaultDir)
		if err != nil {
			return nil, err
		}
		profiles = append(profiles, setupProfile{
			Name:     name,
			Provider: provider,
			Dir:      dir,
		})
	}
}

func renderServerYAML(spec serverSetupSpec) (string, error) {
	type pkiBlock struct {
		CACert     string `yaml:"ca_cert"`
		ServerCert string `yaml:"server_cert"`
		ServerKey  string `yaml:"server_key"`
	}
	type logBlock struct {
		Level  string `yaml:"level"`
		Format string `yaml:"format"`
	}
	type telegramBlock struct {
		Enabled             bool   `yaml:"enabled"`
		BotTokenEnv         string `yaml:"bot_token_env,omitempty"`
		DefaultChatID       string `yaml:"default_chat_id,omitempty"`
		DisableNotification bool   `yaml:"disable_notification,omitempty"`
	}
	type notifierBlock struct {
		Telegram *telegramBlock `yaml:"telegram,omitempty"`
	}
	type jwtBlock struct {
		SecretKeyFile           string `yaml:"secret_key_file"`
		Issuer                  string `yaml:"issuer"`
		Audience                string `yaml:"audience"`
		AllowFirstFrameFallback *bool  `yaml:"allow_first_frame_fallback,omitempty"`
	}
	type doc struct {
		Listen       string          `yaml:"listen"`
		AuthMode     config.AuthMode `yaml:"auth_mode"`
		PKI          pkiBlock        `yaml:"pki"`
		JWT          *jwtBlock       `yaml:"jwt,omitempty"`
		Log          logBlock        `yaml:"log"`
		ProfilesFile string          `yaml:"profiles_file"`
		Notifier     *notifierBlock  `yaml:"notifier,omitempty"`
	}
	cfg := doc{
		Listen:       spec.Listen,
		AuthMode:     spec.AuthMode,
		PKI:          pkiBlock{CACert: spec.CACertPath, ServerCert: spec.ServerCertPath, ServerKey: spec.ServerKeyPath},
		Log:          logBlock{Level: defaultLogLevel, Format: defaultLogFormat},
		ProfilesFile: spec.ProfilesPath,
	}
	if spec.AuthMode == config.AuthModeJWT {
		fallback := spec.JWTFallback
		cfg.JWT = &jwtBlock{
			SecretKeyFile:           spec.JWTSecretPath,
			Issuer:                  spec.JWTIssuer,
			Audience:                spec.JWTAudience,
			AllowFirstFrameFallback: &fallback,
		}
	}
	if spec.TelegramEnabled {
		cfg.Notifier = &notifierBlock{
			Telegram: &telegramBlock{
				Enabled:             true,
				BotTokenEnv:         spec.TelegramEnvVar,
				DefaultChatID:       spec.TelegramDefaultChat,
				DisableNotification: spec.TelegramSilent,
			},
		}
	}
	return marshalYAML(cfg)
}

func renderProfilesYAML(profiles []setupProfile) (string, error) {
	type validateBlock struct {
		Strategy         string `yaml:"strategy"`
		LiveCheck        bool   `yaml:"live_check"`
		LiveCheckTimeout string `yaml:"live_check_timeout"`
	}
	type propagateBlock struct {
		Mode     string `yaml:"mode"`
		Cooldown string `yaml:"cooldown"`
	}
	type profileDoc struct {
		Name           string         `yaml:"name"`
		Format         string         `yaml:"format"`
		Dir            string         `yaml:"dir"`
		WatchFiles     []string       `yaml:"watch_files,omitempty"`
		AllowedClients []string       `yaml:"allowed_clients"`
		Validate       validateBlock  `yaml:"validate"`
		Propagate      propagateBlock `yaml:"propagate"`
	}
	type doc struct {
		Profiles []profileDoc `yaml:"profiles"`
	}
	out := doc{Profiles: make([]profileDoc, 0, len(profiles))}
	for _, p := range profiles {
		out.Profiles = append(out.Profiles, profileDoc{
			Name:           p.Name,
			Format:         p.Provider.Format,
			Dir:            p.Dir,
			WatchFiles:     append([]string(nil), p.Provider.DefaultWatch...),
			AllowedClients: append([]string(nil), p.AllowedClients...),
			Validate: validateBlock{
				Strategy:         p.Provider.ValidateStrategy,
				LiveCheck:        p.Provider.LiveCheck,
				LiveCheckTimeout: common.LiveCheckDefaultTimeout.String(),
			},
			Propagate: propagateBlock{
				Mode:     defaultPropagateMode,
				Cooldown: "2s",
			},
		})
	}
	return marshalYAML(out)
}

func renderClientYAML(spec clientSetupSpec) (string, error) {
	type pkiBlock struct {
		CACert     string `yaml:"ca_cert"`
		ClientCert string `yaml:"client_cert,omitempty"`
		ClientKey  string `yaml:"client_key,omitempty"`
	}
	type jwtBlock struct {
		TokenEnv  string `yaml:"token_env,omitempty"`
		TokenFile string `yaml:"token_file,omitempty"`
		SendVia   string `yaml:"send_via,omitempty"`
	}
	type reconnectBlock struct {
		InitialDelay string `yaml:"initial_delay"`
		Jitter       string `yaml:"jitter"`
		MaxDelay     string `yaml:"max_delay"`
	}
	type logBlock struct {
		Level  string `yaml:"level"`
		Format string `yaml:"format"`
	}
	type profileDoc struct {
		Name            string   `yaml:"name"`
		Format          string   `yaml:"format"`
		Dir             string   `yaml:"dir"`
		WatchFiles      []string `yaml:"watch_files,omitempty"`
		AccountMetaPath string   `yaml:"account_meta_path,omitempty"`
	}
	type doc struct {
		Server    string          `yaml:"server"`
		AuthMode  config.AuthMode `yaml:"auth_mode"`
		JWT       *jwtBlock       `yaml:"jwt,omitempty"`
		NodeID    string          `yaml:"node_id"`
		Profiles  []profileDoc    `yaml:"profiles"`
		PKI       pkiBlock        `yaml:"pki"`
		Reconnect reconnectBlock  `yaml:"reconnect"`
		Log       logBlock        `yaml:"log"`
	}
	out := doc{
		Server:   spec.ServerURL,
		AuthMode: spec.AuthMode,
		NodeID:   spec.NodeID,
		PKI: pkiBlock{
			CACert:     spec.CACertPath,
			ClientCert: spec.ClientCertPath,
			ClientKey:  spec.ClientKeyPath,
		},
		Reconnect: reconnectBlock{
			InitialDelay: common.ReconnectInitial.String(),
			Jitter:       common.ReconnectJitter.String(),
			MaxDelay:     common.ReconnectMax.String(),
		},
		Log: logBlock{Level: defaultLogLevel, Format: defaultLogFormat},
	}
	for _, p := range spec.Profiles {
		out.Profiles = append(out.Profiles, profileDoc{
			Name:            p.Name,
			Format:          p.Provider.Format,
			Dir:             p.Dir,
			WatchFiles:      append([]string(nil), p.Provider.DefaultWatch...),
			AccountMetaPath: p.Provider.AccountMetaPath,
		})
	}
	if spec.AuthMode == config.AuthModeJWT {
		out.JWT = &jwtBlock{
			TokenEnv:  spec.JWTTokenEnv,
			TokenFile: spec.JWTTokenFile,
			SendVia:   spec.JWTSendVia,
		}
	}
	return marshalYAML(out)
}

type systemdUnitSpec struct {
	Description string
	User        string
	WorkingDir  string
	ExecStart   string
	EnvFile     string
}

func renderSystemdUnit(spec systemdUnitSpec) string {
	var b strings.Builder
	b.WriteString("[Unit]\n")
	b.WriteString("Description=" + spec.Description + "\n")
	b.WriteString("After=network-online.target\n")
	b.WriteString("Wants=network-online.target\n\n")
	b.WriteString("[Service]\n")
	b.WriteString("Type=simple\n")
	if spec.User != "" {
		b.WriteString("User=" + spec.User + "\n")
		b.WriteString("Group=" + spec.User + "\n")
	}
	if spec.WorkingDir != "" {
		b.WriteString("WorkingDirectory=" + spec.WorkingDir + "\n")
	}
	if spec.EnvFile != "" {
		b.WriteString("EnvironmentFile=-" + spec.EnvFile + "\n")
	}
	b.WriteString("ExecStart=" + spec.ExecStart + "\n")
	b.WriteString("Restart=on-failure\n")
	b.WriteString("RestartSec=5s\n\n")
	b.WriteString("[Install]\n")
	b.WriteString("WantedBy=multi-user.target\n")
	return b.String()
}

func printServerSetupSummary(w io.Writer, spec serverSetupSpec, result *serverSetupResult) {
	fmt.Fprintln(w, "Server setup complete.")
	fmt.Fprintf(w, "Server config: %s\n", result.ConfigPath)
	fmt.Fprintf(w, "Profiles file: %s\n", result.ProfilesPath)
	fmt.Fprintf(w, "CA certificate: %s\n", result.CACertPath)
	fmt.Fprintf(w, "Server certificate: %s\n", result.ServerCertPath)
	if result.JWTSecretPath != "" {
		fmt.Fprintf(w, "JWT secret: %s\n", result.JWTSecretPath)
	}
	if result.EnvFilePath != "" {
		fmt.Fprintf(w, "Server env file: %s\n", result.EnvFilePath)
	}
	if result.SystemdUnitPath != "" {
		fmt.Fprintf(w, "systemd unit: %s\n", result.SystemdUnitPath)
	}
	if len(result.IssuedClientDirs) > 0 {
		fmt.Fprintln(w, "\nIssued client bundles:")
		for _, nodeID := range sortedMapKeys(result.IssuedClientDirs) {
			fmt.Fprintf(w, "- %s: %s\n", nodeID, result.IssuedClientDirs[nodeID])
		}
	}
	if len(result.IssuedJWTTokens) > 0 {
		fmt.Fprintln(w, "\nIssued JWT tokens:")
		for _, nodeID := range sortedMapKeys(result.IssuedJWTTokens) {
			fmt.Fprintf(w, "- %s: %s\n", nodeID, result.IssuedJWTTokens[nodeID])
		}
	}
	fmt.Fprintln(w, "\nNext steps:")
	fmt.Fprintf(w, "- Start the server: %s serve -c %s\n", currentExecutable(), result.ConfigPath)
	if result.SystemdUnitPath != "" {
		fmt.Fprintf(w, "- Install the unit: sudo cp %s /etc/systemd/system/\n", result.SystemdUnitPath)
		fmt.Fprintln(w, "- Reload and start: sudo systemctl daemon-reload && sudo systemctl enable --now pangaea-server.service")
	}
	fmt.Fprintf(w, "- Clients should trust %s and connect to %s (or your ingress URL if different).\n", result.CACertPath, spec.AdvertisedClientHint)
}

func printClientSetupSummary(w io.Writer, spec clientSetupSpec, result *clientSetupResult) {
	fmt.Fprintln(w, "Client setup complete.")
	fmt.Fprintf(w, "Client config: %s\n", result.ConfigPath)
	fmt.Fprintf(w, "Trusted CA certificate path: %s\n", result.CACertPath)
	if spec.AuthMode == config.AuthModeMTLS {
		fmt.Fprintf(w, "Client certificate path: %s\n", result.ClientCertPath)
		fmt.Fprintf(w, "Client key path: %s\n", result.ClientKeyPath)
	} else if result.JWTTokenFile != "" {
		fmt.Fprintf(w, "JWT token file path: %s\n", result.JWTTokenFile)
	}
	if result.EnvFilePath != "" {
		fmt.Fprintf(w, "Client env file: %s\n", result.EnvFilePath)
	}
	if result.SystemdUnitPath != "" {
		fmt.Fprintf(w, "systemd unit: %s\n", result.SystemdUnitPath)
	}
	fmt.Fprintln(w, "\nNext steps:")
	fmt.Fprintf(w, "- Put the required auth material at the paths above, then run: %s connect -c %s\n", currentExecutable(), result.ConfigPath)
	if result.SystemdUnitPath != "" {
		fmt.Fprintf(w, "- Install the unit: sudo cp %s /etc/systemd/system/\n", result.SystemdUnitPath)
		fmt.Fprintf(w, "- Reload and start: sudo systemctl daemon-reload && sudo systemctl enable --now %s\n", filepath.Base(result.SystemdUnitPath))
	}
}

func buildServerSAN(tlsHost, additional string) (pki.SAN, error) {
	var san pki.SAN
	if ip := net.ParseIP(tlsHost); ip != nil {
		san.IPs = append(san.IPs, ip)
	} else if strings.TrimSpace(tlsHost) != "" {
		san.DNS = append(san.DNS, strings.TrimSpace(tlsHost))
	}
	if strings.TrimSpace(additional) != "" {
		extra, err := parseSANs(additional)
		if err != nil {
			return pki.SAN{}, err
		}
		san.IPs = append(san.IPs, extra.IPs...)
		san.DNS = append(san.DNS, extra.DNS...)
	}
	if len(san.IPs) == 0 && len(san.DNS) == 0 {
		return san, fmt.Errorf("TLS host is required")
	}
	return san, nil
}

func allowedProfilesForNode(profiles []setupProfile, nodeID string) ([]string, error) {
	var names []string
	for _, p := range profiles {
		if slices.Contains(p.AllowedClients, nodeID) {
			names = append(names, p.Name)
		}
	}
	if len(names) == 0 {
		return nil, fmt.Errorf("node %q is not allowed by any configured profile", nodeID)
	}
	return names, nil
}

func marshalYAML(v any) (string, error) {
	raw, err := yaml.Marshal(v)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

func writeTextFile(path, body string, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if _, err := os.Stat(path); err == nil {
		bak := path + ".bak." + strconv.FormatInt(time.Now().UnixNano(), 10)
		if err := os.Rename(path, bak); err != nil {
			return err
		}
	}
	if err := os.WriteFile(path, []byte(body), mode); err != nil {
		return err
	}
	return os.Chmod(path, mode)
}

func copyFile(src, dst string, mode os.FileMode) error {
	raw, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return writeTextFile(dst, string(raw), mode)
}

func ensureAbsDir(path string) (string, error) {
	if path == "" {
		return "", fmt.Errorf("output directory is required")
	}
	return filepath.Abs(path)
}

func listenPort(listen string) string {
	_, port, err := net.SplitHostPort(listen)
	if err == nil && port != "" {
		return port
	}
	return strconv.Itoa(common.DefaultPort)
}

func sortedMapKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

func defaultClientUnitName(nodeID string) string {
	repl := strings.NewReplacer("/", "-", "\\", "-", " ", "-", ":", "-", ".", "-")
	return "pangaea-client-" + repl.Replace(nodeID) + ".service"
}

func currentExecutable() string {
	exe, err := os.Executable()
	if err != nil || exe == "" {
		return "pangaeactl"
	}
	return exe
}

func currentUsername() string {
	if u, err := user.Current(); err == nil && u.Username != "" {
		return u.Username
	}
	if v := strings.TrimSpace(os.Getenv("USER")); v != "" {
		return v
	}
	return "root"
}

func localHostname() string {
	if v, err := os.Hostname(); err == nil && strings.TrimSpace(v) != "" {
		return strings.TrimSpace(v)
	}
	return "localhost"
}

func shellQuote(s string) string {
	if s == "" {
		return "''"
	}
	if !strings.ContainsAny(s, " \t'\"") {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func newPromptUI(cmd *cobra.Command) *promptUI {
	return &promptUI{
		in:  bufio.NewReader(cmd.InOrStdin()),
		out: cmd.OutOrStdout(),
	}
}

func (ui *promptUI) string(label, def string) (string, error) {
	for {
		if def != "" {
			fmt.Fprintf(ui.out, "%s [%s]: ", label, def)
		} else {
			fmt.Fprintf(ui.out, "%s: ", label)
		}
		line, err := ui.in.ReadString('\n')
		if err != nil && err != io.EOF {
			return "", err
		}
		line = strings.TrimSpace(line)
		if line == "" {
			line = def
		}
		if err == io.EOF && line == "" {
			return "", io.EOF
		}
		return line, nil
	}
}

func (ui *promptUI) required(label, def string) (string, error) {
	for {
		v, err := ui.string(label, def)
		if err != nil {
			return "", err
		}
		if strings.TrimSpace(v) != "" {
			return v, nil
		}
		fmt.Fprintln(ui.out, "A value is required.")
	}
}

func (ui *promptUI) bool(label string, def bool) (bool, error) {
	defLabel := "y/N"
	if def {
		defLabel = "Y/n"
	}
	for {
		fmt.Fprintf(ui.out, "%s [%s]: ", label, defLabel)
		line, err := ui.in.ReadString('\n')
		if err != nil && err != io.EOF {
			return false, err
		}
		line = strings.TrimSpace(strings.ToLower(line))
		if line == "" {
			return def, nil
		}
		switch line {
		case "y", "yes":
			return true, nil
		case "n", "no":
			return false, nil
		default:
			fmt.Fprintln(ui.out, "Please answer yes or no.")
		}
	}
}

func (ui *promptUI) int(label string, def int) (int, error) {
	for {
		raw, err := ui.string(label, strconv.Itoa(def))
		if err != nil {
			return 0, err
		}
		n, err := strconv.Atoi(raw)
		if err != nil || n <= 0 {
			fmt.Fprintln(ui.out, "Please enter a positive integer.")
			continue
		}
		return n, nil
	}
}

func (ui *promptUI) list(label string, def []string) ([]string, error) {
	defRaw := strings.Join(def, ",")
	raw, err := ui.string(label, defRaw)
	if err != nil {
		return nil, err
	}
	return splitCSV(raw), nil
}

func (ui *promptUI) choice(label string, allowed []string, def string) (string, error) {
	allowedLower := make([]string, 0, len(allowed))
	for _, v := range allowed {
		allowedLower = append(allowedLower, strings.ToLower(v))
	}
	for {
		raw, err := ui.string(label+" ("+strings.Join(allowed, "/")+")", def)
		if err != nil {
			return "", err
		}
		raw = strings.ToLower(strings.TrimSpace(raw))
		if slices.Contains(allowedLower, raw) {
			return raw, nil
		}
		fmt.Fprintf(ui.out, "Please choose one of: %s\n", strings.Join(allowed, ", "))
	}
}

func splitCSV(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" || slices.Contains(out, p) {
			continue
		}
		out = append(out, p)
	}
	return out
}
