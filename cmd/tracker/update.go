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
	"time"
)

// Maximum sizes to prevent resource exhaustion from malicious/corrupted releases.
const (
	maxTarballSize   = 500 << 20 // 500 MB
	maxBinarySize    = 200 << 20 // 200 MB
	maxChecksumsSize = 1 << 20   // 1 MB
)

// HTTP clients for update operations. The API client has a short timeout
// (for metadata requests), the download client has a longer one (for binaries).
var (
	updateAPIClient = &http.Client{Timeout: 30 * time.Second}
	updateDLClient  = &http.Client{Timeout: 5 * time.Minute}
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

func executeUpdate() error {
	current := version
	if current == "dev" {
		return fmt.Errorf("cannot self-update a dev build; install a release version first")
	}

	fmt.Printf("Current version: %s\n", current)
	fmt.Println("Checking for updates...")

	release, err := fetchLatestRelease()
	if err != nil {
		return fmt.Errorf("check for updates: %w", err)
	}

	latest := release.TagName
	if versionsEqual(current, latest) {
		fmt.Printf("Already up to date: %s\n", current)
		return nil
	}

	fmt.Printf("Update available: %s → %s\n", current, latest)

	method := detectInstallMethod()
	switch method {
	case "homebrew":
		fmt.Println("Detected Homebrew installation.")
		fmt.Println("Run: brew upgrade tracker")
		return nil
	case "go-install":
		fmt.Printf("Run: go install github.com/2389-research/tracker/cmd/tracker@%s\n", latest)
		return nil
	case "unknown":
		return fmt.Errorf("could not determine binary location; download manually from GitHub releases")
	}

	return selfReplace(release)
}

// versionsEqual compares two version strings, ignoring the optional "v" prefix.
func versionsEqual(a, b string) bool {
	return strings.TrimPrefix(a, "v") == strings.TrimPrefix(b, "v")
}

// fetchLatestRelease queries the GitHub API for the latest release.
func fetchLatestRelease() (*githubRelease, error) {
	req, err := http.NewRequest("GET", "https://api.github.com/repos/2389-research/tracker/releases/latest", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "tracker/"+version)

	resp, err := updateAPIClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case 200:
		// ok
	case 403:
		return nil, fmt.Errorf("GitHub API rate limited (HTTP 403); try again later or set GITHUB_TOKEN")
	case 404:
		return nil, fmt.Errorf("GitHub release not found (HTTP 404); check network/proxy settings")
	default:
		return nil, fmt.Errorf("GitHub API returned HTTP %d", resp.StatusCode)
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
		return "unknown"
	}
	resolved, err := filepath.EvalSymlinks(exe)
	if err != nil {
		resolved = exe
	}
	return classifyInstallPath(resolved, os.Getenv("GOBIN"), os.Getenv("GOPATH"))
}

// classifyInstallPath determines install method from the resolved binary path.
func classifyInstallPath(resolved, gobin, gopath string) string {
	if isHomebrewPath(resolved) {
		return "homebrew"
	}
	if isGoInstallPath(resolved, gobin, gopath) {
		return "go-install"
	}
	return "binary"
}

// isHomebrewPath returns true if the binary path looks like a Homebrew install.
func isHomebrewPath(resolved string) bool {
	return strings.Contains(resolved, "/Cellar/") || strings.HasPrefix(resolved, "/opt/homebrew/")
}

// isGoInstallPath returns true if the binary path is under GOBIN, GOPATH/bin, or ~/go/bin.
func isGoInstallPath(resolved, gobin, gopath string) bool {
	if gobin != "" && strings.HasPrefix(resolved, gobin) {
		return true
	}
	if gopath != "" && strings.Contains(resolved, filepath.Join(gopath, "bin")) {
		return true
	}
	home, _ := os.UserHomeDir()
	return home != "" && strings.HasPrefix(resolved, filepath.Join(home, "go", "bin"))
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

	err = atomicSwap(exe, tmpBin, release.TagName)
	if err != nil {
		os.Remove(tmpBin) // clean up on swap failure
	}
	return err
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

	if err := verifyChecksumIfAvailable(tmpTar, assetName, checksumsURL); err != nil {
		return "", err
	}

	return extractAndTestBinary(tmpTar, dir)
}

// verifyChecksumIfAvailable verifies checksum when available, or warns if not.
func verifyChecksumIfAvailable(tmpTar, assetName, checksumsURL string) error {
	if checksumsURL == "" {
		fmt.Println("Warning: no checksums.txt in release — skipping integrity verification")
		return nil
	}
	fmt.Print("Verifying checksum... ")
	if err := verifyChecksum(tmpTar, assetName, checksumsURL); err != nil {
		fmt.Println("FAILED")
		return fmt.Errorf("checksum: %w", err)
	}
	fmt.Println("OK")
	return nil
}

// extractAndTestBinary extracts the binary from tar, sets permissions, and smoke-tests it.
func extractAndTestBinary(tmpTar, dir string) (string, error) {
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
	if err := os.Remove(bakPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove old backup %s: %w", bakPath, err)
	}

	if err := os.Rename(exe, bakPath); err != nil {
		return fmt.Errorf("backup current binary: %w", err)
	}

	if err := os.Rename(tmpBin, exe); err != nil {
		if rbErr := os.Rename(bakPath, exe); rbErr != nil {
			return fmt.Errorf("install new binary: %w\nROLLBACK ALSO FAILED: %v\nYour previous binary is at: %s", err, rbErr, bakPath)
		}
		return fmt.Errorf("install new binary (rolled back): %w", err)
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
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "tracker/"+version)

	resp, err := updateDLClient.Do(req)
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

	n, err := io.Copy(f, io.LimitReader(resp.Body, maxTarballSize+1))
	if err != nil {
		os.Remove(f.Name())
		return "", err
	}
	if n > maxTarballSize {
		os.Remove(f.Name())
		return "", fmt.Errorf("download too large (%d bytes, max %d)", n, maxTarballSize)
	}
	return f.Name(), nil
}

// verifyChecksum downloads checksums.txt and verifies the file's SHA256.
// Note: checksums are fetched from the same GitHub release as the binary.
// This guards against download corruption, not supply chain compromise.
func verifyChecksum(filePath, assetName, checksumsURL string) error {
	expectedHash, err := fetchExpectedHash(assetName, checksumsURL)
	if err != nil {
		return err
	}
	actualHash, err := computeFileSHA256(filePath)
	if err != nil {
		return err
	}
	if actualHash != expectedHash {
		return fmt.Errorf("checksum mismatch: expected %s, got %s", expectedHash, actualHash)
	}
	return nil
}

// fetchExpectedHash downloads checksums.txt and extracts the hash for assetName.
func fetchExpectedHash(assetName, checksumsURL string) (string, error) {
	req, err := http.NewRequest("GET", checksumsURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "tracker/"+version)

	resp, err := updateDLClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return "", fmt.Errorf("fetch checksums: HTTP %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxChecksumsSize))
	if err != nil {
		return "", err
	}

	// Parse checksums.txt: each line is "hash  filename"
	for _, line := range strings.Split(string(body), "\n") {
		parts := strings.Fields(line)
		if len(parts) == 2 && parts[1] == assetName {
			return parts[0], nil
		}
	}
	return "", fmt.Errorf("no checksum found for %s", assetName)
}

// computeFileSHA256 opens a file and returns its SHA256 hex digest.
func computeFileSHA256(filePath string) (string, error) {
	f, err := os.Open(filePath)
	if err != nil {
		return "", err
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// extractBinaryFromTar extracts the "tracker" binary from a .tar.gz file.
// Output path is hardcoded (not derived from tar entry names) to prevent path traversal.
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

	return findAndExtractBinary(tar.NewReader(gz), destDir)
}

// findAndExtractBinary scans a tar archive for the "tracker" binary and writes it to destDir.
func findAndExtractBinary(tr *tar.Reader, destDir string) (string, error) {
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", err
		}
		base := filepath.Base(hdr.Name)
		if base == "tracker" && hdr.Typeflag == tar.TypeReg {
			return writeBinaryEntry(tr, destDir)
		}
	}
	return "", fmt.Errorf("tracker binary not found in archive")
}

// writeBinaryEntry writes the current tar entry to destDir/.tracker-new.
func writeBinaryEntry(tr *tar.Reader, destDir string) (string, error) {
	tmpBin := filepath.Join(destDir, ".tracker-new")
	out, err := os.Create(tmpBin)
	if err != nil {
		return "", err
	}
	n, err := io.Copy(out, io.LimitReader(tr, maxBinarySize+1))
	out.Close()
	if err != nil {
		os.Remove(tmpBin)
		return "", err
	}
	if n > maxBinarySize {
		os.Remove(tmpBin)
		return "", fmt.Errorf("extracted binary too large (%d bytes, max %d)", n, maxBinarySize)
	}
	return tmpBin, nil
}
