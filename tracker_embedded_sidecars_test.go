// ABOUTME: Library-path tests for embedded built-ins whose *_file sidecars live in the embed FS.
// ABOUTME: Covers ResolveSource→Simulate/ValidateSource, capture IR, and doctor's file-anchored parse.
package tracker

import (
	"context"
	"github.com/2389-research/tracker/llm"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"

	"github.com/2389-research/dippin-lang/ir"
	"github.com/2389-research/dippin-lang/parser"
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
	w := parseWorkflowForCapture(string(src), SourceRef{})
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

// initCopy writes build_product.dip + its sidecars into dir the way
// `tracker init` does (straight from the embed FS), returning the .dip path.
func initCopy(t *testing.T, dir string) string {
	t.Helper()
	src, info, err := OpenWorkflow("build_product")
	if err != nil {
		t.Fatal(err)
	}
	dip := filepath.Join(dir, "build_product.dip")
	if err := os.WriteFile(dip, src, 0o644); err != nil {
		t.Fatal(err)
	}
	wf, err := parser.NewParser(string(src), info.File).Parse()
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range wf.Nodes {
		var rel string
		switch cfg := n.Config.(type) {
		case ir.ToolConfig:
			rel = cfg.CommandFile
		case ir.AgentConfig:
			rel = cfg.PromptFile
		}
		if rel == "" {
			continue
		}
		data, err := fs.ReadFile(embeddedWorkflows, path.Join("examples", rel))
		if err != nil {
			t.Fatal(err)
		}
		dest := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(dest, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dip
}

func setupCommand(t *testing.T, report *SimulateReport) string {
	t.Helper()
	for _, n := range report.Nodes {
		if n.ID == "Setup" {
			return n.Attrs["tool_command"]
		}
	}
	t.Fatal("Setup node missing")
	return ""
}

// Failure mode 1 (reviewer repro): an init copy with an EDITED sidecar but an
// untouched .dip, resolved via ResolveSource(name, dir) and simulated/run with
// cwd != dir, must use the DISK sidecar — never the embedded one just because
// the .dip text still matches the built-in byte-for-byte.
func TestResolveSource_EditedSidecarWinsOverEmbedded(t *testing.T) {
	dir := t.TempDir()
	initCopy(t, dir)
	const marker = "echo EDITED-BY-USER"
	setup := filepath.Join(dir, "scripts", "build_product", "Setup.sh")
	if err := os.WriteFile(setup, []byte(marker), 0o644); err != nil {
		t.Fatal(err)
	}
	chdirEmpty(t) // cwd != dir

	src, info, err := ResolveSource("build_product", dir)
	if err != nil {
		t.Fatal(err)
	}
	if info.Path == "" || info.Name != "" {
		t.Fatalf("expected a filesystem ref for the local copy, got %+v", info)
	}
	report, err := Simulate(context.Background(), src, WithSource(info.Ref()))
	if err != nil {
		t.Fatalf("Simulate: %v", err)
	}
	if got := setupCommand(t, report); got != marker {
		t.Errorf("Simulate used the embedded Setup.sh instead of the edited disk copy: %.60q", got)
	}
	res, err := ValidateSource(src, WithValidateFormat(FormatDip), WithValidateSource(info.Ref()))
	if err != nil {
		t.Fatalf("ValidateSource: %v %v", err, res.Errors)
	}
	if got := res.Graph.Nodes["Setup"].Attrs["tool_command"]; got != marker {
		t.Errorf("ValidateSource used the embedded Setup.sh: %.60q", got)
	}
	if _, err := DescribeInputs(src, FormatDip, WithSource(info.Ref())); err != nil {
		t.Fatalf("DescribeInputs: %v", err)
	}
	// The engine path honours Config.Source the same way (NewEngine parses
	// before any provider wiring; capture is filled from the same ref).
	eng, err := NewEngine(src, Config{WorkingDir: dir, Source: info.Ref(), Capture: &CaptureConfig{}, Git: &GitConfig{Preflight: GitPreflightOff}, LLMClient: &stubCompleter{response: &llm.Response{Message: llm.AssistantMessage("done"), FinishReason: llm.FinishReason{Reason: "stop"}}}})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	defer eng.Close()
	if got := eng.inner.Graph().Nodes["Setup"].Attrs["tool_command"]; got != marker {
		t.Errorf("NewEngine used the embedded Setup.sh: %.60q", got)
	}
	w := eng.capture.spec.Workflow
	if w == nil {
		t.Fatal("capture IR nil")
	}
	for _, n := range w.Nodes {
		if n.ID == "Setup" && n.Config.(ir.ToolConfig).Command != marker {
			t.Errorf("capture IR used the embedded Setup.sh")
		}
	}
}

// Failure mode 2: an EDITED .dip (no longer byte-identical to the built-in)
// loaded with cwd != dir must still resolve its sidecars — via the explicit
// anchor — instead of failing "not found" against the process cwd.
func TestResolveSource_EditedDipResolvesViaAnchor(t *testing.T) {
	dir := t.TempDir()
	dip := initCopy(t, dir)
	src, err := os.ReadFile(dip)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dip, append(src, []byte("\n# edited locally\n")...), 0o644); err != nil {
		t.Fatal(err)
	}
	chdirEmpty(t)

	text, info, err := ResolveSource("build_product", dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Simulate(context.Background(), text, WithSource(info.Ref())); err != nil {
		t.Fatalf("edited .dip did not resolve via anchor: %v", err)
	}
	// Without the anchor the same text falls back to cwd and cannot resolve —
	// the reason callers must pass info.Ref().
	if _, err := Simulate(context.Background(), text); err == nil {
		t.Error("expected cwd-relative resolution to fail for an edited .dip in another dir")
	}
}

// An explicit Builtin ref never consults disk, and an unknown name is an error.
func TestSourceRef_Builtin(t *testing.T) {
	chdirEmpty(t)
	src, info, err := ResolveSource("build_product", "")
	if err != nil {
		t.Fatal(err)
	}
	if info.Ref() != (SourceRef{Builtin: "build_product"}) {
		t.Fatalf("ref = %+v", info.Ref())
	}
	if _, err := Simulate(context.Background(), src, WithSource(info.Ref())); err != nil {
		t.Fatalf("Simulate builtin ref: %v", err)
	}
	if _, err := Simulate(context.Background(), src, WithSource(SourceRef{Builtin: "nope"})); err == nil || !strings.Contains(err.Error(), `no built-in workflow named "nope"`) {
		t.Errorf("unknown builtin should error, got %v", err)
	}
}
