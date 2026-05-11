package bridge

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

// URL Patterns for Antigravity releases
const (
	baseURL = "https://storage.googleapis.com/antigravity-releases/latest"
)

func (b *engineBridge) UpdateBackend(ctx context.Context) error {
	var downloadURL string
	var targetFile string

	osName := runtime.GOOS
	archName := runtime.GOARCH

	agArch := archName
	if agArch == "amd64" {
		agArch = "x64"
	}

	if osName == "linux" {
		if archName == "amd64" {
			downloadURL = baseURL + "/antigravity_amd64.deb"
		} else {
			downloadURL = baseURL + "/antigravity_arm64.deb"
		}
		targetFile = "antigravity.deb"
	} else if osName == "windows" {
		if archName == "amd64" {
			downloadURL = baseURL + "/antigravity-setup-x64.exe"
		} else {
			downloadURL = baseURL + "/antigravity-setup-arm64.exe"
		}
		targetFile = "antigravity-setup.exe"
	} else if osName == "darwin" {
		// macOS: Antigravity-arm64.dmg or Antigravity-x64.dmg
		downloadURL = fmt.Sprintf("%s/Antigravity-%s.dmg", baseURL, agArch)
		targetFile = "antigravity.dmg"
	} else {
		return fmt.Errorf("unsupported OS for auto-update: %s", osName)
	}

	fmt.Printf("🚀 Starting Antigravity backend update for %s/%s...\n", osName, archName)
	fmt.Printf("📥 Downloading latest release from %s...\n", downloadURL)

	err := downloadFile(ctx, downloadURL, targetFile)
	if err != nil {
		return fmt.Errorf("failed to download: %w", err)
	}
	defer os.Remove(targetFile)

	fmt.Println("📦 Extracting server bundle...")
	err = extractBundle(targetFile)
	if err != nil {
		return fmt.Errorf("failed to extract: %w", err)
	}

	fmt.Println("✅ Antigravity backend updated successfully!")
	return nil
}

func downloadFile(ctx context.Context, url, path string) error {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return err
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("bad status: %s", resp.Status)
	}

	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	_, err = io.Copy(f, resp.Body)
	return err
}

func extractBundle(path string) error {
	if strings.HasSuffix(path, ".deb") {
		cmd := exec.Command("bash", "-c", fmt.Sprintf("ar x %s data.tar.xz && tar xf data.tar.xz --strip-components=2 ./opt/antigravity && rm data.tar.xz", path))
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		return cmd.Run()
	} else if strings.HasSuffix(path, ".tar.gz") {
		cmd := exec.Command("tar", "xf", path, "--strip-components=1")
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		return cmd.Run()
	} else if strings.HasSuffix(path, ".exe") {
		cmd := exec.Command("7z", "x", "-oantigravity-extracted", path)
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("extraction failed (is 7-Zip installed?): %w", err)
		}
		fmt.Println("⚠️  Windows extraction complete.")
		return nil
	} else if strings.HasSuffix(path, ".dmg") {
		// macOS extraction: mount dmg, copy app, unmount
		mountCmd := exec.Command("hdiutil", "attach", path, "-nobrowse", "-readonly")
		output, err := mountCmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("failed to mount dmg: %w\nOutput: %s", err, string(output))
		}

		// Find mount point (usually /Volumes/Antigravity)
		// We'll search for it in the output or /Volumes
		mountPoint := "/Volumes/Antigravity"
		// (In real world, we should parse the output of hdiutil attach)

		fmt.Printf("📂 Mounted DMG at %s. Extracting...\n", mountPoint)
		copyCmd := exec.Command("cp", "-R", mountPoint+"/Antigravity.app", "./")
		if err := copyCmd.Run(); err != nil {
			exec.Command("hdiutil", "detach", mountPoint).Run()
			return fmt.Errorf("failed to copy app from dmg: %w", err)
		}

		exec.Command("hdiutil", "detach", mountPoint).Run()
		fmt.Println("✅ macOS extraction complete.")
		return nil
	}
	return fmt.Errorf("unknown package format: %s", path)
}
