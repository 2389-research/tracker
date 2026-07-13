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
	if _, err := dipx.Pack(context.Background(), entryPath, f, dipx.PackOptions{}); err != nil {
		_ = f.Close() // best-effort on the error path; primary error wins
		t.Fatalf("dipx.Pack: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close bundle file: %v", err)
	}
	return out
}

// MinimalDip returns a tiny valid .dip source with the given workflow name,
// start node, and exit node. The two named nodes are bare codergen agents
// wired by a single edge from start to exit.
func MinimalDip(name, start, exit string) string {
	return `workflow ` + name + `
  start: ` + start + `
  exit: ` + exit + `

  agent ` + start + `
    label: "Start"

  agent ` + exit + `
    label: "Exit"

  edges
    ` + start + ` -> ` + exit + `
`
}
