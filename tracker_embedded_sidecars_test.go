// ABOUTME: Library-path tests for embedded built-ins whose *_file sidecars live in the embed FS.
// ABOUTME: Covers ResolveSource→Simulate/ValidateSource, capture IR, and doctor's file-anchored parse.
package tracker

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/2389-research/dippin-lang/ir"
)

// chdirEmpty moves the test into an empty temp dir so nothing can resolve from
// cwd — any sidecar that loads must have come from the embed FS.
func chdirEmpty(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	orig, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(orig) })
}

func TestEmbeddedWorkflowForSource(t *testing.T) {
	src, info, err := OpenWorkflow("build_product")
	if err != nil {
		t.Fatal(err)
	}
	got, ok := embeddedWorkflowForSource(string(src))
	if !ok || got.File != info.File {
		t.Fatalf("embedded source not recognised: ok=%v file=%q", ok, got.File)
	}
	if _, ok := embeddedWorkflowForSource(string(src) + "\n# edited\n"); ok {
		t.Error("an edited copy must not be treated as the built-in")
	}
	if _, ok := embeddedWorkflowForSource(""); ok {
		t.Error("empty source must not match")
	}
}

func TestEmbeddedWorkflowFS_ExposesBuiltins(t *testing.T) {
	for _, wf := range Workflows() {
		if _, err := EmbeddedWorkflowFS().Open(wf.File); err != nil {
			t.Errorf("EmbeddedWorkflowFS missing %s: %v", wf.File, err)
		}
	}
}

// The library's bare-name path: ResolveSource hands back the built-in's text,
// and Simulate / ValidateSource / DescribeInputs must resolve its sidecars
// from the embed FS even when cwd has none.
func TestBuiltinSourceResolvesSidecarsFromEmbedFS(t *testing.T) {
	chdirEmpty(t)
	src, _, err := ResolveSource("build_product", "")
	if err != nil {
		t.Fatal(err)
	}
	report, err := Simulate(context.Background(), src)
	if err != nil {
		t.Fatalf("Simulate: %v", err)
	}
	var found bool
	for _, n := range report.Nodes {
		if n.ID == "SpecLint" {
			found = true
			if !strings.Contains(n.Attrs["prompt"], "SPEC COHERENCE PREFLIGHT") {
				t.Errorf("SpecLint prompt not resolved: %.80q", n.Attrs["prompt"])
			}
		}
	}
	if !found {
		t.Error("SpecLint missing from simulate report")
	}
	res, err := ValidateSource(src, WithValidateFormat(FormatDip))
	if err != nil {
		t.Fatalf("ValidateSource: %v (errors: %v)", err, res.Errors)
	}
	if _, err := DescribeInputs(src, FormatDip); err != nil {
		t.Fatalf("DescribeInputs: %v", err)
	}
}

// Capture must see the same resolved IR for a built-in as the run does.
func TestParseWorkflowForCapture_EmbeddedResolvesSidecars(t *testing.T) {
	chdirEmpty(t)
	src, _, err := OpenWorkflow("build_product")
	if err != nil {
		t.Fatal(err)
	}
	w := parseWorkflowForCapture(string(src))
	if w == nil {
		t.Fatal("capture IR is nil for a built-in")
	}
	for _, n := range w.Nodes {
		if n.ID != "SpecLint" {
			continue
		}
		if !strings.Contains(n.Config.(ir.AgentConfig).Prompt, "SPEC COHERENCE PREFLIGHT") {
			t.Errorf("SpecLint prompt unresolved in capture IR")
		}
	}
}

// doctor: a disk file's sidecars resolve relative to the file, not cwd —
// megaplan already uses the sidecar layout, so it is a ready-made fixture.
func TestCheckPipelineFile_AnchorsDirectivesAtFileDir(t *testing.T) {
	repoRoot, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(repoRoot, "examples", "megaplan.dip")
	chdirEmpty(t)
	out := checkPipelineFile(file)
	if out.Status == CheckStatusError {
		t.Fatalf("doctor could not parse %s from another cwd: %s", file, out.Message)
	}
	graph, msg, ok := loadGraphForGitRequires(context.Background(), file)
	if !ok || graph == nil {
		t.Fatalf("loadGraphForGitRequires: %s", msg)
	}
}

// doctor: a bare built-in name loads from the embed FS.
func TestCheckPipelineFile_BareBuiltinName(t *testing.T) {
	chdirEmpty(t)
	out := checkPipelineFile("build_product")
	if out.Status == CheckStatusError {
		t.Fatalf("doctor rejected bare built-in name: %s (%s)", out.Message, out.Hint)
	}
	graph, msg, ok := loadGraphForGitRequires(context.Background(), "build_product")
	if !ok || graph == nil {
		t.Fatalf("loadGraphForGitRequires(build_product): %s", msg)
	}
	// A path-shaped name that is missing must still be reported as missing.
	if out := checkPipelineFile("build_product.dip"); out.Status != CheckStatusError {
		t.Errorf("missing ./build_product.dip should be an error, got %s: %s", out.Status, out.Message)
	}
}
