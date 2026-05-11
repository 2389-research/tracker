// ABOUTME: Tests for LoadDipxBundle — the .dipx → Graph + BundleInfo loader.
// ABOUTME: Uses real bundles via dipxtest.PackTestBundle (no synthetic ZIPs, no mocks).
package pipeline

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/2389-research/tracker/pipeline/internal/dipxtest"
)

func TestLoadDipxBundle_HappyPath(t *testing.T) {
	dir := t.TempDir()
	entryPath := filepath.Join(dir, "entry.dip")
	if err := os.WriteFile(entryPath, []byte(dipxtest.MinimalDip("entry_test", "start", "exit")), 0o644); err != nil {
		t.Fatal(err)
	}
	bundlePath := dipxtest.PackTestBundle(t, entryPath)

	graph, subgraphs, info, err := LoadDipxBundle(context.Background(), bundlePath)
	if err != nil {
		t.Fatalf("LoadDipxBundle: %v", err)
	}
	if graph == nil {
		t.Fatal("graph is nil")
	}
	if !graph.DippinValidated {
		t.Error("graph not marked DippinValidated")
	}
	if !strings.HasPrefix(info.Identity, "sha256:") {
		t.Errorf("identity should start with sha256:, got %q", info.Identity)
	}
	if len(info.Identity) != len("sha256:")+64 {
		t.Errorf("identity should be sha256: + 64 hex chars, got len %d (%q)", len(info.Identity), info.Identity)
	}
	if info.EntryPath == "" {
		t.Error("BundleInfo.EntryPath is empty")
	}
	if subgraphs == nil {
		t.Error("subgraphs map should be non-nil (even if empty)")
	}
	t.Logf("bundle identity: %s", info.Identity)
	t.Logf("bundle entry path: %s", info.EntryPath)
}

func TestLoadDipxBundle_NotAValidBundle(t *testing.T) {
	// A plain .dip with .dipx extension.
	fake := filepath.Join(t.TempDir(), "bogus.dipx")
	if err := os.WriteFile(fake, []byte(dipxtest.MinimalDip("not_a_bundle", "start", "exit")), 0o644); err != nil {
		t.Fatal(err)
	}
	_, _, _, err := LoadDipxBundle(context.Background(), fake)
	if err == nil {
		t.Fatal("expected error on non-ZIP .dipx, got nil")
	}
	if !strings.Contains(err.Error(), "load bundle") {
		t.Errorf("error should wrap with 'load bundle': %v", err)
	}
}

func TestLoadDipxBundle_HashMismatch(t *testing.T) {
	dir := t.TempDir()
	entryPath := filepath.Join(dir, "entry.dip")
	if err := os.WriteFile(entryPath, []byte(dipxtest.MinimalDip("tampered_test", "start", "exit")), 0o644); err != nil {
		t.Fatal(err)
	}
	bundlePath := dipxtest.PackTestBundle(t, entryPath)

	// Tamper one byte at offset 100 (compressed data region).
	raw, err := os.ReadFile(bundlePath)
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) < 200 {
		t.Skipf("bundle too small to tamper safely (%d bytes)", len(raw))
	}
	// Offset 100 falls inside the deflate-compressed manifest.json payload for
	// MinimalDip-sized bundles (~530 bytes). The len-guard above catches the
	// case where MinimalDip shrinks meaningfully and the offset hits ZIP headers.
	raw[100] ^= 0xFF
	if err := os.WriteFile(bundlePath, raw, 0o644); err != nil {
		t.Fatal(err)
	}

	_, _, _, err = LoadDipxBundle(context.Background(), bundlePath)
	if err == nil {
		t.Fatal("expected error on tampered bundle, got nil")
	}
}

// TestLoadDipxBundle_WithSubgraph exercises the manifest.Files subgraph
// conversion loop in LoadDipxBundle (bundle.Lookup + LoadDippinWorkflowFromIR
// for each non-entry file). The happy-path test only covers entry-only
// bundles, leaving this branch — relied on by every downstream caller that
// expands subgraph_ref / manager_loop refs — without direct coverage.
func TestLoadDipxBundle_WithSubgraph(t *testing.T) {
	dir := t.TempDir()

	// Write the subgraph file first. dipx.Pack walks the entry's refs from
	// disk and includes them in the bundle automatically, so just having
	// sub.dip sit next to entry.dip is sufficient.
	subPath := filepath.Join(dir, "sub.dip")
	if err := os.WriteFile(subPath, []byte(dipxtest.MinimalDip("sub_workflow", "s_start", "s_exit")), 0o644); err != nil {
		t.Fatal(err)
	}

	// Entry workflow references sub.dip via a subgraph node. Canonical
	// dippin syntax (see examples/variable_interpolation_demo.dip,
	// testdata/expand_parent.dip): kind keyword + node name, then ref:
	// pointing at a sibling .dip path.
	entrySource := `workflow entry_with_sub
  start: a
  exit: c

  agent a
    label: "Start"

  subgraph b
    ref: sub.dip

  agent c
    label: "Exit"

  edges
    a -> b
    b -> c
`
	entryPath := filepath.Join(dir, "entry.dip")
	if err := os.WriteFile(entryPath, []byte(entrySource), 0o644); err != nil {
		t.Fatal(err)
	}

	bundlePath := dipxtest.PackTestBundle(t, entryPath)

	graph, subgraphs, info, err := LoadDipxBundle(context.Background(), bundlePath)
	if err != nil {
		t.Fatalf("LoadDipxBundle: %v", err)
	}
	if graph == nil {
		t.Fatal("entry graph is nil")
	}
	if !graph.DippinValidated {
		t.Error("entry graph not marked DippinValidated")
	}
	if info.EntryPath == "" {
		t.Error("BundleInfo.EntryPath is empty")
	}

	if len(subgraphs) < 1 {
		t.Fatalf("expected at least 1 subgraph, got %d (keys: %v)", len(subgraphs), keys(subgraphs))
	}
	for path, sub := range subgraphs {
		if !strings.HasPrefix(path, "workflows/") {
			t.Errorf("subgraph key %q not canonical bundle path (should start with workflows/)", path)
		}
		if !strings.HasSuffix(path, ".dip") {
			t.Errorf("subgraph key %q does not end in .dip", path)
		}
		if path == info.EntryPath {
			t.Errorf("subgraphs map should not contain the entry path %q", path)
		}
		if sub == nil {
			t.Errorf("subgraph %q is nil", path)
			continue
		}
		if !sub.DippinValidated {
			t.Errorf("subgraph %q not marked DippinValidated", path)
		}
	}
}

// keys returns the keys of a map[string]*Graph for diagnostic output.
func keys(m map[string]*Graph) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
