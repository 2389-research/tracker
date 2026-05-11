// ABOUTME: Tests for the PackTestBundle helper — verifies it produces a real
// ABOUTME: .dipx that round-trips through dipx.Open with a populated manifest.
package dipxtest

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/2389-research/dippin-lang/dipx"
)

func TestPackTestBundle_ProducesValidBundle(t *testing.T) {
	dir := t.TempDir()
	entryPath := filepath.Join(dir, "entry.dip")
	if err := os.WriteFile(entryPath, []byte(minimalDip("entry", "start", "exit")), 0o644); err != nil {
		t.Fatal(err)
	}

	bundlePath := PackTestBundle(t, entryPath)

	if _, err := os.Stat(bundlePath); err != nil {
		t.Fatalf("bundle file should exist: %v", err)
	}

	bundle, err := dipx.Open(context.Background(), bundlePath)
	if err != nil {
		t.Fatalf("dipx.Open on packed bundle: %v", err)
	}
	if bundle.Manifest().Entry == "" {
		t.Errorf("bundle manifest has no entry")
	}
}

// minimalDip returns a tiny valid .dip source with the given workflow name,
// start node, and exit node. The two named nodes are bare codergen agents
// wired by a single edge from start to exit.
func minimalDip(name, start, exit string) string {
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
