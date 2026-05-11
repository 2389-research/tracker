// ABOUTME: Test helper that packs real .dipx bundles for use in downstream tests.
// ABOUTME: Uses dipx.Pack directly — no synthetic ZIPs, no mocks (per CLAUDE.md).
package dipxtest

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/2389-research/dippin-lang/dipx"
)

// PackTestBundle writes a real .dipx bundle to t.TempDir() that includes
// entryPath as the entry workflow. Returns the absolute path to the .dipx.
// Failures fail the test immediately via t.Fatalf.
func PackTestBundle(t *testing.T, entryPath string) string {
	t.Helper()
	out := filepath.Join(t.TempDir(), "bundle.dipx")
	f, err := os.Create(out)
	if err != nil {
		t.Fatalf("create bundle file: %v", err)
	}
	defer func() { _ = f.Close() }()
	if _, err := dipx.Pack(context.Background(), entryPath, f); err != nil {
		t.Fatalf("dipx.Pack: %v", err)
	}
	return out
}
