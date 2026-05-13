# Native `.dipx` Bundle Support Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Make tracker accept `.dipx` bundles (content-addressed, SHA-256-verified ZIPs from dippin v0.24) natively wherever it accepts a pipeline file today, with strict bundle-identity verification on resume and identity threaded into every audit-trail surface.

**Architecture:** A single dispatch branch in `cmd/tracker/loading.go:loadPipeline()` detects `.dipx`, opens the bundle via `dipx.Open`, converts its pre-parsed `*ir.Workflow` directly to a tracker `Graph` (skipping re-parse), and skips the filesystem subgraph walker entirely (dipx already verified closure). Bundle identity (`"sha256:" + hex(bundle.Identity())`) is threaded into `Checkpoint`, every `jsonlLogEntry` line, `RunSummary`, and `tracker.Result`. Resume verifies the checkpoint's identity against the current bundle's identity; mismatch aborts unless `--force-bundle-mismatch`.

**Tech Stack:** Go, `github.com/2389-research/dippin-lang` v0.24.0 (`dipx` package), existing tracker pipeline/engine/checkpoint infrastructure.

**Reference design:** [`docs/plans/2026-05-11-native-dipx-bundle-support-design.md`](2026-05-11-native-dipx-bundle-support-design.md)

**Branch:** Already on `feat/native-dipx-bundle-support`.

---

## Pre-flight: Confirm starting state

Run before starting:

```bash
git rev-parse --abbrev-ref HEAD   # → feat/native-dipx-bundle-support
git log --oneline -3              # → docs(plans): design ... / fix(test): ... / Merge PR #202
go build ./...                    # → no output (clean)
go test ./... -short              # → all 17 packages ok
```

If any of the above doesn't match, stop and investigate.

---

## Task 1: Bump dippin-lang to v0.24.0

**Files:**
- Modify: `go.mod`
- Modify: `go.sum`

**Step 1: Bump dependency**

```bash
go get github.com/2389-research/dippin-lang@v0.24.0
```

Expected: updates `go.mod` to `github.com/2389-research/dippin-lang v0.24.0` and refreshes `go.sum`.

**Step 2: Verify build clean**

```bash
go build ./...
```

Expected: no output. If anything breaks from v0.23→v0.24, **stop and fix in this same task** before moving on. Check `~/go/pkg/mod/github.com/2389-research/dippin-lang@v0.24.0/CHANGELOG.md` for breaking changes.

**Step 3: Verify tests clean**

```bash
go test ./... -short
```

Expected: all 17 packages `ok`. Fix any regressions in this task.

**Step 4: Verify dippin-lang version constant**

`tracker doctor` references `PinnedDippinVersion` (a CLAUDE.md gotcha — was wrong for v0.23). Search and update.

```bash
grep -rn "PinnedDippinVersion" --include="*.go" .
```

Update the constant to `"v0.24.0"`.

**Step 5: Verify with `tracker doctor`**

```bash
go run ./cmd/tracker doctor
```

Expected: any dippin-version-related lines should say `v0.24.0`.

**Step 6: Commit**

```bash
git add go.mod go.sum cmd/tracker/  # whichever files PinnedDippinVersion lives in
git commit -m "deps: bump dippin-lang v0.23.0 → v0.24.0 for .dipx support"
```

---

## Task 2: Test fixture helper `PackTestBundle`

A helper that produces real `.dipx` bundles for downstream tests. CLAUDE.md forbids mocking — every test fixture is a real packed bundle.

**Files:**
- Create: `pipeline/internal/dipxtest/pack.go`
- Create: `pipeline/internal/dipxtest/pack_test.go`

**Step 1: Write the failing test**

`pipeline/internal/dipxtest/pack_test.go`:

```go
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

// minimalDip returns a tiny valid .dip source.
func minimalDip(name, start, exit string) string {
	return `workflow ` + name + `:
  start: ` + start + `
  exit: ` + exit + `

nodes:
  ` + start + `:
    kind: codergen
  ` + exit + `:
    kind: codergen

edges:
  - from: ` + start + `
    to: ` + exit + `
`
}
```

**Step 2: Run test to verify it fails**

```bash
go test ./pipeline/internal/dipxtest/
```

Expected: FAIL with `undefined: PackTestBundle`.

**Step 3: Write minimal implementation**

`pipeline/internal/dipxtest/pack.go`:

```go
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
```

**Step 4: Run test to verify it passes**

```bash
go test ./pipeline/internal/dipxtest/
```

Expected: PASS.

**Step 5: Commit**

```bash
git add pipeline/internal/dipxtest/
git commit -m "test: PackTestBundle helper for real .dipx fixtures"
```

---

## Task 3: Split `LoadDippinWorkflow` into a shared FromIR tail

The `.dipx` loader path will reuse the validate+lint+convert tail. Split the current function so both `.dip` source and `.dipx` IR can call it.

**Files:**
- Modify: `pipeline/dippin_load.go`
- Test: `pipeline/dippin_load_test.go`

**Step 1: Write the failing test**

Add to `pipeline/dippin_load_test.go` (create the file if needed):

```go
package pipeline

import (
	"strings"
	"testing"

	"github.com/2389-research/dippin-lang/parser"
)

func TestLoadDippinWorkflowFromIR_ProducesSameGraphAsLoadDippinWorkflow(t *testing.T) {
	source := `workflow test_split:
  start: a
  exit: b

nodes:
  a:
    kind: codergen
  b:
    kind: codergen

edges:
  - from: a
    to: b
`
	const filename = "test_split.dip"

	graphViaSource, _, err := LoadDippinWorkflow(source, filename)
	if err != nil {
		t.Fatalf("LoadDippinWorkflow: %v", err)
	}

	workflow, err := parser.NewParser(source, filename).Parse()
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	graphViaIR, _, err := LoadDippinWorkflowFromIR(workflow, filename)
	if err != nil {
		t.Fatalf("LoadDippinWorkflowFromIR: %v", err)
	}

	if graphViaSource.Name != graphViaIR.Name {
		t.Errorf("graph name mismatch: source=%q ir=%q", graphViaSource.Name, graphViaIR.Name)
	}
	if graphViaSource.StartNode != graphViaIR.StartNode {
		t.Errorf("start mismatch: source=%q ir=%q", graphViaSource.StartNode, graphViaIR.StartNode)
	}
	if len(graphViaSource.Nodes) != len(graphViaIR.Nodes) {
		t.Errorf("node count: source=%d ir=%d", len(graphViaSource.Nodes), len(graphViaIR.Nodes))
	}
	if !graphViaIR.DippinValidated {
		t.Errorf("IR path did not mark DippinValidated")
	}
}

func TestLoadDippinWorkflowFromIR_NilWorkflow(t *testing.T) {
	_, _, err := LoadDippinWorkflowFromIR(nil, "x.dip")
	if err == nil || !strings.Contains(err.Error(), "nil") {
		t.Errorf("expected nil-workflow error, got: %v", err)
	}
}
```

**Step 2: Run test to verify it fails**

```bash
go test ./pipeline/ -run TestLoadDippinWorkflowFromIR
```

Expected: FAIL with `undefined: LoadDippinWorkflowFromIR`.

**Step 3: Write minimal implementation**

Replace `pipeline/dippin_load.go` with the split shape. Keep `LoadDippinWorkflow` as a façade that parses then delegates; introduce `LoadDippinWorkflowFromIR` as the shared tail:

```go
package pipeline

import (
	"fmt"

	"github.com/2389-research/dippin-lang/ir"
	"github.com/2389-research/dippin-lang/parser"
	"github.com/2389-research/dippin-lang/validator"
)

// LoadDippinWorkflow parses dippin source then delegates to
// LoadDippinWorkflowFromIR. filename is used for diagnostics.
func LoadDippinWorkflow(source, filename string) (*Graph, []validator.Diagnostic, error) {
	workflow, err := parser.NewParser(source, filename).Parse()
	if err != nil {
		return nil, nil, fmt.Errorf("parse Dippin file: %w", err)
	}
	return LoadDippinWorkflowFromIR(workflow, filename)
}

// LoadDippinWorkflowFromIR runs dippin's structural validator + linter on the
// IR workflow, then converts it to tracker's Graph. Diagnostics returned cover
// both validate and lint passes; validation errors are fatal, lint warnings
// are non-fatal. Used by both .dip source (via LoadDippinWorkflow) and .dipx
// bundle (via dipx.Open → bundle.Entry()).
func LoadDippinWorkflowFromIR(workflow *ir.Workflow, filename string) (*Graph, []validator.Diagnostic, error) {
	if workflow == nil {
		return nil, nil, fmt.Errorf("nil workflow for %s", filename)
	}

	valResult := validator.Validate(workflow)
	if valResult.HasErrors() {
		return nil, valResult.Diagnostics, fmt.Errorf("%d validation error(s) in %s", len(valResult.Errors()), filename)
	}

	lintResult := validator.Lint(workflow)

	graph, err := FromDippinIR(workflow)
	if err != nil {
		return nil, nil, fmt.Errorf("convert Dippin IR to graph: %w", err)
	}
	graph.DippinValidated = true

	var allDiags []validator.Diagnostic
	allDiags = append(allDiags, valResult.Diagnostics...)
	allDiags = append(allDiags, lintResult.Diagnostics...)
	return graph, allDiags, nil
}
```

**Step 4: Run tests to verify they pass**

```bash
go test ./pipeline/
```

Expected: all pipeline tests PASS, including the new two.

**Step 5: Verify full suite still clean**

```bash
go test ./... -short
```

Expected: 17 packages `ok`. If anything regressed, the split is wrong; investigate.

**Step 6: Commit**

```bash
git add pipeline/dippin_load.go pipeline/dippin_load_test.go
git commit -m "refactor(pipeline): split LoadDippinWorkflow into FromIR tail for .dipx reuse"
```

---

## Task 4: `pipeline.LoadDipxBundle`

The bundle loader: open the .dipx, convert entry + every subgraph IR to tracker Graphs, return `BundleInfo` with the identity hash.

**Files:**
- Create: `pipeline/dipx_load.go`
- Create: `pipeline/dipx_load_test.go`

**Step 1: Write the failing happy-path test**

`pipeline/dipx_load_test.go`:

```go
package pipeline

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/2389-research/tracker/pipeline/internal/dipxtest"
)

const minimalEntry = `workflow entry_test:
  start: a
  exit: b

nodes:
  a:
    kind: codergen
  b:
    kind: codergen

edges:
  - from: a
    to: b
`

func TestLoadDipxBundle_HappyPath(t *testing.T) {
	dir := t.TempDir()
	entryPath := filepath.Join(dir, "entry.dip")
	if err := os.WriteFile(entryPath, []byte(minimalEntry), 0o644); err != nil {
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
		t.Errorf("identity should be sha256: + 64 hex chars, got len %d", len(info.Identity))
	}
	if info.EntryPath == "" {
		t.Error("BundleInfo.EntryPath is empty")
	}
	if subgraphs == nil {
		t.Error("subgraphs map should be non-nil (even if empty)")
	}
}
```

**Step 2: Run test to verify it fails**

```bash
go test ./pipeline/ -run TestLoadDipxBundle
```

Expected: FAIL with `undefined: LoadDipxBundle`.

**Step 3: Write minimal implementation**

`pipeline/dipx_load.go`:

```go
// ABOUTME: Loads .dipx bundles produced by dippin v0.24+ — verifies hashes,
// ABOUTME: converts pre-parsed IR to tracker Graphs, returns content-addressed identity.
package pipeline

import (
	"context"
	"encoding/hex"
	"fmt"
	"os"

	"github.com/2389-research/dippin-lang/dipx"
)

// BundleInfo carries the metadata extracted from a loaded .dipx bundle.
// Identity is the canonical "sha256:<64 hex>" form of the bundle's
// content-addressed hash (SHA-256 of manifest.json bytes-as-stored).
// EntryPath is the canonical bundle-relative path of the entry workflow.
type BundleInfo struct {
	Identity  string
	EntryPath string
	Manifest  dipx.Manifest
}

// LoadDipxBundle opens a .dipx file, verifies all SHA-256 hashes via
// dipx.Open (strict mode), converts the entry workflow and every transitively-
// referenced subgraph from pre-parsed IR to tracker Graphs, and returns the
// graphs plus a BundleInfo carrying the bundle's content-addressed identity.
//
// The subgraphs map is keyed by canonical bundle path (matching manifest.Files
// entries). dipx has already verified ref closure and acyclicity, so no
// recursive walk is needed on tracker's side.
func LoadDipxBundle(ctx context.Context, path string) (*Graph, map[string]*Graph, BundleInfo, error) {
	bundle, err := dipx.Open(ctx, path)
	if err != nil {
		return nil, nil, BundleInfo{}, fmt.Errorf("load bundle %s: %w", path, err)
	}
	manifest := bundle.Manifest()

	entry := bundle.Entry()
	entryGraph, diags, err := LoadDippinWorkflowFromIR(entry, manifest.Entry)
	for _, d := range diags {
		fmt.Fprintln(os.Stderr, d.String())
	}
	if err != nil {
		return nil, nil, BundleInfo{}, fmt.Errorf("load bundle %s: entry %s: %w", path, manifest.Entry, err)
	}

	subgraphs := make(map[string]*Graph)
	for _, file := range manifest.Files {
		if file.Path == manifest.Entry {
			continue
		}
		wf, err := bundle.Lookup(file.Path)
		if err != nil {
			return nil, nil, BundleInfo{}, fmt.Errorf("load bundle %s: lookup %s: %w", path, file.Path, err)
		}
		sub, subDiags, err := LoadDippinWorkflowFromIR(wf, file.Path)
		for _, d := range subDiags {
			fmt.Fprintln(os.Stderr, d.String())
		}
		if err != nil {
			return nil, nil, BundleInfo{}, fmt.Errorf("load bundle %s: subgraph %s: %w", path, file.Path, err)
		}
		subgraphs[file.Path] = sub
	}

	id := bundle.Identity()
	info := BundleInfo{
		Identity:  "sha256:" + hex.EncodeToString(id[:]),
		EntryPath: manifest.Entry,
		Manifest:  manifest,
	}
	return entryGraph, subgraphs, info, nil
}
```

**Step 4: Run test to verify it passes**

```bash
go test ./pipeline/ -run TestLoadDipxBundle
```

Expected: PASS.

**Step 5: Add error-path tests**

Append to `pipeline/dipx_load_test.go`:

```go
func TestLoadDipxBundle_NotAValidBundle(t *testing.T) {
	// A plain .dip with .dipx extension.
	fake := filepath.Join(t.TempDir(), "bogus.dipx")
	if err := os.WriteFile(fake, []byte(minimalEntry), 0o644); err != nil {
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
	if err := os.WriteFile(entryPath, []byte(minimalEntry), 0o644); err != nil {
		t.Fatal(err)
	}
	bundlePath := dipxtest.PackTestBundle(t, entryPath)

	// Tamper one byte at a known content offset. The ZIP header is ~30 bytes
	// for the first file; flip a byte at offset 100 which sits in compressed data.
	raw, err := os.ReadFile(bundlePath)
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) < 200 {
		t.Skipf("bundle too small to tamper safely (%d bytes)", len(raw))
	}
	raw[100] ^= 0xFF
	if err := os.WriteFile(bundlePath, raw, 0o644); err != nil {
		t.Fatal(err)
	}

	_, _, _, err = LoadDipxBundle(context.Background(), bundlePath)
	if err == nil {
		t.Fatal("expected error on tampered bundle, got nil")
	}
}
```

Run:

```bash
go test ./pipeline/ -run TestLoadDipxBundle
```

Expected: all three subtests PASS.

**Step 6: Commit**

```bash
git add pipeline/dipx_load.go pipeline/dipx_load_test.go
git commit -m "feat(pipeline): LoadDipxBundle — open + verify + IR-direct convert"
```

---

## Task 5: CLI dispatch — `.dipx` branch in `loadPipeline`

Wire the new loader into the existing CLI loader chokepoint. After this task, `tracker validate foo.dipx` should work.

**Files:**
- Modify: `cmd/tracker/loading.go`
- Modify: `cmd/tracker/run.go` (loadAndValidatePipeline return shape)

**Step 1: Sketch the dispatch**

In `cmd/tracker/loading.go`:

- Add `loadDipxPipeline(filename string) (*pipeline.Graph, map[string]*pipeline.Graph, pipeline.BundleInfo, error)` — calls `pipeline.LoadDipxBundle(ctx, filename)` with `context.Background()`. (Threading caller ctx is out of scope for this task; existing `loadPipeline` doesn't accept ctx either.)
- Modify `loadPipeline(filename, formatOverride) (*pipeline.Graph, error)` — this signature is awkward for the bundle case since bundles also carry subgraphs + BundleInfo. **Don't change `loadPipeline`'s signature** (would touch every caller). Instead:
  - Add a sibling function `loadPipelineAndBundle(filename, formatOverride string) (*pipeline.Graph, map[string]*pipeline.Graph, pipeline.BundleInfo, error)` that:
    - If extension is `.dipx`: calls `loadDipxPipeline`, returns its results.
    - Otherwise: calls existing `loadPipeline`, calls existing `loadSubgraphs`, returns `(graph, subgraphs, BundleInfo{}, nil)`.
  - This becomes the new entry point for `loadAndValidatePipeline` (run.go) and any other caller that needs subgraphs/BundleInfo.
  - Existing callers of `loadPipeline` that don't care about subgraphs/bundles (validate? simulate?) keep using it unchanged — we'll update them in a later task.

**Step 2: Write the failing CLI test**

Create `cmd/tracker/loading_dipx_test.go`:

```go
package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/2389-research/tracker/pipeline/internal/dipxtest"
)

const minimalEntryDip = `workflow cli_dispatch:
  start: a
  exit: b

nodes:
  a:
    kind: codergen
  b:
    kind: codergen

edges:
  - from: a
    to: b
`

func TestLoadPipelineAndBundle_DipxDispatch(t *testing.T) {
	dir := t.TempDir()
	entry := filepath.Join(dir, "entry.dip")
	if err := os.WriteFile(entry, []byte(minimalEntryDip), 0o644); err != nil {
		t.Fatal(err)
	}
	bundlePath := dipxtest.PackTestBundle(t, entry)

	graph, subgraphs, info, err := loadPipelineAndBundle(bundlePath, "")
	if err != nil {
		t.Fatalf("loadPipelineAndBundle on .dipx: %v", err)
	}
	if graph == nil {
		t.Fatal("graph nil")
	}
	if info.Identity == "" {
		t.Error("BundleInfo.Identity empty on .dipx path")
	}
	_ = subgraphs
	_ = context.Background()
}

func TestLoadPipelineAndBundle_DipPath(t *testing.T) {
	dir := t.TempDir()
	entry := filepath.Join(dir, "entry.dip")
	if err := os.WriteFile(entry, []byte(minimalEntryDip), 0o644); err != nil {
		t.Fatal(err)
	}

	graph, subgraphs, info, err := loadPipelineAndBundle(entry, "")
	if err != nil {
		t.Fatalf("loadPipelineAndBundle on .dip: %v", err)
	}
	if graph == nil {
		t.Fatal("graph nil")
	}
	if info.Identity != "" {
		t.Errorf("BundleInfo.Identity should be empty on .dip, got %q", info.Identity)
	}
	_ = subgraphs
}
```

**Step 3: Run test to verify it fails**

```bash
go test ./cmd/tracker/ -run TestLoadPipelineAndBundle
```

Expected: FAIL with `undefined: loadPipelineAndBundle`.

**Step 4: Implement**

Append to `cmd/tracker/loading.go`:

```go
// loadDipxPipeline reads a .dipx bundle, verifies hashes, and converts the
// entry + every transitively-referenced workflow to tracker Graphs.
func loadDipxPipeline(filename string) (*pipeline.Graph, map[string]*pipeline.Graph, pipeline.BundleInfo, error) {
	return pipeline.LoadDipxBundle(context.Background(), filename)
}

// loadPipelineAndBundle is the loader entry point that handles both .dip
// (filesystem + recursive subgraph walker) and .dipx (sealed bundle, pre-
// resolved subgraphs). Always returns the subgraphs map and BundleInfo;
// .dip callers see an empty BundleInfo and a subgraph map populated from
// disk, while .dipx callers see a populated BundleInfo and subgraphs
// pre-resolved by dipx.
func loadPipelineAndBundle(filename, formatOverride string) (*pipeline.Graph, map[string]*pipeline.Graph, pipeline.BundleInfo, error) {
	if strings.EqualFold(filepath.Ext(filename), ".dipx") {
		return loadDipxPipeline(filename)
	}
	graph, err := loadPipeline(filename, formatOverride)
	if err != nil {
		return nil, nil, pipeline.BundleInfo{}, err
	}
	subgraphs, err := loadSubgraphs(graph, filename)
	if err != nil {
		return nil, nil, pipeline.BundleInfo{}, err
	}
	return graph, subgraphs, pipeline.BundleInfo{}, nil
}
```

Add `"context"` to the import list at top of file if not already present.

**Step 5: Run test to verify it passes**

```bash
go test ./cmd/tracker/ -run TestLoadPipelineAndBundle
```

Expected: both subtests PASS.

**Step 6: Migrate `loadAndValidatePipeline` to the new entry point**

In `cmd/tracker/run.go`, find `loadAndValidatePipeline` (around line 314-325 per design). Change its return signature to include `pipeline.BundleInfo` and migrate it to call `loadPipelineAndBundle` instead of the old `loadPipeline + loadSubgraphs`. Update all callers in `run.go` (and any other call sites).

```go
func loadAndValidatePipeline(cfg *cliConfig) (*pipeline.Graph, map[string]*pipeline.Graph, pipeline.BundleInfo, error) {
	resolved, _, _, err := resolvePipelineSource(cfg.pipelineFile)
	if err != nil {
		return nil, nil, pipeline.BundleInfo{}, err
	}
	graph, subgraphs, info, err := loadPipelineAndBundle(resolved, cfg.format)
	if err != nil {
		return nil, nil, pipeline.BundleInfo{}, err
	}
	if err := validateSubgraphRefs(graph, subgraphs); err != nil {
		return nil, nil, pipeline.BundleInfo{}, err
	}
	return graph, subgraphs, info, nil
}
```

(Adapt to the actual current shape of `loadAndValidatePipeline` — the design refers to `cmd/tracker/run.go:314-325`; verify exact lines before editing.)

Update every caller in run.go to take the extra return.

**Step 7: Run full test suite**

```bash
go test ./... -short
```

Expected: 17 packages `ok`. If callers were missed, fix them.

**Step 8: Smoke-test the CLI**

```bash
# Pack a real bundle from an existing example .dip
go install github.com/2389-research/dippin-lang/cmd/dippin@v0.24.0  # only if needed; or use local checkout
# (or use the test fixture path)

# Or simpler: write a tiny .dip and pack via dipx programmatically. Use this Go one-liner:
cat <<EOF > /tmp/smoke.dip
workflow smoke:
  start: a
  exit: b

nodes:
  a:
    kind: codergen
  b:
    kind: codergen

edges:
  - from: a
    to: b
EOF

go run -tags=ignore - <<EOF
package main
import (
    "context"; "os"
    "github.com/2389-research/dippin-lang/dipx"
)
func main() {
    f, _ := os.Create("/tmp/smoke.dipx")
    defer f.Close()
    if _, err := dipx.Pack(context.Background(), "/tmp/smoke.dip", f); err != nil {
        panic(err)
    }
}
EOF

go run ./cmd/tracker validate /tmp/smoke.dipx
```

Expected: validate succeeds (no error), exit code 0. (Don't worry if the printed line doesn't yet show identity — that's Task 9.)

**Step 9: Commit**

```bash
git add cmd/tracker/loading.go cmd/tracker/loading_dipx_test.go cmd/tracker/run.go
git commit -m "feat(cli): .dipx dispatch in pipeline loader entry point"
```

---

## Task 6: `Checkpoint.BundleIdentity` field

**Files:**
- Modify: `pipeline/checkpoint.go`
- Test: `pipeline/checkpoint_test.go`

**Step 1: Write the failing test**

Add to `pipeline/checkpoint_test.go`:

```go
func TestCheckpoint_BundleIdentity_Roundtrip(t *testing.T) {
	cp := &Checkpoint{
		RunID:          "test-run",
		BundleIdentity: "sha256:efb5648d28e6c250dfad5411651d427f4f62ca24e185ce6cfc51478a4c6711ab",
	}
	path := filepath.Join(t.TempDir(), "cp.json")
	if err := SaveCheckpoint(path, cp); err != nil {
		t.Fatalf("SaveCheckpoint: %v", err)
	}
	loaded, err := LoadCheckpoint(path)
	if err != nil {
		t.Fatalf("LoadCheckpoint: %v", err)
	}
	if loaded.BundleIdentity != cp.BundleIdentity {
		t.Errorf("BundleIdentity not preserved: got %q want %q", loaded.BundleIdentity, cp.BundleIdentity)
	}
}

func TestCheckpoint_BundleIdentity_BackwardCompat(t *testing.T) {
	// Old-format JSON without bundle_identity should load with empty string.
	path := filepath.Join(t.TempDir(), "old.json")
	old := `{"run_id":"old-run","current_node":"a","completed_nodes":["start"],"context":{},"restart_count":0,"timestamp":"2026-05-01T00:00:00Z"}`
	if err := os.WriteFile(path, []byte(old), 0o644); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadCheckpoint(path)
	if err != nil {
		t.Fatalf("LoadCheckpoint: %v", err)
	}
	if loaded.BundleIdentity != "" {
		t.Errorf("expected empty BundleIdentity on old checkpoint, got %q", loaded.BundleIdentity)
	}
}
```

**Step 2: Run test to verify it fails**

```bash
go test ./pipeline/ -run TestCheckpoint_BundleIdentity
```

Expected: FAIL with `unknown field 'BundleIdentity' in struct literal`.

**Step 3: Implement**

Edit `pipeline/checkpoint.go` to add the field on the `Checkpoint` struct:

```go
// BundleIdentity is the content-addressed identity of the .dipx bundle the
// run was started against ("sha256:<hex>"). Empty for runs started from a
// plain .dip file. Used for strict resume verification.
BundleIdentity string `json:"bundle_identity,omitempty"`
```

**Step 4: Run tests to verify they pass**

```bash
go test ./pipeline/ -run TestCheckpoint
```

Expected: PASS.

**Step 5: Commit**

```bash
git add pipeline/checkpoint.go pipeline/checkpoint_test.go
git commit -m "feat(pipeline): Checkpoint.BundleIdentity for resume verification"
```

---

## Task 7: `PipelineEvent.BundleIdentity` + engine option

Bundle identity stamped on every event the engine emits.

**Files:**
- Modify: `pipeline/events.go` (PipelineEvent struct)
- Modify: `pipeline/events_jsonl.go` (jsonlLogEntry + buildLogEntry)
- Modify: `pipeline/engine.go` (WithBundleIdentity option)
- Test: `pipeline/events_test.go` (or events_jsonl_test.go)

**Step 1: Write the failing test**

Add to the appropriate events test file (find existing event test, follow its setup):

```go
func TestPipelineEvent_BundleIdentity_FlowsToJSONL(t *testing.T) {
	evt := PipelineEvent{
		Source:         "engine",
		Type:           EventPipelineStarted,
		RunID:          "test",
		BundleIdentity: "sha256:efb5648d28e6c2",
	}
	entry := buildLogEntry(evt)
	if entry.BundleIdentity != "sha256:efb5648d28e6c2" {
		t.Errorf("BundleIdentity not copied to jsonlLogEntry: %q", entry.BundleIdentity)
	}
}
```

**Step 2: Run test to verify it fails**

```bash
go test ./pipeline/ -run TestPipelineEvent_BundleIdentity
```

Expected: FAIL — field missing.

**Step 3: Implement**

- `pipeline/events.go`: add `BundleIdentity string` to `PipelineEvent`.
- `pipeline/events_jsonl.go`: add `BundleIdentity string \`json:"bundle_identity,omitempty"\`` to `jsonlLogEntry`, and in `buildLogEntry` copy `evt.BundleIdentity`.
- `pipeline/engine.go`: add a private `bundleIdentity string` field on the engine, an option:

```go
// WithBundleIdentity stamps every PipelineEvent the engine emits with the
// given identity string. Used to thread .dipx bundle identity into the
// activity log. Empty string (the default) is a no-op.
func WithBundleIdentity(id string) Option {
	return func(e *Engine) { e.bundleIdentity = id }
}
```

- In the engine's event emission helper (find where it constructs `PipelineEvent` values — likely a method like `emitEvent` or in the engine's run loop), set `evt.BundleIdentity = e.bundleIdentity` before emitting.

**Step 4: Run tests to verify they pass**

```bash
go test ./pipeline/
```

Expected: all PASS.

**Step 5: Commit**

```bash
git add pipeline/events.go pipeline/events_jsonl.go pipeline/engine.go pipeline/events_test.go
git commit -m "feat(pipeline): bundle identity on every PipelineEvent and JSONL entry"
```

---

## Task 8: Persist `BundleIdentity` into checkpoint on save

The engine knows the bundle identity (via `WithBundleIdentity`). The checkpoint writer needs to write it.

**Files:**
- Modify: `pipeline/engine.go` (wherever it constructs the Checkpoint to save)

**Step 1: Locate checkpoint construction**

```bash
grep -n "Checkpoint{" pipeline/engine.go pipeline/*.go
```

Find the spot where the engine builds the `*Checkpoint` it hands to `SaveCheckpoint`.

**Step 2: Write the failing test**

Add an integration-flavored test (in `pipeline/engine_test.go` or wherever engine integration tests live — find the existing pattern). Smallest possible engine run with `WithBundleIdentity("sha256:abc"))` and `WithCheckpointPath(...)`, then load the checkpoint and verify the identity field.

```go
func TestEngine_PersistsBundleIdentityToCheckpoint(t *testing.T) {
	graph := minimalTwoNodeGraph(t) // helper that returns a trivial Graph
	checkpointPath := filepath.Join(t.TempDir(), "cp.json")
	engine := NewEngine(graph,
		WithCheckpointPath(checkpointPath),
		WithBundleIdentity("sha256:bundleid"),
	)
	// Drive a single tick or run-to-completion via whatever helper exists.
	// Look at existing engine tests for the pattern.
	_ = engine.Run(context.Background())

	cp, err := LoadCheckpoint(checkpointPath)
	if err != nil {
		t.Fatalf("LoadCheckpoint: %v", err)
	}
	if cp.BundleIdentity != "sha256:bundleid" {
		t.Errorf("BundleIdentity not persisted: got %q", cp.BundleIdentity)
	}
}
```

(If `minimalTwoNodeGraph` doesn't exist, follow the pattern in existing engine tests to build one inline.)

**Step 3: Run test to verify it fails**

```bash
go test ./pipeline/ -run TestEngine_PersistsBundleIdentityToCheckpoint
```

Expected: FAIL.

**Step 4: Implement**

In the engine's checkpoint construction (located in Step 1), set `cp.BundleIdentity = e.bundleIdentity`.

**Step 5: Run test to verify it passes**

```bash
go test ./pipeline/ -run TestEngine_PersistsBundleIdentityToCheckpoint
```

Expected: PASS.

**Step 6: Commit**

```bash
git add pipeline/engine.go pipeline/engine_test.go
git commit -m "feat(pipeline): engine persists bundle identity into checkpoint"
```

---

## Task 9: Thread `BundleInfo` through `Config` and into the engine

Wire the BundleInfo from `loadAndValidatePipeline` to the engine constructor.

**Files:**
- Modify: `tracker.go` (Config struct)
- Modify: `cmd/tracker/run.go` (wiring from cliConfig → engine)
- Modify: `tracker.go` or wherever `tracker.Run` constructs the engine

**Step 1: Add Config field**

In `tracker.go`, add to `Config`:

```go
// BundleIdentity is the content-addressed identity of the .dipx bundle
// the pipeline was loaded from ("sha256:<hex>"). Empty for runs from a
// plain .dip file. Stamped onto every emitted PipelineEvent and persisted
// to the checkpoint for resume verification.
BundleIdentity string
```

**Step 2: Thread into engine construction**

Where `tracker.Run` (or equivalent) builds the engine, pass `WithBundleIdentity(cfg.BundleIdentity)`. Match the existing option-passing pattern.

**Step 3: Set `Config.BundleIdentity` from the loader**

In `cmd/tracker/run.go`, after `loadAndValidatePipeline` returns `BundleInfo`, set `trackerCfg.BundleIdentity = info.Identity` before invoking `tracker.Run`. Find every call site of `tracker.Run` from the CLI and add this.

**Step 4: Add a regression test**

Pick the most CLI-shaped integration test (or write one in `cmd/tracker/run_test.go` modeled after existing ones). Run a `.dipx` end-to-end; after completion, load the checkpoint and assert `BundleIdentity` is non-empty.

```go
func TestRun_DipxStampsBundleIdentityIntoCheckpoint(t *testing.T) {
	dir := t.TempDir()
	entry := filepath.Join(dir, "entry.dip")
	if err := os.WriteFile(entry, []byte(minimalEntryDip), 0o644); err != nil {
		t.Fatal(err)
	}
	bundlePath := dipxtest.PackTestBundle(t, entry)

	workdir := t.TempDir()
	// Drive a minimal run with --autopilot lax (or whatever non-interactive flag exists).
	// Follow existing run tests for the exact invocation shape.
	// ...
	// After run completes, find the run dir's checkpoint.json and read it.
	// Assert checkpoint.BundleIdentity is non-empty and starts with "sha256:".
}
```

(If full end-to-end via CLI is too heavy here, defer to Task 15 and just verify the wiring with a unit-shaped test that exercises `tracker.Run` with a hand-built `Config{BundleIdentity: "..."}` and confirms the checkpoint comes out right.)

**Step 5: Run tests**

```bash
go test ./... -short
```

Expected: 17 packages `ok`.

**Step 6: Commit**

```bash
git add tracker.go cmd/tracker/run.go cmd/tracker/run_test.go
git commit -m "feat(cli): plumb BundleInfo from loader to engine via Config"
```

---

## Task 10: `tracker.Result.BundleIdentity`

The library API's run-completion record carries identity for embedded callers.

**Files:**
- Modify: `tracker.go` (Result struct + populate at run completion)
- Test: alongside existing Result tests

**Step 1: Write the failing test**

Find existing tests for `tracker.Result` (likely in `tracker_test.go`). Add:

```go
func TestResult_BundleIdentity_PopulatedFromConfig(t *testing.T) {
	// Use whatever minimal setup existing tracker.Run tests use.
	// Run with Config.BundleIdentity = "sha256:test", then assert
	// result.BundleIdentity == "sha256:test".
}
```

**Step 2: Run test to verify it fails**

**Step 3: Implement**

- Add `BundleIdentity string` to `tracker.Result`.
- In `tracker.Run`, after engine completes, set `result.BundleIdentity = cfg.BundleIdentity` before returning.

**Step 4: Run test to verify it passes**

**Step 5: Commit**

```bash
git add tracker.go tracker_test.go
git commit -m "feat(api): tracker.Result.BundleIdentity for library callers"
```

---

## Task 11: `RunSummary.BundleIdentity` + read from checkpoint

`tracker list` reads checkpoints; surface identity in the summary.

**Files:**
- Modify: `tracker_audit.go` (RunSummary struct + ListRuns reader)
- Test: `tracker_audit_test.go`

**Step 1: Write the failing test**

```go
func TestListRuns_PopulatesBundleIdentity(t *testing.T) {
	workdir := t.TempDir()
	runDir := filepath.Join(workdir, ".tracker", "runs", "test-run-1")
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatal(err)
	}
	cp := &pipeline.Checkpoint{
		RunID:          "test-run-1",
		BundleIdentity: "sha256:abc123",
		Timestamp:      time.Now(),
	}
	if err := pipeline.SaveCheckpoint(filepath.Join(runDir, "checkpoint.json"), cp); err != nil {
		t.Fatal(err)
	}

	runs, err := ListRuns(workdir, AuditConfig{LogWriter: io.Discard})
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 1 {
		t.Fatalf("want 1 run, got %d", len(runs))
	}
	if runs[0].BundleIdentity != "sha256:abc123" {
		t.Errorf("BundleIdentity not populated: %q", runs[0].BundleIdentity)
	}
}
```

**Step 2: Run test to verify it fails**

```bash
go test . -run TestListRuns_PopulatesBundleIdentity
```

Expected: FAIL — field missing.

**Step 3: Implement**

- Add `BundleIdentity string \`json:"bundle_identity,omitempty"\`` to `RunSummary`.
- In the `ListRuns` summary builder, copy `checkpoint.BundleIdentity` into the summary.

**Step 4: Run test to verify it passes**

**Step 5: Commit**

```bash
git add tracker_audit.go tracker_audit_test.go
git commit -m "feat(api): RunSummary.BundleIdentity for tracker list"
```

---

## Task 12: `tracker list` Bundle column

User-visible CLI output gains the Bundle column.

**Files:**
- Modify: `cmd/tracker/audit.go` (printRunList)
- Test: `cmd/tracker/audit_test.go`

**Step 1: Write the failing test**

Find existing `printRunList` test pattern in `audit_test.go`. Add a test where one run has identity and one doesn't, capture stdout, assert the truncated hash appears for the .dipx run and is blank for the .dip run.

```go
func TestPrintRunList_BundleColumn(t *testing.T) {
	runs := []tracker.RunSummary{
		{RunID: "aaaaaaaa", Status: "success", Nodes: 1, BundleIdentity: "sha256:efb5648d28e6c250dfad5411651d427f4f62ca24e185ce6cfc51478a4c6711ab"},
		{RunID: "bbbbbbbb", Status: "success", Nodes: 1, BundleIdentity: ""},
	}
	out := captureStdout(t, func() { printRunList(runs) })
	if !strings.Contains(out, "sha256:efb5648d28e6c2") {
		t.Errorf("truncated bundle hash should appear in output:\n%s", out)
	}
	if strings.Contains(out, "sha256:") && !strings.Contains(out, "sha256:efb5648d28e6c2") {
		// fine — sha256: only appears once
	}
	if !strings.Contains(out, "Bundle") {
		t.Errorf("Bundle header missing:\n%s", out)
	}
}
```

`captureStdout` helper — if not present, write one inline that redirects os.Stdout via `os.Pipe`.

**Step 2: Run test to verify it fails**

**Step 3: Implement**

In `cmd/tracker/audit.go:printRunList`:

- Add `"Bundle"` to the header row and the divider row.
- Add a helper:

```go
func truncateBundleIdentity(id string) string {
	if id == "" {
		return ""
	}
	const prefix = "sha256:"
	if !strings.HasPrefix(id, prefix) {
		return id
	}
	hex := id[len(prefix):]
	if len(hex) <= 16 {
		return id
	}
	return prefix + hex[:16] + "..."
}
```

- For each row, append the truncated identity.

Adjust column widths in the existing `Printf` format strings to accommodate.

**Step 4: Run test to verify it passes**

**Step 5: Commit**

```bash
git add cmd/tracker/audit.go cmd/tracker/audit_test.go
git commit -m "feat(cli): Bundle column in tracker list"
```

---

## Task 13: `tracker audit` Bundle header line

Single-run audit gets a `Bundle:` line in the header block.

**Files:**
- Modify: `cmd/tracker/audit.go` (printAuditHeader)
- Modify: `tracker_audit.go` (AuditReport struct, if BundleIdentity not already there — it should propagate from Checkpoint)
- Test: `cmd/tracker/audit_test.go`

**Step 1: Add BundleIdentity to AuditReport**

If not already populated, add `BundleIdentity string \`json:"bundle_identity,omitempty"\`` to `tracker.AuditReport` and copy it from the checkpoint in `Audit()`.

**Step 2: Write the failing test**

```go
func TestPrintAuditHeader_BundleLine(t *testing.T) {
	r := &tracker.AuditReport{
		RunID:          "test",
		Status:         "success",
		BundleIdentity: "sha256:efb5648d28e6c2",
	}
	out := captureStdout(t, func() { printAuditHeader(r) })
	if !strings.Contains(out, "Bundle:") {
		t.Errorf("Bundle: line missing:\n%s", out)
	}
	if !strings.Contains(out, "sha256:efb5648d28e6c2") {
		t.Errorf("identity not in header:\n%s", out)
	}
}

func TestPrintAuditHeader_NoBundleLine_WhenIdentityEmpty(t *testing.T) {
	r := &tracker.AuditReport{RunID: "test", Status: "success"}
	out := captureStdout(t, func() { printAuditHeader(r) })
	if strings.Contains(out, "Bundle:") {
		t.Errorf("Bundle: line should not appear when identity empty:\n%s", out)
	}
}
```

**Step 3: Run test to verify it fails**

**Step 4: Implement**

In `printAuditHeader`:

```go
if r.BundleIdentity != "" {
	fmt.Printf("  Bundle:    %s\n", r.BundleIdentity)
}
```

**Step 5: Run tests to verify they pass**

**Step 6: Commit**

```bash
git add cmd/tracker/audit.go tracker_audit.go cmd/tracker/audit_test.go
git commit -m "feat(cli): Bundle header in tracker audit"
```

---

## Task 14: `--force-bundle-mismatch` flag

The escape hatch for the strict resume check.

**Files:**
- Modify: `cmd/tracker/flags.go`
- Modify: `cmd/tracker/run.go` (cliConfig + flag wiring)

**Step 1: Add the flag**

In `cmd/tracker/flags.go`, find where existing bool flags are defined and add:

```go
flag.BoolVar(&cfg.forceBundleMismatch, "force-bundle-mismatch", false,
	"allow resume even when the bundle's content-addressed identity differs from the original run")
```

In `cliConfig` struct (find it — likely `cmd/tracker/main.go` or `cmd/tracker/flags.go`), add:

```go
forceBundleMismatch bool
```

**Step 2: Verify flag parses**

```bash
go run ./cmd/tracker --help 2>&1 | grep force-bundle-mismatch
```

Expected: flag appears in help output with description.

**Step 3: Commit**

```bash
git add cmd/tracker/flags.go cmd/tracker/main.go cmd/tracker/run.go  # whichever held cliConfig
git commit -m "feat(cli): --force-bundle-mismatch flag (no behavior yet)"
```

---

## Task 15: Resume-time identity verification

The strict-mismatch policy: identity must match unless `--force-bundle-mismatch`.

**Files:**
- Modify: `cmd/tracker/commands.go` (resolveRunCheckpoint or wherever resume wires up)
- Modify: `pipeline/events.go` (new event type `EventBundleMismatchForced`)
- Test: `cmd/tracker/commands_test.go` or new `cmd/tracker/resume_dipx_test.go`

**Step 1: Add the event type constant**

In `pipeline/events.go`, alongside existing event constants:

```go
EventBundleMismatchForced EventType = "bundle_mismatch_forced"
```

**Step 2: Write the failing tests**

Create `cmd/tracker/resume_dipx_test.go`:

```go
package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/2389-research/tracker/pipeline"
	"github.com/2389-research/tracker/pipeline/internal/dipxtest"
)

func writeFakeCheckpoint(t *testing.T, runDir, identity string) string {
	t.Helper()
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatal(err)
	}
	cp := &pipeline.Checkpoint{
		RunID:          filepath.Base(runDir),
		BundleIdentity: identity,
	}
	path := filepath.Join(runDir, "checkpoint.json")
	if err := pipeline.SaveCheckpoint(path, cp); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestVerifyResumeBundle_MatchesIdentity(t *testing.T) {
	dir := t.TempDir()
	entry := filepath.Join(dir, "entry.dip")
	if err := os.WriteFile(entry, []byte(minimalEntryDip), 0o644); err != nil {
		t.Fatal(err)
	}
	bundlePath := dipxtest.PackTestBundle(t, entry)
	_, _, info, err := pipeline.LoadDipxBundle(context.Background(), bundlePath)
	if err != nil {
		t.Fatal(err)
	}

	err = verifyResumeBundle(info.Identity, info.Identity, false)
	if err != nil {
		t.Errorf("matching identities should pass: %v", err)
	}
}

func TestVerifyResumeBundle_MismatchAbortsByDefault(t *testing.T) {
	err := verifyResumeBundle(
		"sha256:"+strings.Repeat("a", 64),
		"sha256:"+strings.Repeat("b", 64),
		false,
	)
	if err == nil {
		t.Fatal("expected error on identity mismatch, got nil")
	}
	if !errors.Is(err, errBundleIdentityMismatch) {
		t.Errorf("expected errBundleIdentityMismatch, got %v", err)
	}
	if !strings.Contains(err.Error(), "force-bundle-mismatch") {
		t.Errorf("error should mention --force-bundle-mismatch: %v", err)
	}
}

func TestVerifyResumeBundle_MismatchAllowedWithForce(t *testing.T) {
	err := verifyResumeBundle(
		"sha256:"+strings.Repeat("a", 64),
		"sha256:"+strings.Repeat("b", 64),
		true,
	)
	if err != nil {
		t.Errorf("--force-bundle-mismatch should allow mismatch: %v", err)
	}
}

func TestVerifyResumeBundle_DowngradeRejected(t *testing.T) {
	// Checkpoint has identity, current bundle has none (.dip resume).
	err := verifyResumeBundle("sha256:"+strings.Repeat("a", 64), "", false)
	if err == nil {
		t.Error("expected downgrade rejection")
	}
}

func TestVerifyResumeBundle_UpgradeRejected(t *testing.T) {
	// Checkpoint has no identity, current bundle has one (.dipx resume).
	err := verifyResumeBundle("", "sha256:"+strings.Repeat("a", 64), false)
	if err == nil {
		t.Error("expected upgrade rejection")
	}
}

func TestVerifyResumeBundle_NeitherSideHasIdentity(t *testing.T) {
	// Pure .dip → .dip resume — existing behavior, never errors.
	err := verifyResumeBundle("", "", false)
	if err != nil {
		t.Errorf("no-identity-either-side should pass unchanged: %v", err)
	}
}

func randomHex(t *testing.T, n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(b)
}
```

**Step 3: Run tests to verify they fail**

```bash
go test ./cmd/tracker/ -run TestVerifyResumeBundle
```

Expected: FAIL — `verifyResumeBundle` and `errBundleIdentityMismatch` undefined.

**Step 4: Implement**

In `cmd/tracker/commands.go` (or a new `cmd/tracker/resume_bundle.go`):

```go
package main

import (
	"errors"
	"fmt"
)

var errBundleIdentityMismatch = errors.New("bundle identity mismatch on resume")

// verifyResumeBundle checks the checkpoint's stored bundle identity against
// the current bundle's identity. Returns nil if they match (or if force is
// true). Any difference — including empty-on-one-side — is a mismatch.
//
// The empty-vs-empty case (resume a .dip-started run against a .dip) is the
// only no-identity-change case that's silently allowed; it preserves existing
// behavior for plain .dip workflows.
func verifyResumeBundle(checkpointIdentity, currentIdentity string, force bool) error {
	if checkpointIdentity == currentIdentity {
		return nil
	}
	if force {
		return nil
	}
	return fmt.Errorf("%w\n  run was started against: %s\n  current bundle:          %s\nThe pipeline source has changed since this run was started. To resume against a different bundle, pass --force-bundle-mismatch.",
		errBundleIdentityMismatch,
		displayIdentity(checkpointIdentity),
		displayIdentity(currentIdentity),
	)
}

func displayIdentity(id string) string {
	if id == "" {
		return "(none — plain .dip)"
	}
	return id
}
```

**Step 5: Run tests to verify they pass**

```bash
go test ./cmd/tracker/ -run TestVerifyResumeBundle
```

Expected: all PASS.

**Step 6: Wire into the resume flow**

Find `resolveRunCheckpoint` (per the design: `cmd/tracker/commands.go:358-397`). After loading the checkpoint and before returning, if the pipeline source is a `.dipx`:

1. Compute the current bundle's identity (via `pipeline.LoadDipxBundle` or just `dipx.Open` since we don't need the graph at this stage — but loadAndValidatePipeline will need the graph too; if cheaper, just call `dipx.Open` here and pass the identity along, OR accept the slight duplication of opening the bundle twice).
2. Call `verifyResumeBundle(cp.BundleIdentity, currentIdentity, cfg.forceBundleMismatch)`.
3. If `force` was used and mismatch happened, emit the `EventBundleMismatchForced` event. (Engine isn't constructed yet — defer the emit to after engine starts. Stash the fact in `cliConfig.bundleMismatchForcedFrom = cp.BundleIdentity` so the engine's first emit can include it.)

(Implementation of step 3 — the deferred event — is detail-fiddly. If it's too invasive in this task, emit it directly to a stderr warning here and add the activity.jsonl emission in a tiny follow-up task.)

**Step 7: Add a CLI-level integration test for the abort path**

```go
func TestResume_AbortsOnBundleMismatch(t *testing.T) {
	// 1. Pack bundle A. Start a run, write its checkpoint with bundle A's identity.
	// 2. Pack bundle B (different source). Invoke tracker -r <runID> bundleB.dipx.
	// 3. Assert the command errors with "bundle identity mismatch".
	// 4. Re-invoke with --force-bundle-mismatch. Assert it proceeds (or at least gets
	//    past the verification step).
}
```

(Sketch only — implement to whatever level matches the existing CLI test infrastructure.)

**Step 8: Run full suite**

```bash
go test ./... -short
```

Expected: 17 packages `ok`.

**Step 9: Commit**

```bash
git add cmd/tracker/commands.go cmd/tracker/resume_bundle.go cmd/tracker/resume_dipx_test.go pipeline/events.go
git commit -m "feat(cli): strict bundle-identity verification on resume"
```

---

## Task 16: End-to-end smoke test

A real `.dipx` run through the CLI, verified across every audit surface.

**Files:**
- Create: `cmd/tracker/dipx_e2e_test.go` (or extend an existing integration test)

**Step 1: Write the integration test**

```go
package main

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tracker "github.com/2389-research/tracker"
	"github.com/2389-research/tracker/pipeline"
	"github.com/2389-research/tracker/pipeline/internal/dipxtest"
)

func TestE2E_DipxIdentityFlowsThroughAllAuditSurfaces(t *testing.T) {
	// 1. Pack a real .dipx from a minimal .dip.
	dir := t.TempDir()
	entry := filepath.Join(dir, "entry.dip")
	if err := os.WriteFile(entry, []byte(minimalEntryDip), 0o644); err != nil {
		t.Fatal(err)
	}
	bundlePath := dipxtest.PackTestBundle(t, entry)

	// 2. Compute the expected identity directly via LoadDipxBundle.
	_, _, info, err := pipeline.LoadDipxBundle(context.Background(), bundlePath)
	if err != nil {
		t.Fatal(err)
	}
	wantIdentity := info.Identity

	// 3. Run a pipeline against the bundle via tracker.Run (library API).
	workdir := t.TempDir()
	cfg := tracker.Config{
		// fill in whatever the minimal Config fields are; copy from existing tracker.Run tests
		PipelineFile: bundlePath, // or whatever field name
		Workdir:      workdir,
		// any non-interactive options (autopilot lax, etc.)
	}
	result, err := tracker.Run(context.Background(), cfg)
	if err != nil {
		t.Fatalf("tracker.Run: %v", err)
	}

	// 4. Assert tracker.Result.BundleIdentity.
	if result.BundleIdentity != wantIdentity {
		t.Errorf("Result.BundleIdentity: want %q got %q", wantIdentity, result.BundleIdentity)
	}

	// 5. Assert checkpoint persisted the identity.
	cp, err := pipeline.LoadCheckpoint(filepath.Join(result.ArtifactRunDir, "checkpoint.json"))
	if err != nil {
		t.Fatal(err)
	}
	if cp.BundleIdentity != wantIdentity {
		t.Errorf("Checkpoint.BundleIdentity: want %q got %q", wantIdentity, cp.BundleIdentity)
	}

	// 6. Assert every activity.jsonl line carries the identity.
	logBytes, err := os.ReadFile(filepath.Join(result.ArtifactRunDir, "activity.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(logBytes)), "\n")
	for i, line := range lines {
		var entry map[string]any
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			t.Fatalf("line %d not valid JSON: %v", i, err)
		}
		if entry["bundle_identity"] != wantIdentity {
			t.Errorf("line %d missing bundle_identity (got %v)", i, entry["bundle_identity"])
		}
	}

	// 7. Assert RunSummary via ListRuns.
	runs, err := tracker.ListRuns(workdir, tracker.AuditConfig{LogWriter: io.Discard})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, r := range runs {
		if r.RunID == result.RunID && r.BundleIdentity == wantIdentity {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("RunSummary with matching identity not found among %v", runs)
	}
}
```

(Adapt `tracker.Config` field names and `tracker.Run` invocation shape to match the existing API. Find a working invocation in existing tests first.)

**Step 2: Run test**

```bash
go test ./cmd/tracker/ -run TestE2E_DipxIdentityFlowsThroughAllAuditSurfaces -v
```

Expected: PASS.

**Step 3: Run full suite**

```bash
go test ./... -short
```

Expected: 17 packages `ok`.

**Step 4: Manual smoke test via CLI**

```bash
# (Use the smoke fixture from Task 5 or repack as needed.)
go run ./cmd/tracker validate /tmp/smoke.dipx
go run ./cmd/tracker simulate /tmp/smoke.dipx
go run ./cmd/tracker run /tmp/smoke.dipx --autopilot lax  # to a fresh workdir
go run ./cmd/tracker list
# Expect Bundle column with truncated sha256:...
go run ./cmd/tracker audit <runID-from-list>
# Expect Bundle: sha256:... header line
```

**Step 5: Commit**

```bash
git add cmd/tracker/dipx_e2e_test.go
git commit -m "test: end-to-end .dipx run verifies identity across all audit surfaces"
```

---

## Task 17: CHANGELOG entry

Per CLAUDE.md: "Keep CHANGELOG.md updated with every feature... Update the changelog in the same PR as the code change, not after."

**Files:**
- Modify: `CHANGELOG.md`

**Step 1: Add the entry**

Under `## [Unreleased]`, add:

```markdown
### Added

- **Native `.dipx` bundle support** (closes [request]). Tracker accepts content-addressed `.dipx` bundles (produced by `dippin pack`) anywhere it accepts a pipeline file: `tracker validate`, `tracker simulate`, `tracker run`, and `tracker -r <runID>` resume. Bundle identity (`sha256:<hex>`) is stamped onto every `activity.jsonl` line, persisted into the checkpoint, surfaced in `tracker list` (new `Bundle` column) and `tracker audit` (new `Bundle:` header), and exposed on `tracker.Result.BundleIdentity` for embedded library callers. Resume against a `.dipx` strictly verifies the identity matches the original run — mismatch aborts with both hashes shown; `--force-bundle-mismatch` is the escape hatch. Loader uses dipx's pre-parsed `*ir.Workflow` directly (no re-parse of bundled sources), and bypasses the filesystem subgraph walker entirely (dipx already verified ref closure + acyclicity on `Open`).

### Changed

- **dippin-lang dependency bumped v0.23.0 → v0.24.0** for the `dipx` package. `PinnedDippinVersion` updated to match (so `tracker doctor` no longer suggests an outdated version).
```

**Step 2: Verify formatting**

```bash
git diff CHANGELOG.md
```

Spot-check the entry matches the existing prose style (full sentences, technical detail, links to issues/PRs).

**Step 3: Commit**

```bash
git add CHANGELOG.md
git commit -m "docs: changelog entry for native .dipx bundle support"
```

---

## Final verification

Before opening a PR, run the full gate from CLAUDE.md:

```bash
go build ./...
go test ./... -short
dippin doctor examples/ask_and_execute.dip examples/build_product.dip examples/build_product_with_superspec.dip
```

Expected:
- Build clean.
- All 17 packages `ok`.
- All three examples score A.

Then:

```bash
git log --oneline feat/native-dipx-bundle-support ^main
```

Expected: ~16 commits, one per task (plus the test fix and design doc commits already on the branch).

PR title suggestion: `feat: native .dipx bundle support (loader + audit trail + strict resume)`

---

## Notes for the executor

- **Don't skip the smoke test in Task 5.** It's the first user-facing proof the dispatch works. If `tracker validate foo.dipx` doesn't run there, something's wrong with the wiring before any later tasks build on top.
- **Watch out for `cmd/tracker/run.go` having many `tracker.Run` call sites.** Each one needs the BundleIdentity wiring update (Task 9). Use `grep -n "tracker.Run\b" cmd/tracker/` to find them all.
- **The smoke .dipx fixture path** — for repeated manual testing, keep a pre-built `.dipx` somewhere persistent rather than regenerating each time.
- **Task 8 vs Task 9 ordering:** Task 8 is engine-internal (identity → checkpoint via `WithBundleIdentity`); Task 9 is CLI plumbing (loader → Config → engine). They can technically be done in either order, but the chosen order gives you a passing engine test before doing the wider CLI wiring.
- **Subagent-driven execution:** each task is self-contained — a fresh subagent can pick it up with just the task description, the design doc, and the project conventions in CLAUDE.md. Review between tasks; surface anything weird before moving on.
