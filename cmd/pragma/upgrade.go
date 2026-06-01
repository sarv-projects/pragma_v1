package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const (
	releaseAPI = "https://api.github.com/repos/sarv-projects/pragma/releases/latest"
)

type githubRelease struct {
	TagName string         `json:"tag_name"`
	Assets  []githubAsset  `json:"assets"`
	HTMLURL string         `json:"html_url"`
}

type githubAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

func runUpgrade() int {
	fmt.Printf("Pragma %s (%s/%s)\n", pragmaCurrentVersion, runtime.GOOS, runtime.GOARCH)
	fmt.Println("Checking for updates...")

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Get(releaseAPI)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: Failed to check for updates: %v\n", err)
		return 1
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		fmt.Fprintf(os.Stderr, "Error: GitHub API returned status %d\n", resp.StatusCode)
		return 1
	}

	var release githubRelease
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		fmt.Fprintf(os.Stderr, "Error: Failed to parse release info: %v\n", err)
		return 1
	}

	latestVersion := strings.TrimPrefix(release.TagName, "v")
	if latestVersion == pragmaCurrentVersion {
		fmt.Printf("Already up to date (v%s).\n", pragmaCurrentVersion)
		return 0
	}

	if pragmaCurrentVersion == "dev" {
		fmt.Printf("Latest release: v%s\n", latestVersion)
		fmt.Println("You are running a development build. Install a release build to use upgrade.")
		return 0
	}

	fmt.Printf("New version available: v%s (current: v%s)\n", latestVersion, pragmaCurrentVersion)

	// Find the correct asset for this platform
	suffix := platformSuffix()
	var downloadURL string
	for _, asset := range release.Assets {
		if strings.Contains(asset.Name, suffix) && !strings.HasSuffix(asset.Name, ".sha256") {
			downloadURL = asset.BrowserDownloadURL
			break
		}
	}

	if downloadURL == "" {
		fmt.Fprintf(os.Stderr, "Error: No binary found for %s/%s in the release.\n", runtime.GOOS, runtime.GOARCH)
		fmt.Fprintf(os.Stderr, "Visit %s to download manually.\n", release.HTMLURL)
		return 1
	}

	// Find the corresponding .sha256 checksum asset
	var checksumURL string
	for _, asset := range release.Assets {
		if asset.Name == findBinaryAssetName(release.Assets, suffix)+".sha256" {
			checksumURL = asset.BrowserDownloadURL
			break
		}
	}

	fmt.Printf("Downloading %s...\n", downloadURL)

	dlResp, err := client.Get(downloadURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: Failed to download: %v\n", err)
		return 1
	}
	defer dlResp.Body.Close()

	if dlResp.StatusCode != http.StatusOK {
		fmt.Fprintf(os.Stderr, "Error: Download returned status %d\n", dlResp.StatusCode)
		return 1
	}

	// Read the binary into memory for checksum verification
	binaryData, err := io.ReadAll(dlResp.Body)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: Download failed: %v\n", err)
		return 1
	}

	// Verify SHA256 checksum if available
	if checksumURL != "" {
		fmt.Println("Verifying checksum...")
		csResp, err := client.Get(checksumURL)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: Failed to download checksum: %v\n", err)
			return 1
		}
		defer csResp.Body.Close()

		if csResp.StatusCode == http.StatusOK {
			csData, err := io.ReadAll(csResp.Body)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error: Failed to read checksum file: %v\n", err)
				return 1
			}
			// Checksum file format: "<hex>  <filename>" or just "<hex>"
			fields := strings.Fields(strings.TrimSpace(string(csData)))
			if len(fields) == 0 {
				fmt.Fprintf(os.Stderr, "Warning: Checksum file is empty, skipping verification\n")
			} else {
				expectedHash := fields[0]
				actualHash := sha256Hash(binaryData)
				if !strings.EqualFold(expectedHash, actualHash) {
					fmt.Fprintf(os.Stderr, "Error: Checksum verification failed.\n")
					fmt.Fprintf(os.Stderr, "  Expected: %s\n", expectedHash)
					fmt.Fprintf(os.Stderr, "  Got:      %s\n", actualHash)
					return 1
				}
				fmt.Println("Checksum verified OK.")
			}
		}
	}

	// Write to a temp file next to the current executable
	execPath, err := os.Executable()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: Cannot determine executable path: %v\n", err)
		return 1
	}

	tmpPath := execPath + ".new"
	if err := os.WriteFile(tmpPath, binaryData, 0644); err != nil {
		fmt.Fprintf(os.Stderr, "Error: Cannot write temp file: %v\n", err)
		return 1
	}

	// Make executable (no-op on Windows)
	if runtime.GOOS != "windows" {
		if err := os.Chmod(tmpPath, 0755); err != nil {
			os.Remove(tmpPath)
			fmt.Fprintf(os.Stderr, "Error: Cannot set permissions: %v\n", err)
			return 1
		}
	}

	// Replace current executable
	oldPath := execPath + ".old"
	os.Remove(oldPath) // clean up any previous .old file

	if err := os.Rename(execPath, oldPath); err != nil {
		os.Remove(tmpPath)
		fmt.Fprintf(os.Stderr, "Error: Cannot replace executable: %v\n", err)
		return 1
	}

	if err := os.Rename(tmpPath, execPath); err != nil {
		// Try to restore
		_ = os.Rename(oldPath, execPath)
		fmt.Fprintf(os.Stderr, "Error: Cannot install new binary: %v\n", err)
		return 1
	}

	os.Remove(oldPath)
	fmt.Printf("Successfully upgraded binary to v%s!\n", latestVersion)

	// Also upgrade the Python daemon if the venv exists.
	upgradeDaemon(latestVersion)

	return 0
}

// upgradeDaemon upgrades the Python daemon in ~/.pragma/venv after a binary upgrade.
func upgradeDaemon(newVersion string) {
	home, err := os.UserHomeDir()
	if err != nil {
		return
	}

	var venvPython string
	if runtime.GOOS == "windows" {
		venvPython = filepath.Join(home, ".pragma", "venv", "Scripts", "python.exe")
	} else {
		venvPython = filepath.Join(home, ".pragma", "venv", "bin", "python")
	}

	if _, err := os.Stat(venvPython); err != nil {
		fmt.Println("Note: No ~/.pragma/venv found — skipping daemon upgrade.")
		fmt.Println("Run 'pragma setup' to install the Python daemon.")
		return
	}

	fmt.Print("Upgrading Python daemon... ")

	// Use tagged release for versioned upgrades
	// Note: setup.go uses main branch for initial install. If you encounter
	// compatibility issues after upgrade, run: pragma setup
	pkgURL := fmt.Sprintf(
		"https://github.com/sarv-projects/pragma/archive/refs/tags/v%s.tar.gz#subdirectory=daemon",
		newVersion,
	)

	var cmd *exec.Cmd
	if uvPath, err := exec.LookPath("uv"); err == nil {
		cmd = exec.Command(uvPath, "pip", "install", "--python", venvPython, pkgURL)
	} else {
		var pipPath string
		if runtime.GOOS == "windows" {
			pipPath = filepath.Join(home, ".pragma", "venv", "Scripts", "pip.exe")
		} else {
			pipPath = filepath.Join(home, ".pragma", "venv", "bin", "pip")
		}
		cmd = exec.Command(pipPath, "install", pkgURL)
	}

	if out, err := cmd.CombinedOutput(); err != nil {
		fmt.Printf("\u26a0\ufe0f  Could not upgrade daemon: %v\n", err)
		fmt.Printf("   %s\n", strings.TrimSpace(string(out)))
		fmt.Println("   Manually upgrade: pip install -e ./daemon")
		return
	}
	fmt.Println("\u2705")
}

func platformSuffix() string {
	os := runtime.GOOS
	arch := runtime.GOARCH
	return fmt.Sprintf("%s-%s", os, arch)
}

func sha256Hash(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

func findBinaryAssetName(assets []githubAsset, suffix string) string {
	for _, asset := range assets {
		if strings.Contains(asset.Name, suffix) && !strings.HasSuffix(asset.Name, ".sha256") {
			return asset.Name
		}
	}
	return ""
}
