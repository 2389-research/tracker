// ABOUTME: Tests for LoadDipxBundle — the .dipx → Graph + BundleInfo loader.
// ABOUTME: Uses real bundles via dipxtest.PackTestBundle (no synthetic ZIPs, no mocks).
package pipeline

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/2389-research/dippin-lang/validator"
	"github.com/2389-research/tracker/internal/dipxtest"
)

func TestLoadDipxBundle_HappyPath(t *testing.T) {
	dir := t.TempDir()
	entryPath := filepath.Join(dir, "entry.dip")
	if err := os.WriteFile(entryPath, []byte(dipxtest.MinimalDip("entry_test", "start", "exit")), 0o644); err != nil {
		t.Fatal(err)
	}
	bundlePath := dipxtest.PackTestBundle(t, entryPath)

	graph, subgraphs, info, _, err := LoadDipxBundle(context.Background(), bundlePath)
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
	_, _, _, _, err := LoadDipxBundle(context.Background(), fake)
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

	_, _, _, _, err = LoadDipxBundle(context.Background(), bundlePath)
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

	graph, subgraphs, info, _, err := LoadDipxBundle(context.Background(), bundlePath)
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

	// Critical assertion: every subgraph_ref in the entry graph must be the
	// canonical bundle path (matching a key in the subgraphs map), not the
	// author's source ref. Regression guard for the bug fixed when LoadDipxBundle
	// started canonicalizing refs after IR-to-Graph conversion.
	//
	// Direct Attrs read intentional: this test verifies that
	// canonicalizeSubgraphRefs rewrites the raw attribute value, so we must
	// inspect it raw. subgraph_ref has no typed accessor on *pipeline.Node —
	// the codebase consistently reads it via node.Attrs["subgraph_ref"]
	// (see pipeline/subgraph.go, cmd/tracker/nodes.go, cmd/tracker/loading.go).
	foundRef := false
	for _, node := range graph.Nodes {
		ref := node.Attrs["subgraph_ref"]
		if ref == "" {
			continue
		}
		foundRef = true
		if _, ok := subgraphs[ref]; !ok {
			t.Errorf("subgraph_ref %q on node %q not found in subgraphs map (keys: %v)", ref, node.ID, keys(subgraphs))
		}
		if !strings.HasPrefix(ref, "workflows/") {
			t.Errorf("subgraph_ref %q on node %q should be canonical bundle path (start with workflows/)", ref, node.ID)
		}
	}
	if !foundRef {
		t.Error("expected at least one node with a subgraph_ref attr; found none")
	}
}

// TestLoadDipxBundle_SuppressesDIP126 confirms that the bundle loader filters
// out dippin's DIP126 lint warning ("subgraph ref file does not exist") on
// every load. The warning is misleading for bundles — the file IS in the ZIP
// and dipx.Open has already verified ref closure, but dippin's lint calls
// os.Stat on a bundle-relative path that has no on-disk counterpart.
func TestLoadDipxBundle_SuppressesDIP126(t *testing.T) {
	dir := t.TempDir()
	subPath := filepath.Join(dir, "sub.dip")
	if err := os.WriteFile(subPath, []byte(dipxtest.MinimalDip("sub_wf_dip126", "s_start", "s_exit")), 0o644); err != nil {
		t.Fatal(err)
	}
	entryPath := filepath.Join(dir, "entry.dip")
	entrySource := `workflow entry_with_sub_dip126
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
	if err := os.WriteFile(entryPath, []byte(entrySource), 0o644); err != nil {
		t.Fatal(err)
	}
	bundlePath := dipxtest.PackTestBundle(t, entryPath)

	_, _, _, diags, loadErr := LoadDipxBundle(context.Background(), bundlePath)
	if loadErr != nil {
		t.Fatalf("LoadDipxBundle: %v", loadErr)
	}
	for _, d := range diags {
		if d.Code == validator.DIP126 {
			t.Errorf("DIP126 should be suppressed for bundle loads; got: %s", d.String())
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
