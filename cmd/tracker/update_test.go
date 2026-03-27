package main

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestVersionsEqual(t *testing.T) {
	tests := []struct {
		a, b string
		want bool
	}{
		{"0.12.0", "0.12.0", true},
		{"v0.12.0", "0.12.0", true},
		{"0.12.0", "v0.12.0", true},
		{"v0.12.0", "v0.12.0", true},
		{"0.12.0", "0.12.1", false},
		{"dev", "v0.12.0", false},
		{"", "", true},
		{"v", "", true}, // edge: "v" trimmed to ""
	}
	for _, tt := range tests {
		if got := versionsEqual(tt.a, tt.b); got != tt.want {
			t.Errorf("versionsEqual(%q, %q) = %v, want %v", tt.a, tt.b, got, tt.want)
		}
	}
}

func TestClassifyInstallPath(t *testing.T) {
	tests := []struct {
		name     string
		resolved string
		gobin    string
		gopath   string
		want     string
	}{
		{"cellar", "/usr/local/Cellar/tracker/0.12.0/bin/tracker", "", "", "homebrew"},
		{"opt homebrew", "/opt/homebrew/bin/tracker", "", "", "homebrew"},
		{"linuxbrew cellar", "/home/linuxbrew/.linuxbrew/Cellar/tracker/0.12.0/bin/tracker", "", "", "homebrew"},
		{"gobin", "/custom/gobin/tracker", "/custom/gobin", "", "go-install"},
		{"gopath", "/home/user/go/bin/tracker", "", "/home/user/go", "go-install"},
		{"binary", "/usr/local/bin/tracker", "", "", "binary"},
		{"random path", "/tmp/tracker", "", "", "binary"},
		{"not homebrew user", "/Users/homebrew/bin/tracker", "", "", "binary"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := classifyInstallPath(tt.resolved, tt.gobin, tt.gopath)
			if got != tt.want {
				t.Errorf("classifyInstallPath(%q, %q, %q) = %q, want %q",
					tt.resolved, tt.gobin, tt.gopath, got, tt.want)
			}
		})
	}
}

func TestFindAsset(t *testing.T) {
	// Build the exact asset name that findAsset will look for
	expectedName := fmt.Sprintf("tracker_0.12.0_%s_%s.tar.gz", runtime.GOOS, runtime.GOARCH)

	t.Run("matching asset found", func(t *testing.T) {
		release := &githubRelease{
			TagName: "v0.12.0",
			Assets: []githubAsset{
				{Name: expectedName, BrowserDownloadURL: "https://example.com/match.tar.gz"},
				{Name: "tracker_0.12.0_other_os.tar.gz", BrowserDownloadURL: "https://example.com/other.tar.gz"},
				{Name: "checksums.txt", BrowserDownloadURL: "https://example.com/checksums.txt"},
			},
		}
		name, url, checksums := findAsset(release)
		if name != expectedName {
			t.Errorf("name = %q, want %q", name, expectedName)
		}
		if url != "https://example.com/match.tar.gz" {
			t.Errorf("url = %q, want match.tar.gz URL", url)
		}
		if checksums != "https://example.com/checksums.txt" {
			t.Errorf("checksums = %q, want checksums.txt URL", checksums)
		}
	})

	t.Run("v prefix stripped from tag", func(t *testing.T) {
		release := &githubRelease{
			TagName: "v1.0.0",
			Assets:  []githubAsset{},
		}
		name, _, _ := findAsset(release)
		if strings.Contains(name, "vv") || strings.HasPrefix(name, "tracker_v") {
			t.Errorf("v prefix not stripped: name = %q", name)
		}
		want := fmt.Sprintf("tracker_1.0.0_%s_%s.tar.gz", runtime.GOOS, runtime.GOARCH)
		if name != want {
			t.Errorf("name = %q, want %q", name, want)
		}
	})

	t.Run("no matching asset", func(t *testing.T) {
		release := &githubRelease{
			TagName: "v99.99.99",
			Assets: []githubAsset{
				{Name: "tracker_0.12.0_linux_amd64.tar.gz", BrowserDownloadURL: "https://example.com/linux.tar.gz"},
			},
		}
		_, url, _ := findAsset(release)
		if url != "" {
			t.Errorf("expected empty URL for non-matching version, got %q", url)
		}
	})

	t.Run("no checksums", func(t *testing.T) {
		release := &githubRelease{
			TagName: "v0.12.0",
			Assets: []githubAsset{
				{Name: expectedName, BrowserDownloadURL: "https://example.com/match.tar.gz"},
			},
		}
		_, _, checksums := findAsset(release)
		if checksums != "" {
			t.Errorf("expected empty checksums URL, got %q", checksums)
		}
	})
}

func TestVerifyChecksum(t *testing.T) {
	dir := t.TempDir()

	// Create a test file with known content
	content := []byte("hello tracker binary")
	filePath := filepath.Join(dir, "test.tar.gz")
	if err := os.WriteFile(filePath, content, 0o644); err != nil {
		t.Fatal(err)
	}

	// Compute its SHA256
	h := sha256.Sum256(content)
	correctHash := hex.EncodeToString(h[:])

	t.Run("valid checksum passes", func(t *testing.T) {
		checksumBody := fmt.Sprintf("deadbeef  other_file.tar.gz\n%s  test.tar.gz\n", correctHash)
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprint(w, checksumBody)
		}))
		defer srv.Close()

		err := verifyChecksum(filePath, "test.tar.gz", srv.URL)
		if err != nil {
			t.Errorf("expected pass, got error: %v", err)
		}
	})

	t.Run("wrong checksum fails", func(t *testing.T) {
		checksumBody := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa  test.tar.gz\n"
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprint(w, checksumBody)
		}))
		defer srv.Close()

		err := verifyChecksum(filePath, "test.tar.gz", srv.URL)
		if err == nil {
			t.Error("expected checksum mismatch error")
		}
		if err != nil && !strings.Contains(err.Error(), "checksum mismatch") {
			t.Errorf("expected 'checksum mismatch', got: %v", err)
		}
	})

	t.Run("asset not in checksums file", func(t *testing.T) {
		checksumBody := "deadbeef  other_file.tar.gz\n"
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprint(w, checksumBody)
		}))
		defer srv.Close()

		err := verifyChecksum(filePath, "test.tar.gz", srv.URL)
		if err == nil {
			t.Error("expected 'no checksum found' error")
		}
		if err != nil && !strings.Contains(err.Error(), "no checksum found") {
			t.Errorf("expected 'no checksum found', got: %v", err)
		}
	})

	t.Run("HTTP error returns clear message", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(403)
		}))
		defer srv.Close()

		err := verifyChecksum(filePath, "test.tar.gz", srv.URL)
		if err == nil {
			t.Error("expected HTTP error")
		}
		if err != nil && !strings.Contains(err.Error(), "HTTP 403") {
			t.Errorf("expected 'HTTP 403', got: %v", err)
		}
	})
}

func TestExtractBinaryFromTar(t *testing.T) {
	dir := t.TempDir()

	t.Run("binary at root", func(t *testing.T) {
		tarPath := createTestTar(t, dir, "root.tar.gz", "tracker", []byte("#!/bin/sh\necho ok"))
		result, err := extractBinaryFromTar(tarPath, dir)
		if err != nil {
			t.Fatal(err)
		}
		defer os.Remove(result)

		data, _ := os.ReadFile(result)
		if string(data) != "#!/bin/sh\necho ok" {
			t.Errorf("unexpected content: %q", data)
		}
	})

	t.Run("binary in subdirectory", func(t *testing.T) {
		tarPath := createTestTar(t, dir, "sub.tar.gz", "dist/tracker", []byte("binary-content"))
		result, err := extractBinaryFromTar(tarPath, dir)
		if err != nil {
			t.Fatal(err)
		}
		defer os.Remove(result)

		data, _ := os.ReadFile(result)
		if string(data) != "binary-content" {
			t.Errorf("unexpected content: %q", data)
		}
	})

	t.Run("no tracker binary", func(t *testing.T) {
		tarPath := createTestTar(t, dir, "nobin.tar.gz", "README.md", []byte("readme"))
		_, err := extractBinaryFromTar(tarPath, dir)
		if err == nil {
			t.Error("expected error for missing binary")
		}
		if err != nil && !strings.Contains(err.Error(), "not found in archive") {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("directory entry named tracker is skipped", func(t *testing.T) {
		// Create a tar with a directory entry named "tracker" (not a regular file)
		tarPath := filepath.Join(dir, "direntry.tar.gz")
		f, err := os.Create(tarPath)
		if err != nil {
			t.Fatal(err)
		}
		gw := gzip.NewWriter(f)
		tw := tar.NewWriter(gw)
		tw.WriteHeader(&tar.Header{
			Name:     "tracker",
			Typeflag: tar.TypeDir,
			Mode:     0o755,
		})
		tw.Close()
		gw.Close()
		f.Close()

		_, err = extractBinaryFromTar(tarPath, dir)
		if err == nil {
			t.Error("expected error — directory entry should not match")
		}
	})
}

func TestAtomicSwap(t *testing.T) {
	t.Run("successful swap", func(t *testing.T) {
		dir := t.TempDir()
		exe := filepath.Join(dir, "tracker-old")
		newBin := filepath.Join(dir, "tracker-new")
		os.WriteFile(exe, []byte("old"), 0o755)
		os.WriteFile(newBin, []byte("new"), 0o755)

		err := atomicSwap(exe, newBin, "v1.0.0")
		if err != nil {
			t.Fatal(err)
		}

		data, _ := os.ReadFile(exe)
		if string(data) != "new" {
			t.Errorf("exe should contain new binary, got %q", data)
		}

		bakData, _ := os.ReadFile(exe + ".bak")
		if string(bakData) != "old" {
			t.Errorf("bak should contain old binary, got %q", bakData)
		}

		// Verify newBin path no longer exists (it was renamed)
		if _, err := os.Stat(newBin); !os.IsNotExist(err) {
			t.Error("tmpBin should no longer exist after rename")
		}
	})

	t.Run("rollback on failure", func(t *testing.T) {
		dir := t.TempDir()
		exe := filepath.Join(dir, "tracker-rb")
		os.WriteFile(exe, []byte("original"), 0o755)

		// Pass a non-existent tmpBin path — rename will fail
		err := atomicSwap(exe, filepath.Join(dir, "nonexistent"), "v2.0.0")
		if err == nil {
			t.Error("expected error")
		}
		if !strings.Contains(err.Error(), "rolled back") {
			t.Errorf("expected 'rolled back' in error, got: %v", err)
		}

		// Original should be restored
		data, _ := os.ReadFile(exe)
		if string(data) != "original" {
			t.Errorf("expected rollback to restore original, got %q", data)
		}
	})

	t.Run("pre-existing bak is replaced", func(t *testing.T) {
		dir := t.TempDir()
		exe := filepath.Join(dir, "tracker-bak")
		os.WriteFile(exe, []byte("current"), 0o755)
		os.WriteFile(exe+".bak", []byte("ancient"), 0o755)
		os.WriteFile(filepath.Join(dir, "tracker-new2"), []byte("newest"), 0o755)

		err := atomicSwap(exe, filepath.Join(dir, "tracker-new2"), "v3.0.0")
		if err != nil {
			t.Fatal(err)
		}

		bakData, _ := os.ReadFile(exe + ".bak")
		if string(bakData) != "current" {
			t.Errorf("bak should be previous current, got %q", bakData)
		}
	})
}

func TestUpdateCheckCache(t *testing.T) {
	t.Run("read nonexistent", func(t *testing.T) {
		dir := t.TempDir()
		cache := readUpdateCache(filepath.Join(dir, "nope.json"))
		if cache.Latest != "" {
			t.Errorf("expected empty Latest, got %q", cache.Latest)
		}
	})

	t.Run("roundtrip", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "cache.json")
		cache := updateCheckCache{Latest: "v1.2.3"}
		writeUpdateCache(path, cache)

		got := readUpdateCache(path)
		if got.Latest != "v1.2.3" {
			t.Errorf("expected v1.2.3, got %q", got.Latest)
		}
	})

	t.Run("corrupt file returns zero value and is removed", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "bad.json")
		os.WriteFile(path, []byte("not json{{{"), 0o600)
		cache := readUpdateCache(path)
		if cache.Latest != "" {
			t.Errorf("expected empty Latest for corrupt cache, got %q", cache.Latest)
		}
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Error("expected corrupt cache file to be removed")
		}
	})
}

// helpers

func createTestTar(t *testing.T, dir, name, entryName string, content []byte) string {
	t.Helper()
	path := filepath.Join(dir, name)
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	gw := gzip.NewWriter(f)
	tw := tar.NewWriter(gw)

	hdr := &tar.Header{
		Name:     entryName,
		Size:     int64(len(content)),
		Mode:     0o755,
		Typeflag: tar.TypeReg,
	}
	if err := tw.WriteHeader(hdr); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(content); err != nil {
		t.Fatal(err)
	}

	tw.Close()
	gw.Close()
	return path
}
