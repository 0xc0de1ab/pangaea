package main

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/dh-kam/refutils/flagsbinder"
	"github.com/google/antigravity-compat-proxy/internal/api"
	"github.com/google/antigravity-compat-proxy/internal/bridge"
	"github.com/google/antigravity-compat-proxy/internal/extserver"
	"github.com/google/antigravity-compat-proxy/internal/proc"
	"github.com/google/antigravity-compat-proxy/internal/scraper"
	"github.com/google/antigravity-compat-proxy/internal/utils"
	"github.com/spf13/cobra"
)

type serveOptions struct {
	DBPath              string `flag:"db-path" env:"STATE_VSCDB_PATH" usage:"Path to state.vscdb"`
	ServerPath          string `flag:"server-path" env:"SERVER_PATH" usage:"Path to antigravity-server index.js"`
	CorePath            string `flag:"core-path" env:"CORE_PATH" usage:"Path to ls_core binary"`
	InstallDir          string `flag:"install-dir" env:"INSTALL_DIR" usage:"Installation directory"`
	ProxyAddr           string `flag:"proxy-addr" env:"PROXY_ADDR" usage:"Address for the proxy server to listen on"`
	CoreAddr            string `flag:"core-addr" env:"CORE_ADDR" usage:"Address of the Antigravity core (e.g. localhost:5505)"`
	CorePort            string `flag:"core-port" env:"CORE_PORT" usage:"Local ls_core HTTP port when core-addr is not supplied"`
	CoreCSRF            string `flag:"core-csrf" env:"CORE_CSRF_TOKEN" usage:"CSRF token for internal core communication"`
	ExtensionServerAddr string `flag:"extension-server-addr" env:"EXTENSION_SERVER_ADDR" usage:"Address for the local ExtensionServerService"`
	ExtensionServerCSRF string `flag:"extension-server-csrf" env:"EXTENSION_SERVER_CSRF_TOKEN" usage:"CSRF token used by ls_core when calling ExtensionServerService"`
	CloudProxyAddr      string `flag:"cloud-proxy-addr" env:"ANTIGRAVITY_CLOUD_PROXY_ADDR" usage:"Address for the local Cloud Code stream tap proxy"`
	CloudCodeEndpoint   string `flag:"cloud-code-endpoint" env:"ANTIGRAVITY_CLOUD_CODE_ENDPOINT" usage:"Upstream Cloud Code endpoint for Antigravity"`
	OpenAIKey           string `flag:"openai-api-key" env:"OPENAI_API_KEY" usage:"API key for OpenAI provider"`
	AnthropicKey        string `flag:"anthropic-api-key" env:"ANTHROPIC_API_KEY" usage:"API key for Anthropic provider"`
	GeminiKey           string `flag:"gemini-api-key" env:"GOOGLE_API_KEY" usage:"API key for Gemini provider"`
}

func newServeCommand() *cobra.Command {
	opts := &serveOptions{}
	binder := flagsbinder.NewViperCobraFlagsBinder().
		String("db-path", os.ExpandEnv("$HOME/.antigravity-server/data/User/globalStorage/state.vscdb"), "Path to state.vscdb").
		String("server-path", "/opt/antigravity/server/index.js", "Path to antigravity-server index.js").
		String("core-path", "/opt/antigravity/bin/ls_core", "Path to ls_core binary").
		String("install-dir", "/opt/antigravity", "Installation directory").
		String("proxy-addr", ":8080", "Address for the proxy server to listen on").
		String("core-addr", "", "Address of the Antigravity core (e.g. localhost:5505)").
		String("core-port", "5505", "Local ls_core HTTP port when core-addr is not supplied").
		String("core-csrf", "proxy-secret-token", "CSRF token for internal core communication").
		String("extension-server-addr", "127.0.0.1:5530", "Address for the local ExtensionServerService").
		String("extension-server-csrf", "proxy-extension-token", "CSRF token used by ls_core when calling ExtensionServerService").
		String("cloud-proxy-addr", "127.0.0.1:5599", "Address for the local Cloud Code stream tap proxy").
		String("cloud-code-endpoint", "https://daily-cloudcode-pa.googleapis.com", "Upstream Cloud Code endpoint for Antigravity").
		String("openai-api-key", "", "API key for OpenAI provider").
		String("anthropic-api-key", "", "API key for Anthropic provider").
		String("gemini-api-key", "", "API key for Gemini provider")

	cmd := &cobra.Command{
		Use:           "serve",
		Short:         "Start the proxy server and backend processes",
		SilenceUsage:  true,
		SilenceErrors: true,
		PreRunE: func(cmd *cobra.Command, args []string) error {
			if err := binder.BindCommand(cmd, opts, args...); err != nil {
				return usageError(cmd, err)
			}
			// Fallback to random keys if not provided
			if opts.OpenAIKey == "" {
				opts.OpenAIKey = utils.GenerateOpenAIKey()
			}
			if opts.AnthropicKey == "" {
				opts.AnthropicKey = utils.GenerateAnthropicKey()
			}
			if opts.GeminiKey == "" {
				opts.GeminiKey = utils.GenerateGeminiKey()
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			return runServe(cmd.Context(), opts)
		},
	}
	binder.SetTo(cmd.Flags())
	return cmd
}

func runServe(ctx context.Context, opts *serveOptions) error {
	fmt.Println("--- Antigravity Proxy Starting ---")
	fmt.Printf("OpenAI API Key:    %s\n", opts.OpenAIKey)
	fmt.Printf("Anthropic API Key: %s\n", opts.AnthropicKey)
	fmt.Printf("Gemini API Key:    %s\n", opts.GeminiKey)
	fmt.Println("----------------------------------")

	pm := proc.NewProcessManager(opts.ServerPath, opts.CorePath, opts.InstallDir)
	sc := scraper.NewSQLiteScraper(opts.DBPath)

	bridgeAddr := opts.CoreAddr
	if bridgeAddr == "" {
		bridgeAddr = "http://127.0.0.1:" + portFromAddr(opts.CorePort, "5505")
	}
	// Add http:// prefix if missing
	if !strings.HasPrefix(bridgeAddr, "http") {
		bridgeAddr = "http://" + bridgeAddr
	}

	br := bridge.NewEngineBridge(bridgeAddr, sc)
	br.SetCoreCSRF(opts.CoreCSRF)

	// Start backend processes only if we are using the default local core
	if opts.CoreAddr == "" {
		streamTap, err := bridge.NewCloudStreamProxy(opts.CloudProxyAddr, opts.CloudCodeEndpoint)
		if err != nil {
			return fmt.Errorf("failed to configure Cloud Code stream proxy: %w", err)
		}
		if err := streamTap.Start(); err != nil {
			return fmt.Errorf("failed to start Cloud Code stream proxy: %w", err)
		}
		defer func() {
			stopCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			_ = streamTap.Close(stopCtx)
		}()
		br.SetStreamTap(streamTap)
		fmt.Printf("Cloud Code stream proxy listening on %s -> %s\n", streamTap.EndpointURL(), opts.CloudCodeEndpoint)

		extSrv := extserver.New(opts.ExtensionServerAddr, opts.DBPath)
		if err := extSrv.Start(); err != nil {
			return fmt.Errorf("failed to start ExtensionServerService: %w", err)
		}
		defer func() {
			stopCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			_ = extSrv.Stop(stopCtx)
		}()

		pm.ConfigureRuntime(proc.RuntimeOptions{
			CorePort:      portFromAddr(opts.CorePort, "5505"),
			CoreCSRF:      opts.CoreCSRF,
			ExtServerPort: portFromAddr(extSrv.Addr(), "5530"),
			ExtServerCSRF: opts.ExtensionServerCSRF,
			CloudEndpoint: streamTap.EndpointURL(),
			Auth:          sc,
		})
		if err := pm.Start(); err != nil {
			return fmt.Errorf("failed to start backend processes: %w", err)
		}
		defer pm.Stop()
	}

	// Verify protocol and promote to binary gRPC if compatible.
	verifyCtx, verifyCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer verifyCancel()
	if err := verifyProtocolWithRetry(verifyCtx, br); err != nil {
		fmt.Printf("⚠️  Protocol verification failed: %v. Falling back to safe JSON mode.\n", err)
	}

	// Initialize API server
	keys := api.APIKeys{
		OpenAI:    opts.OpenAIKey,
		Anthropic: opts.AnthropicKey,
		Gemini:    opts.GeminiKey,
	}
	srv := api.NewServer(br, keys, version)

	// Setup signal handling for graceful shutdown
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Run API server in a goroutine
	go func() {
		fmt.Printf("Proxy server listening on %s\n", opts.ProxyAddr)
		if err := srv.Run(opts.ProxyAddr); err != nil {
			fmt.Printf("Server stopped: %v\n", err)
		}
	}()

	<-ctx.Done()
	fmt.Println("Shutting down...")
	return nil
}

type protocolVerifier interface {
	VerifyProtocol(context.Context) error
}

func verifyProtocolWithRetry(ctx context.Context, verifier protocolVerifier) error {
	var lastErr error
	for {
		if err := verifier.VerifyProtocol(ctx); err != nil {
			lastErr = err
		} else {
			return nil
		}

		timer := time.NewTimer(250 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			if lastErr != nil {
				return lastErr
			}
			return ctx.Err()
		case <-timer.C:
		}
	}
}

func portFromAddr(addr, fallback string) string {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return fallback
	}
	if strings.HasPrefix(addr, "http://") || strings.HasPrefix(addr, "https://") {
		withoutScheme := strings.TrimPrefix(strings.TrimPrefix(addr, "http://"), "https://")
		if hostPort := strings.Split(withoutScheme, "/")[0]; hostPort != "" {
			addr = hostPort
		}
	}
	if _, port, err := net.SplitHostPort(addr); err == nil && port != "" {
		return port
	}
	if !strings.Contains(addr, ":") {
		return addr
	}
	if idx := strings.LastIndex(addr, ":"); idx >= 0 && idx+1 < len(addr) {
		return addr[idx+1:]
	}
	return fallback
}
