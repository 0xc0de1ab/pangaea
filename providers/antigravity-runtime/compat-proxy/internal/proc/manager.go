package proc

import (
	"bufio"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/google/antigravity-compat-proxy/internal/extserver"
	"github.com/google/antigravity-compat-proxy/internal/interfaces"
)

const (
	PackagesURL = "https://us-central1-apt.pkg.dev/projects/antigravity-auto-updater-dev/dists/antigravity-debian/main/binary-amd64/Packages"
	BaseRepoURL = "https://us-central1-apt.pkg.dev/projects/antigravity-auto-updater-dev"
)

type ProcessManager struct {
	serverPath     string
	corePath       string
	installDir     string
	corePort       string
	coreCSRF       string
	extServerPort  string
	extServerCSRF  string
	cloudEndpoint  string
	auth           interfaces.TokenReader
	currentVersion string
	serverCmd      *exec.Cmd
	coreCmd        *exec.Cmd
	mu             sync.Mutex
}

func NewProcessManager(serverPath, corePath, installDir string) *ProcessManager {
	return &ProcessManager{
		serverPath:    serverPath,
		corePath:      corePath,
		installDir:    installDir,
		corePort:      "5505",
		coreCSRF:      "proxy-secret-token",
		extServerPort: "5530",
		extServerCSRF: "proxy-extension-token",
		cloudEndpoint: "https://daily-cloudcode-pa.googleapis.com",
	}
}

type RuntimeOptions struct {
	CorePort      string
	CoreCSRF      string
	ExtServerPort string
	ExtServerCSRF string
	CloudEndpoint string
	Auth          interfaces.TokenReader
}

func (pm *ProcessManager) ConfigureRuntime(opts RuntimeOptions) {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	if strings.TrimSpace(opts.CorePort) != "" {
		pm.corePort = strings.TrimSpace(opts.CorePort)
	}
	if strings.TrimSpace(opts.CoreCSRF) != "" {
		pm.coreCSRF = strings.TrimSpace(opts.CoreCSRF)
	}
	if strings.TrimSpace(opts.ExtServerPort) != "" {
		pm.extServerPort = strings.TrimSpace(opts.ExtServerPort)
	}
	if strings.TrimSpace(opts.ExtServerCSRF) != "" {
		pm.extServerCSRF = strings.TrimSpace(opts.ExtServerCSRF)
	}
	if strings.TrimSpace(opts.CloudEndpoint) != "" {
		pm.cloudEndpoint = strings.TrimSpace(opts.CloudEndpoint)
	}
	if opts.Auth != nil {
		pm.auth = opts.Auth
	}
}

func (pm *ProcessManager) Start() error {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	if pm.serverCmd != nil && pm.serverCmd.Process != nil {
		// Check if it's actually running
		if err := pm.serverCmd.Process.Signal(syscall.Signal(0)); err == nil {
			return nil // Already running
		}
	}

	// Start antigravity-server (Node.js)
	// Assuming serverPath is the absolute path to the node script
	pm.serverCmd = exec.Command("node", pm.serverPath)
	pm.serverCmd.Stdout = os.Stdout
	pm.serverCmd.Stderr = os.Stderr
	// Set environment variables if needed
	pm.serverCmd.Env = append(os.Environ(), "NODE_ENV=production")

	if err := pm.serverCmd.Start(); err != nil {
		return fmt.Errorf("failed to start antigravity-server: %w", err)
	}

	geminiDir := strings.TrimSpace(os.Getenv("ANTIGRAVITY_GEMINI_DIR"))
	if geminiDir == "" {
		geminiDir = "/var/lib/antigravity/home/.antigravity-server"
	}
	appDataDir := strings.TrimSpace(os.Getenv("ANTIGRAVITY_APP_DATA_DIR"))
	if appDataDir == "" {
		appDataDir = "data"
	}

	args := []string{
		"--enable_lsp",
		"--http_server_port", pm.corePort,
		"--csrf_token", pm.coreCSRF,
		"--extension_server_port", pm.extServerPort,
		"--extension_server_csrf_token", pm.extServerCSRF,
		"--cloud_code_endpoint", pm.cloudEndpoint,
		"--gemini_dir", geminiDir,
		"--app_data_dir", appDataDir,
	}
	pm.coreCmd = exec.Command(pm.corePath, args...)
	pm.coreCmd.Stdout = os.Stdout
	pm.coreCmd.Stderr = os.Stderr

	token, err := pm.waitForAuthToken(10*time.Second, 250*time.Millisecond)
	if err != nil {
		pm.serverCmd.Process.Kill()
		return fmt.Errorf("failed to load Antigravity token for ls_core metadata: %w", err)
	}
	stdin, err := pm.coreCmd.StdinPipe()
	if err != nil {
		pm.serverCmd.Process.Kill()
		return fmt.Errorf("failed to open ls_core stdin: %w", err)
	}
	var metadataStdin io.WriteCloser = stdin
	metadataPayload := extserver.EncodeCodeiumMetadataForProcess(token)

	if err := pm.coreCmd.Start(); err != nil {
		// Cleanup server if core fails
		pm.serverCmd.Process.Kill()
		return fmt.Errorf("failed to start ls_core: %w", err)
	}
	if metadataStdin != nil {
		if _, err := metadataStdin.Write(metadataPayload); err != nil {
			fmt.Printf("Warning: failed to write ls_core metadata: %v\n", err)
		}
		_ = metadataStdin.Close()
	}

	fmt.Printf("Processes started: antigravity-server (pid %d), ls_core (pid %d, extension server port %s)\n", pm.serverCmd.Process.Pid, pm.coreCmd.Process.Pid, pm.extServerPort)
	return nil
}

func (pm *ProcessManager) waitForAuthToken(timeout time.Duration, interval time.Duration) (string, error) {
	if pm.auth == nil {
		return "", fmt.Errorf("auth provider is not configured")
	}
	deadline := time.Now().Add(timeout)
	var lastErr error
	for {
		token, err := pm.auth.GetLatestToken()
		if err == nil && strings.TrimSpace(token) != "" {
			return strings.TrimSpace(token), nil
		}
		if err != nil {
			lastErr = err
		} else {
			lastErr = fmt.Errorf("empty token")
		}
		if time.Now().After(deadline) {
			return "", lastErr
		}
		time.Sleep(interval)
	}
}

func (pm *ProcessManager) Stop() error {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	if pm.coreCmd != nil && pm.coreCmd.Process != nil {
		fmt.Printf("Stopping ls_core (pid %d)...\n", pm.coreCmd.Process.Pid)
		if err := pm.coreCmd.Process.Signal(syscall.SIGTERM); err != nil {
			pm.coreCmd.Process.Kill()
		}
		// Wait with timeout
		done := make(chan error, 1)
		go func() {
			done <- pm.coreCmd.Wait()
		}()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			pm.coreCmd.Process.Kill()
		}
		pm.coreCmd = nil
	}

	if pm.serverCmd != nil && pm.serverCmd.Process != nil {
		fmt.Printf("Stopping antigravity-server (pid %d)...\n", pm.serverCmd.Process.Pid)
		if err := pm.serverCmd.Process.Signal(syscall.SIGTERM); err != nil {
			pm.serverCmd.Process.Kill()
		}
		done := make(chan error, 1)
		go func() {
			done <- pm.serverCmd.Wait()
		}()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			pm.serverCmd.Process.Kill()
		}
		pm.serverCmd = nil
	}

	return nil
}

func (pm *ProcessManager) Restart() error {
	if err := pm.Stop(); err != nil {
		return err
	}
	return pm.Start()
}

func (pm *ProcessManager) IsHealthy() bool {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	if pm.serverCmd == nil || pm.serverCmd.Process == nil {
		return false
	}
	if pm.coreCmd == nil || pm.coreCmd.Process == nil {
		return false
	}

	if err := pm.serverCmd.Process.Signal(syscall.Signal(0)); err != nil {
		return false
	}
	if err := pm.coreCmd.Process.Signal(syscall.Signal(0)); err != nil {
		return false
	}

	return true
}

// CheckAndPerformUpdate checks for updates and performs them if available.
func (pm *ProcessManager) CheckAndPerformUpdate() (bool, error) {
	fmt.Println("Checking for updates...")
	resp, err := http.Get(PackagesURL)
	if err != nil {
		return false, fmt.Errorf("failed to fetch Packages index: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return false, fmt.Errorf("bad status from Packages index: %d", resp.StatusCode)
	}

	var latestVersion, filename string
	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "Version: ") {
			latestVersion = strings.TrimPrefix(line, "Version: ")
		} else if strings.HasPrefix(line, "Filename: ") {
			filename = strings.TrimPrefix(line, "Filename: ")
		}
		// Packages file can have multiple packages, but we assume the first one is ours
		// or they are separated by empty lines.
		if latestVersion != "" && filename != "" {
			break
		}
	}

	if latestVersion == "" || filename == "" {
		return false, fmt.Errorf("failed to parse version or filename from Packages index")
	}

	if latestVersion == pm.currentVersion {
		fmt.Printf("Already at latest version: %s\n", latestVersion)
		return false, nil
	}

	fmt.Printf("New version found: %s (current: %s). Updating...\n", latestVersion, pm.currentVersion)

	debURL := fmt.Sprintf("%s/%s", BaseRepoURL, filename)
	if err := pm.performUpdate(debURL, latestVersion); err != nil {
		return false, fmt.Errorf("update failed: %w", err)
	}

	pm.currentVersion = latestVersion
	return true, nil
}

func (pm *ProcessManager) performUpdate(debURL, version string) error {
	// 1. Download .deb
	tmpFile, err := os.CreateTemp("", "antigravity_*.deb")
	if err != nil {
		return err
	}
	defer os.Remove(tmpFile.Name())
	defer tmpFile.Close()

	fmt.Printf("Downloading update from %s...\n", debURL)
	resp, err := http.Get(debURL)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if _, err := io.Copy(tmpFile, resp.Body); err != nil {
		return err
	}

	// 2. Extract .deb
	extractDir := filepath.Join(pm.installDir, version)
	if err := os.MkdirAll(extractDir, 0755); err != nil {
		return err
	}

	fmt.Printf("Extracting update to %s...\n", extractDir)
	// Using system 'ar' and 'tar' for simplicity as per design
	// ar x package.deb
	cmd := exec.Command("ar", "x", tmpFile.Name())
	cmd.Dir = extractDir
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to extract ar: %w", err)
	}

	// tar xf data.tar.xz (or data.tar.gz)
	var dataTar string
	if _, err := os.Stat(filepath.Join(extractDir, "data.tar.xz")); err == nil {
		dataTar = "data.tar.xz"
	} else if _, err := os.Stat(filepath.Join(extractDir, "data.tar.gz")); err == nil {
		dataTar = "data.tar.gz"
	} else {
		return fmt.Errorf("could not find data.tar.xz or data.tar.gz in .deb")
	}

	cmd = exec.Command("tar", "xf", dataTar)
	cmd.Dir = extractDir
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to extract tar: %w", err)
	}

	// 3. Update paths (assuming standard paths inside the deb)
	// Adjust these based on the actual structure of the extracted deb
	pm.serverPath = filepath.Join(extractDir, "opt/antigravity/server/index.js")
	pm.corePath = filepath.Join(extractDir, "opt/antigravity/bin/ls_core")

	// 4. Restart processes
	fmt.Println("Restarting processes with new version...")
	return pm.Restart()
}

var _ interfaces.LifecycleManager = (*ProcessManager)(nil)
