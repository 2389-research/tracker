// ABOUTME: Writes the executed specification into the run directory — source .dip, expanded IR, and input documents.
// ABOUTME: Makes a run's structure reconstructible from the spec instead of from whichever per-node artifact files happen to exist.
package pipeline

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"

	"github.com/2389-research/dippin-lang/ir"
)

// Filenames written into <artifactDir>/<runID>/ by WriteSpecArtifacts.
// specFileMode is the mode for every stored spec artifact. Owner-only: the
// files carry the run's --param values and any reference material it was given.
const specFileMode = 0o600

const (
	SpecSourceFile = "workflow.dip"
	SpecIRFile     = "workflow.ir.json"
	SpecInputsDir  = "inputs"
)

// SpecInput is one document supplied to a run — a spec, a sprint file, any
// reference material a node reads. Stored content-addressed so two runs given
// the same input share a name, and so a recorded digest can be checked against
// the bytes on disk.
type SpecInput struct {
	// Name is the document's original filename, for display only.
	Name string `json:"name"`
	// Role describes what the run used it for ("spec", "sprint", …). Free
	// text; nothing branches on it.
	Role string `json:"role,omitempty"`
	// SHA256 is the digest of Content, and the stored file's basename.
	SHA256 string `json:"sha256"`
	// Size is Content's length in bytes.
	Size int `json:"size_bytes"`
	// Ext is the stored file's extension, including the dot.
	Ext string `json:"ext,omitempty"`

	// Content is the document itself. Not serialized into the manifest — the
	// bytes live in inputs/<sha256><ext> so the manifest stays small.
	Content []byte `json:"-"`
}

// SpecArtifacts is everything about the specification a run executed.
type SpecArtifacts struct {
	// SourcePath is where the .dip was read from, for display.
	SourcePath string
	// Source is the .dip verbatim.
	Source string
	// Workflow is the parsed IR. Recorded because dippin expands subgraphs at
	// compile time: the authored source and the graph that actually ran are
	// not the same document, and only the expanded form explains the events.
	Workflow *ir.Workflow
	// Params are the --param values as passed. These change behavior, so a
	// spec recorded without them does not reproduce the run.
	Params map[string]string
	// Inputs are the documents the run was given.
	Inputs []SpecInput
	// BundleIdentity is the .dipx identity ("sha256:<hex>"), empty for a
	// plain .dip run.
	BundleIdentity string
}

// secretParamKey matches param names that conventionally carry a credential.
// Params are recorded because they change behavior and a spec without them does
// not reproduce the run — but the manifest is durable and on-disk, so a value
// under one of these names is replaced with a placeholder rather than persisted.
// Matching is on the key, since values are opaque.
var secretParamKey = regexp.MustCompile(`(?i)(token|secret|password|passwd|credential|api[_-]?key|access[_-]?key|private[_-]?key|auth)`)

// redactedParamValue marks a param whose value was withheld. Deliberately not
// an empty string: a reader has to be able to tell "no value" from "not shown".
const redactedParamValue = "[redacted]"

// redactSecretParams copies params, replacing secret-looking values. The keys
// are kept — knowing *which* params a run was given is part of reproducing it,
// and the name alone is not the secret.
//
// Key-name matching is a heuristic and will miss a credential passed under an
// innocuous name. It is a floor, not a guarantee; the durable fix is to keep
// secrets out of --param entirely.
func redactSecretParams(params map[string]string) map[string]string {
	if params == nil {
		return nil
	}
	out := make(map[string]string, len(params))
	for k, v := range params {
		if v != "" && secretParamKey.MatchString(k) {
			out[k] = redactedParamValue
			continue
		}
		out[k] = v
	}
	return out
}

// SpecManifest is the record of what WriteSpecArtifacts stored, for embedding
// in a run manifest. Paths are relative to the run directory.
type SpecManifest struct {
	SourcePath     string            `json:"source_path,omitempty"`
	SourceFile     string            `json:"source_file,omitempty"`
	SourceSHA256   string            `json:"source_sha256,omitempty"`
	IRFile         string            `json:"ir_file,omitempty"`
	Params         map[string]string `json:"params,omitempty"`
	Inputs         []SpecInput       `json:"inputs,omitempty"`
	BundleIdentity string            `json:"bundle_identity,omitempty"`
}

// WriteSpecArtifacts stores the spec in runDir and returns a manifest of what
// it wrote.
//
// Without this, a run's own structure is only recoverable by inference. Node
// kind, the configured exit node, per-node model choices, and the edge set all
// live in the spec; a reader that instead infers them from which artifact
// directories exist gets them wrong at scale, because directories are sparse —
// a 39-node run leaves a handful. Recording the spec turns that inference into
// a join.
//
// Best-effort per file: a failure to write one artifact does not prevent the
// others, and the manifest reports only what actually landed. The run itself is
// never failed by this — losing telemetry is not worth losing the run.
func WriteSpecArtifacts(runDir string, spec SpecArtifacts) (SpecManifest, error) {
	manifest := SpecManifest{
		SourcePath:     spec.SourcePath,
		Params:         redactSecretParams(spec.Params),
		BundleIdentity: spec.BundleIdentity,
	}
	if err := refuseIfSymlink(runDir); err != nil {
		return manifest, fmt.Errorf("run dir unsafe: %w", err)
	}
	if err := os.MkdirAll(runDir, 0o700); err != nil {
		return manifest, fmt.Errorf("create run dir: %w", err)
	}

	var errs []error
	if spec.Source != "" {
		if err := writeSpecSource(runDir, spec.Source, &manifest); err != nil {
			errs = append(errs, err)
		}
	}
	if spec.Workflow != nil {
		if err := writeSpecIR(runDir, spec.Workflow, &manifest); err != nil {
			errs = append(errs, err)
		}
	}
	inputs, inputErrs := writeSpecInputs(runDir, spec.Inputs)
	manifest.Inputs = inputs
	errs = append(errs, inputErrs...)

	return manifest, joinErrs(errs)
}

// writeTightened creates path at specFileMode with the mode applied before any
// content lands and refuses to follow a symlink at the final component.
//
// Delegates to writeCaptureFile so the spec artifacts, run.json, and the
// activity-log mirror share one hardened create path (#213, #521, #529). The
// spec files carry the run's --param values and any reference material, so they
// must never be world-readable — not even momentarily during the write.
func writeTightened(path string, data []byte) error {
	return writeCaptureFile(path, data, specFileMode)
}

// writeSpecSource stores the .dip verbatim and records its digest.
func writeSpecSource(runDir, source string, manifest *SpecManifest) error {
	path := filepath.Join(runDir, SpecSourceFile)
	if err := writeTightened(path, []byte(source)); err != nil {
		return fmt.Errorf("write %s: %w", SpecSourceFile, err)
	}
	manifest.SourceFile = SpecSourceFile
	manifest.SourceSHA256 = digest([]byte(source))
	return nil
}

// writeSpecIR stores the expanded graph as JSON. Marshaling the IR is what
// `dippin parse` does, so the file is the same shape that tool emits.
func writeSpecIR(runDir string, workflow *ir.Workflow, manifest *SpecManifest) error {
	data, err := json.MarshalIndent(workflow, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal IR: %w", err)
	}
	path := filepath.Join(runDir, SpecIRFile)
	if err := writeTightened(path, data); err != nil {
		return fmt.Errorf("write %s: %w", SpecIRFile, err)
	}
	manifest.IRFile = SpecIRFile
	return nil
}

// writeSpecInputs stores each input content-addressed under inputs/. Returns
// the manifest entries for the inputs that landed, plus one error per failure.
func writeSpecInputs(runDir string, inputs []SpecInput) ([]SpecInput, []error) {
	if len(inputs) == 0 {
		return nil, nil
	}
	dir := filepath.Join(runDir, SpecInputsDir)
	if err := refuseIfSymlink(dir); err != nil {
		return nil, []error{fmt.Errorf("%s unsafe: %w", SpecInputsDir, err)}
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, []error{fmt.Errorf("create %s: %w", SpecInputsDir, err)}
	}

	stored := make([]SpecInput, 0, len(inputs))
	var errs []error
	for _, in := range inputs {
		in.SHA256 = digest(in.Content)
		in.Size = len(in.Content)
		if in.Ext == "" {
			in.Ext = filepath.Ext(in.Name)
		}
		path := filepath.Join(dir, in.SHA256+in.Ext)
		if err := writeTightened(path, in.Content); err != nil {
			errs = append(errs, fmt.Errorf("write input %s: %w", in.Name, err))
			continue
		}
		stored = append(stored, in)
	}
	return stored, errs
}

// digest returns the hex sha256 of b.
func digest(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// joinErrs collapses a slice of errors into one, or nil when empty.
func joinErrs(errs []error) error {
	switch len(errs) {
	case 0:
		return nil
	case 1:
		return errs[0]
	default:
		return fmt.Errorf("%w (and %d more)", errs[0], len(errs)-1)
	}
}
