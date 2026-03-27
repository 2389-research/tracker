// ABOUTME: Self-update command — downloads latest release from GitHub and replaces the binary.
// ABOUTME: Detects install method (Homebrew/go install/binary) and acts accordingly.
package main

import (
	"archive/tar"
	"compress/gzip"
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
)

// githubRelease represents the relevant fields from GitHub's release API.
type githubRelease struct {
	TagName string        `json:"tag_name"`
	Assets  []githubAsset `json:"assets"`
}

type githubAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

func executeUpdate(checkOnly bool) error {
	current := version

	fmt.Printf("Current version: %s\n", current)
	fmt.Println("Checking for updates...")

	release, err := fetchLatestRelease()
	if err != nil {
		return fmt.Errorf("check for updates: %w", err)
	}

	latest := release.TagName
	if latest == current || latest == "v"+current {
		fmt.Printf("Already up to date: %s\n", current)
		return nil
	}

	fmt.Printf("Update available: %s → %s\n", current, latest)

	if checkOnly {
		fmt.Println("Run `tracker update` to install.")
		return nil
	}

	method := detectInstallMethod()
	switch method {
	case "homebrew":
		fmt.Println("Detected Homebrew installation.")
		fmt.Println("Run: brew upgrade tracker")
		return nil
	case "go-install":
		fmt.Printf("Run: go install github.com/2389-research/tracker/cmd/tracker@%s\n", latest)
		fmt.Println("Or use --force to self-replace the binary directly.")
		return nil
	}

	return selfReplace(release)
}

// fetchLatestRelease queries the GitHub API for the latest release.
func fetchLatestRelease() (*githubRelease, error) {
	resp, err := http.Get("https://api.github.com/repos/2389-research/tracker/releases/latest")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("GitHub API returned %d", resp.StatusCode)
	}

	var release githubRelease
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return nil, fmt.Errorf("parse release: %w", err)
	}
	return &release, nil
}

// detectInstallMethod determines how tracker was installed.
func detectInstallMethod() string {
	exe, err := os.Executable()
	if err != nil {
		return "binary"
	}
	resolved, err := filepath.EvalSymlinks(exe)
	if err != nil {
		resolved = exe
	}

	// Homebrew detection
	if strings.Contains(resolved, "/Cellar/") || strings.Contains(resolved, "/homebrew/") {
		return "homebrew"
	}

	// go install detection
	if gobin := os.Getenv("GOBIN"); gobin != "" && strings.HasPrefix(resolved, gobin) {
		return "go-install"
	}
	if gopath := os.Getenv("GOPATH"); gopath != "" && strings.Contains(resolved, filepath.Join(gopath, "bin")) {
		return "go-install"
	}
	// Default GOPATH
	home, _ := os.UserHomeDir()
	if home != "" && strings.HasPrefix(resolved, filepath.Join(home, "go", "bin")) {
		return "go-install"
	}

	return "binary"
}

// selfReplace downloads and replaces the current binary.
func selfReplace(release *githubRelease) error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("find current binary: %w", err)
	}
	exe, err = filepath.EvalSymlinks(exe)
	if err != nil {
		return fmt.Errorf("resolve binary path: %w", err)
	}

	dir := filepath.Dir(exe)
	if err := checkWritePermission(dir); err != nil {
		return fmt.Errorf("no write permission to %s: %w\nTry: sudo tracker update", dir, err)
	}

	tmpBin, err := downloadAndPrepare(release, dir)
	if err != nil {
		return err
	}
	defer os.Remove(tmpBin)

	return atomicSwap(exe, tmpBin, release.TagName)
}

// downloadAndPrepare downloads, verifies, extracts, and tests the new binary.
func downloadAndPrepare(release *githubRelease, dir string) (string, error) {
	assetName, assetURL, checksumsURL := findAsset(release)
	if assetURL == "" {
		return "", fmt.Errorf("no release asset for %s/%s (looking for %s)", runtime.GOOS, runtime.GOARCH, assetName)
	}

	fmt.Printf("Downloading %s...\n", assetName)
	tmpTar, err := downloadToTemp(dir, assetURL)
	if err != nil {
		return "", fmt.Errorf("download: %w", err)
	}
	defer os.Remove(tmpTar)

	if checksumsURL != "" {
		fmt.Print("Verifying checksum... ")
		if err := verifyChecksum(tmpTar, assetName, checksumsURL); err != nil {
			fmt.Println("FAILED")
			return "", fmt.Errorf("checksum: %w", err)
		}
		fmt.Println("OK")
	}

	tmpBin, err := extractBinaryFromTar(tmpTar, dir)
	if err != nil {
		return "", fmt.Errorf("extract: %w", err)
	}

	if err := os.Chmod(tmpBin, 0o755); err != nil {
		os.Remove(tmpBin)
		return "", fmt.Errorf("chmod: %w", err)
	}

	fmt.Print("Testing new binary... ")
	if output, err := exec.Command(tmpBin, "version").CombinedOutput(); err != nil {
		fmt.Println("FAILED")
		os.Remove(tmpBin)
		return "", fmt.Errorf("test failed: %w\n%s", err, output)
	}
	fmt.Println("OK")

	return tmpBin, nil
}

// findAsset locates the correct release asset for this OS/arch.
func findAsset(release *githubRelease) (name, url, checksumsURL string) {
	name = fmt.Sprintf("tracker_%s_%s_%s.tar.gz",
		strings.TrimPrefix(release.TagName, "v"),
		runtime.GOOS, runtime.GOARCH)

	for _, a := range release.Assets {
		if a.Name == name {
			url = a.BrowserDownloadURL
		}
		if a.Name == "checksums.txt" {
			checksumsURL = a.BrowserDownloadURL
		}
	}
	return
}

// atomicSwap replaces the current binary with the new one, keeping a .bak.
func atomicSwap(exe, tmpBin, tagName string) error {
	bakPath := exe + ".bak"
	os.Remove(bakPath)

	if err := os.Rename(exe, bakPath); err != nil {
		return fmt.Errorf("backup current binary: %w", err)
	}

	if err := os.Rename(tmpBin, exe); err != nil {
		os.Rename(bakPath, exe) // rollback
		return fmt.Errorf("install new binary: %w", err)
	}

	fmt.Printf("Updated to %s\n", tagName)
	fmt.Printf("Previous version backed up to %s\n", bakPath)
	return nil
}

// checkWritePermission verifies the process can write to the directory.
func checkWritePermission(dir string) error {
	tmp := filepath.Join(dir, ".tracker-update-test")
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	f.Close()
	os.Remove(tmp)
	return nil
}

// downloadToTemp downloads a URL to a temp file in the given directory.
func downloadToTemp(dir, url string) (string, error) {
	resp, err := http.Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	f, err := os.CreateTemp(dir, "tracker-update-*.tar.gz")
	if err != nil {
		return "", err
	}
	defer f.Close()

	if _, err := io.Copy(f, resp.Body); err != nil {
		os.Remove(f.Name())
		return "", err
	}
	return f.Name(), nil
}

// verifyChecksum downloads checksums.txt and verifies the file's SHA256.
func verifyChecksum(filePath, assetName, checksumsURL string) error {
	resp, err := http.Get(checksumsURL)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	// Parse checksums.txt: each line is "hash  filename"
	var expectedHash string
	for _, line := range strings.Split(string(body), "\n") {
		parts := strings.Fields(line)
		if len(parts) == 2 && parts[1] == assetName {
			expectedHash = parts[0]
			break
		}
	}
	if expectedHash == "" {
		return fmt.Errorf("no checksum found for %s", assetName)
	}

	// Compute actual hash
	f, err := os.Open(filePath)
	if err != nil {
		return err
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return err
	}
	actualHash := hex.EncodeToString(h.Sum(nil))

	if actualHash != expectedHash {
		return fmt.Errorf("checksum mismatch: expected %s, got %s", expectedHash, actualHash)
	}
	return nil
}

// extractBinaryFromTar extracts the "tracker" binary from a .tar.gz file.
func extractBinaryFromTar(tarPath, destDir string) (string, error) {
	f, err := os.Open(tarPath)
	if err != nil {
		return "", err
	}
	defer f.Close()

	gz, err := gzip.NewReader(f)
	if err != nil {
		return "", err
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", err
		}

		// Look for the tracker binary (may be at root or in a subdirectory)
		base := filepath.Base(hdr.Name)
		if base == "tracker" && hdr.Typeflag == tar.TypeReg {
			tmpBin := filepath.Join(destDir, ".tracker-new")
			out, err := os.Create(tmpBin)
			if err != nil {
				return "", err
			}
			if _, err := io.Copy(out, tr); err != nil {
				out.Close()
				os.Remove(tmpBin)
				return "", err
			}
			out.Close()
			return tmpBin, nil
		}
	}

	return "", fmt.Errorf("tracker binary not found in archive")
}
