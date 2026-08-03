// ABOUTME: Tests for spec-artifact capture into the run directory.
// ABOUTME: Covers verbatim source, expanded IR, content-addressed inputs, and best-effort failure behavior.
package pipeline

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/2389-research/dippin-lang/ir"
)

func TestWriteSpecArtifactsStoresSourceAndIR(t *testing.T) {
	runDir := filepath.Join(t.TempDir(), "run1")
	source := "workflow Hello\n  goal: \"say hi\"\n  start: A\n  exit: A\n"
	workflow := &ir.Workflow{
		Name: "Hello",
		Goal: "say hi",
		Nodes: []*ir.Node{
			{ID: "A", Kind: ir.NodeAgent},
		},
	}

	manifest, err := WriteSpecArtifacts(runDir, SpecArtifacts{
		SourcePath: "/tmp/hello.dip",
		Source:     source,
		Workflow:   workflow,
		Params:     map[string]string{"model": "claude-haiku-4-5"},
	})
	if err != nil {
		t.Fatalf("WriteSpecArtifacts: %v", err)
	}

	// The .dip must be byte-identical: a spec diff between two runs is the
	// whole point, and a reformatted copy would show spurious changes.
	got, err := os.ReadFile(filepath.Join(runDir, SpecSourceFile))
	if err != nil {
		t.Fatalf("read source: %v", err)
	}
	if string(got) != source {
		t.Errorf("stored source = %q, want verbatim %q", got, source)
	}
	if manifest.SourceSHA256 == "" {
		t.Error("manifest records no source digest")
	}
	if manifest.SourcePath != "/tmp/hello.dip" {
		t.Errorf("manifest SourcePath = %q, want the original path", manifest.SourcePath)
	}
	if manifest.Params["model"] != "claude-haiku-4-5" {
		t.Errorf("manifest Params = %v, want the passed params", manifest.Params)
	}

	// The IR is the graph that actually ran; dippin expands subgraphs at
	// compile time, so it is a different document from the source.
	irData, err := os.ReadFile(filepath.Join(runDir, SpecIRFile))
	if err != nil {
		t.Fatalf("read IR: %v", err)
	}
	var parsed map[string]any
	if err := json.Unmarshal(irData, &parsed); err != nil {
		t.Fatalf("IR is not valid JSON: %v", err)
	}
	if manifest.IRFile != SpecIRFile {
		t.Errorf("manifest IRFile = %q, want %q", manifest.IRFile, SpecIRFile)
	}
}

func TestWriteSpecArtifactsContentAddressesInputs(t *testing.T) {
	runDir := filepath.Join(t.TempDir(), "run2")
	body := []byte("# Sprint 001\nStand up the skeleton.\n")

	manifest, err := WriteSpecArtifacts(runDir, SpecArtifacts{
		Source: "workflow X\n",
		Inputs: []SpecInput{
			{Name: "SPRINT-001.md", Role: "sprint", Content: body},
		},
	})
	if err != nil {
		t.Fatalf("WriteSpecArtifacts: %v", err)
	}
	if len(manifest.Inputs) != 1 {
		t.Fatalf("manifest has %d inputs, want 1", len(manifest.Inputs))
	}

	in := manifest.Inputs[0]
	if in.SHA256 == "" {
		t.Fatal("input has no digest")
	}
	if in.Size != len(body) {
		t.Errorf("input Size = %d, want %d", in.Size, len(body))
	}
	if in.Ext != ".md" {
		t.Errorf("input Ext = %q, want .md (inferred from the name)", in.Ext)
	}

	// Stored under its digest, so identical inputs across runs collide by
	// design and a recorded digest can be checked against the bytes.
	stored, err := os.ReadFile(filepath.Join(runDir, SpecInputsDir, in.SHA256+in.Ext))
	if err != nil {
		t.Fatalf("read stored input: %v", err)
	}
	if string(stored) != string(body) {
		t.Errorf("stored input = %q, want %q", stored, body)
	}

	// The manifest must not inline the bytes — it stays small so a reader can
	// load it without pulling every input into memory.
	encoded, err := json.Marshal(manifest)
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}
	if strings.Contains(string(encoded), "Stand up the skeleton") {
		t.Error("manifest inlines input content; it should only reference the digest")
	}
}

func TestWriteSpecArtifactsDedupesIdenticalInputs(t *testing.T) {
	runDir := filepath.Join(t.TempDir(), "run3")
	body := []byte("shared reference material")

	manifest, err := WriteSpecArtifacts(runDir, SpecArtifacts{
		Inputs: []SpecInput{
			{Name: "a.txt", Content: body},
			{Name: "b.txt", Content: body},
		},
	})
	if err != nil {
		t.Fatalf("WriteSpecArtifacts: %v", err)
	}
	if len(manifest.Inputs) != 2 {
		t.Fatalf("manifest has %d inputs, want both recorded", len(manifest.Inputs))
	}
	if manifest.Inputs[0].SHA256 != manifest.Inputs[1].SHA256 {
		t.Error("identical content produced different digests")
	}

	// Both manifest entries survive (they had different names), but only one
	// file is stored, because the name is the digest.
	entries, err := os.ReadDir(filepath.Join(runDir, SpecInputsDir))
	if err != nil {
		t.Fatalf("read inputs dir: %v", err)
	}
	if len(entries) != 1 {
		t.Errorf("stored %d files, want 1 (content-addressed)", len(entries))
	}
}

func TestWriteSpecArtifactsSkipsEmptyFields(t *testing.T) {
	runDir := filepath.Join(t.TempDir(), "run4")

	manifest, err := WriteSpecArtifacts(runDir, SpecArtifacts{})
	if err != nil {
		t.Fatalf("WriteSpecArtifacts: %v", err)
	}
	// Nothing supplied, nothing written, and no phantom manifest entries
	// pointing at files that don't exist.
	if manifest.SourceFile != "" || manifest.IRFile != "" || len(manifest.Inputs) != 0 {
		t.Errorf("manifest references artifacts that were never written: %+v", manifest)
	}
	for _, name := range []string{SpecSourceFile, SpecIRFile, SpecInputsDir} {
		if _, err := os.Stat(filepath.Join(runDir, name)); !os.IsNotExist(err) {
			t.Errorf("%s exists but nothing was supplied for it", name)
		}
	}
}

func TestWriteSpecArtifactsRecordsBundleIdentity(t *testing.T) {
	runDir := filepath.Join(t.TempDir(), "run5")
	manifest, err := WriteSpecArtifacts(runDir, SpecArtifacts{
		Source:         "workflow X\n",
		BundleIdentity: "sha256:deadbeef",
	})
	if err != nil {
		t.Fatalf("WriteSpecArtifacts: %v", err)
	}
	if manifest.BundleIdentity != "sha256:deadbeef" {
		t.Errorf("BundleIdentity = %q, want it carried through", manifest.BundleIdentity)
	}
}

func TestWriteSpecArtifactsIsBestEffortPerFile(t *testing.T) {
	runDir := filepath.Join(t.TempDir(), "run6")
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Occupy the inputs path with a regular file so MkdirAll under it fails,
	// while the source write is unaffected.
	if err := os.WriteFile(filepath.Join(runDir, SpecInputsDir), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	manifest, err := WriteSpecArtifacts(runDir, SpecArtifacts{
		Source: "workflow X\n",
		Inputs: []SpecInput{{Name: "a.txt", Content: []byte("a")}},
	})
	if err == nil {
		t.Error("expected an error for the input that could not be stored")
	}
	// The source still landed: one artifact failing must not cost the others.
	if manifest.SourceFile == "" {
		t.Error("source was not written despite being independent of the failure")
	}
	if len(manifest.Inputs) != 0 {
		t.Error("manifest lists an input that was never stored")
	}
}

func TestDigestIsStable(t *testing.T) {
	// Bind the first call rather than comparing digest(x) != digest(x) inline:
	// staticcheck flags identical operands (SA4000) even though determinism is
	// exactly what this asserts.
	abc := digest([]byte("abc"))
	if abc != digest([]byte("abc")) {
		t.Error("digest is not deterministic")
	}
	if abc == digest([]byte("abd")) {
		t.Error("digest collides on different content")
	}
}

// TestSecretParamsAreRedacted covers a CodeRabbit finding on #519: --param
// values commonly carry credentials, and the manifest is durable on-disk, so a
// secret-looking key must not persist its value. Keys are kept — knowing which
// params a run was given is part of reproducing it, and the name is not secret.
func TestSecretParamsAreRedacted(t *testing.T) {
	got := redactSecretParams(map[string]string{
		"api_key":     "sk-live-abc123",
		"AUTH_TOKEN":  "bearer-xyz",
		"db-password": "hunter2",
		"model":       "claude-haiku-4-5",
		"empty_token": "",
	})

	for _, k := range []string{"api_key", "AUTH_TOKEN", "db-password"} {
		if got[k] != redactedParamValue {
			t.Errorf("%s = %q, want %q", k, got[k], redactedParamValue)
		}
	}
	if got["model"] != "claude-haiku-4-5" {
		t.Errorf("non-secret param was altered: %q", got["model"])
	}
	// An empty value has nothing to hide; redacting it would imply a secret
	// exists where none was passed.
	if got["empty_token"] != "" {
		t.Errorf("empty value became %q", got["empty_token"])
	}
	if len(got) != 5 {
		t.Errorf("key set changed: %v", got)
	}
	if redactSecretParams(nil) != nil {
		t.Error("nil params should stay nil")
	}
}

// TestSpecArtifactsAreNarrowedOnRewrite covers a CodeRabbit finding on #519:
// os.WriteFile applies its mode only when it *creates* the file, so a run
// directory that already held a world-readable artifact — a resumed run, a
// reused dir — kept the wider permissions and left the spec (and any params or
// reference material in it) readable by other local users.
func TestSpecArtifactsAreNarrowedOnRewrite(t *testing.T) {
	runDir := filepath.Join(t.TempDir(), "reused")
	inputsDir := filepath.Join(runDir, SpecInputsDir)
	if err := os.MkdirAll(inputsDir, 0o755); err != nil {
		t.Fatal(err)
	}

	body := []byte("reference material")
	sha := digest(body)
	// Pre-create every artifact wide open, as a prior run would have left them.
	pre := map[string][]byte{
		filepath.Join(runDir, SpecSourceFile): []byte("stale\n"),
		filepath.Join(runDir, SpecIRFile):     []byte("{}\n"),
		filepath.Join(inputsDir, sha+".md"):   []byte("stale\n"),
	}
	for path, data := range pre {
		if err := os.WriteFile(path, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	if _, err := WriteSpecArtifacts(runDir, SpecArtifacts{
		Source:   "workflow X\n",
		Workflow: &ir.Workflow{Name: "X"},
		Inputs:   []SpecInput{{Name: "ref.md", Content: body}},
	}); err != nil {
		t.Fatalf("WriteSpecArtifacts: %v", err)
	}

	for path := range pre {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("stat %s: %v", path, err)
		}
		if got := info.Mode().Perm(); got != specFileMode {
			t.Errorf("%s mode = %#o, want %#o — a pre-existing wide file was not narrowed",
				filepath.Base(path), got, specFileMode)
		}
	}
}
