// ABOUTME: Domain workflow (relocated from agent/tools per #452): writes one enriched
// ABOUTME: sprint markdown file per call from a project-wide contract + per-sprint description.
// ABOUTME: Architect (strong model) supplies the project map; this tool calls a mid-tier model once per sprint.
package sprintwriter

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/2389-research/tracker/agent/exec"
	"github.com/2389-research/tracker/agent/tools"
)

// WriteEnrichedSprintTool calls a mid-tier LLM once per sprint to write an enriched sprint
// markdown file from a project-wide architectural contract plus a per-sprint description.
type WriteEnrichedSprintTool struct {
	client   tools.Completer
	model    string
	provider string
	workDir  string
	env      exec.ExecutionEnvironment
}

// WriteEnrichedSprintOption configures the WriteEnrichedSprintTool.
type WriteEnrichedSprintOption func(*WriteEnrichedSprintTool)

// WithSprintWriterModel sets the model used to write each sprint.
func WithSprintWriterModel(model string) WriteEnrichedSprintOption {
	return func(t *WriteEnrichedSprintTool) { t.model = model }
}

// WithSprintWriterProvider sets the provider used to write each sprint.
func WithSprintWriterProvider(provider string) WriteEnrichedSprintOption {
	return func(t *WriteEnrichedSprintTool) { t.provider = provider }
}

// WithSprintWriterWorkDir sets the base directory for writing sprint files.
func WithSprintWriterWorkDir(dir string) WriteEnrichedSprintOption {
	return func(t *WriteEnrichedSprintTool) { t.workDir = dir }
}

// WithSprintWriterEnv routes file writes through the supplied
// ExecutionEnvironment so the writable_paths fs-jail (#272) can intercept
// generated sprint writes alongside the openat2-protected env.WriteFile
// path. When nil (default), the tool falls back to direct os.WriteFile —
// fine for the unjailed code path but bypasses the jail when writable_paths
// is set (#275 audit pass).
//
// If workDir is still empty when this option fires, defaults it to
// env.WorkingDir(). Without that default, a caller that supplies env but
// not WithSprintWriterWorkDir would get filepath.Rel(env.WorkingDir(),
// absPath) producing a leading "../..." that env.WriteFile rejects — the
// tool would silently stop writing files (#275 review, Copilot
// write_enriched_sprint.go:57).
func WithSprintWriterEnv(env exec.ExecutionEnvironment) WriteEnrichedSprintOption {
	return func(t *WriteEnrichedSprintTool) {
		t.env = env
		if t.workDir == "" && env != nil {
			t.workDir = env.WorkingDir()
		}
	}
}

// NewWriteEnrichedSprintTool creates a tool that writes enriched sprint markdown.
func NewWriteEnrichedSprintTool(client tools.Completer, opts ...WriteEnrichedSprintOption) *WriteEnrichedSprintTool {
	t := &WriteEnrichedSprintTool{
		client:   client,
		model:    "claude-sonnet-4-6",
		provider: "anthropic",
	}
	for _, opt := range opts {
		opt(t)
	}
	return t
}

func (t *WriteEnrichedSprintTool) Name() string { return "write_enriched_sprint" }

func (t *WriteEnrichedSprintTool) Description() string {
	return "Write ONE enriched sprint markdown file. The tool reads the project-wide " +
		"architectural contract from .ai/contract.md (write it once, before iterating) " +
		"and uses it on every invocation. Pass only the per-sprint path and description; " +
		"the tool calls a mid-tier model (Sonnet) to produce the complete enriched markdown " +
		"matching the format consumed by local-LLM sprint executors. Call this tool once " +
		"per sprint — iterate across all sprints in the plan."
}

func (t *WriteEnrichedSprintTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"path": {
				"type": "string",
				"description": "File path for THIS sprint, relative to output_dir, e.g. SPRINT-005.md"
			},
			"description": {
				"type": "string",
				"description": "Per-sprint description: title, scope summary, FRs covered, files this sprint owns, cross-sprint dependencies it consumes. The contract (read from disk) carries the project map; this carries the per-sprint slice."
			},
			"contract_file": {
				"type": "string",
				"description": "Optional path to the contract file. Relative paths resolved against working directory. Defaults to .ai/contract.md."
			},
			"contract": {
				"type": "string",
				"description": "Optional inline contract string. If provided, takes precedence over contract_file. Use this only if you cannot write a contract file first."
			},
			"output_dir": {
				"type": "string",
				"description": "Directory to write the sprint file. Defaults to working directory."
			}
		},
		"required": ["path", "description"]
	}`)
}

// SprintRunResult captures the per-sprint outcome of RunOne. Used by callers
// (Execute and dispatch_sprints) to format summaries and aggregate counts.
type SprintRunResult struct {
	Path           string
	Bytes          int
	Verdict        string
	PatchesApplied int
	Model          string
	AuthorIn       int
	AuthorOut      int
	AuditIn        int
	AuditOut       int
}

// LoadContract reads the contract file from disk. Resolves relative paths
// against workDir. Empty contractFile defaults to ".ai/contract.md".
//
// When a workDir is set, validates that the resolved path stays inside it.
// The contract content is shipped to the LLM provider as prompt context, so
// a traversal here is a data-exfiltration vector. Both relative paths with
// ".." segments and absolute paths pointing outside workDir are rejected.
func (t *WriteEnrichedSprintTool) LoadContract(contractFile string) (string, error) {
	if contractFile == "" {
		contractFile = ".ai/contract.md"
	}
	resolved, err := t.resolveUnderWorkDir(contractFile)
	if err != nil {
		return "", fmt.Errorf("contract_file %q: %w", contractFile, err)
	}
	data, err := os.ReadFile(resolved)
	if err != nil {
		return "", fmt.Errorf("read contract from %s: %w (write the contract file first)", resolved, err)
	}
	return string(data), nil
}

// resolveUnderWorkDir takes a path argument (absolute or relative) and returns
// the absolute resolved path. When workDir is set, delegates to
// tools.ResolveUnderRoot which evaluates symlinks before the containment check —
// that prevents a symlink inside workDir pointing outside from being used to
// escape (which a pure string-prefix check would miss). When workDir is
// empty (e.g., in tests that don't set one), passes through with cleaning
// only.
func (t *WriteEnrichedSprintTool) resolveUnderWorkDir(p string) (string, error) {
	if t.workDir == "" {
		return filepath.Clean(p), nil
	}
	return tools.ResolveUnderRoot(t.workDir, p)
}

// resolveOutputDir applies the same defaulting rules Execute uses, then
// confines the result under workDir when one is configured. An LLM-supplied
// absolute outputDir like "/tmp/outside" would otherwise become the new
// write root for every per-file tools.ResolveUnderRoot call — escaping the
// configured workspace entirely.
//
// Returns the absolute output path (or an error if it can't be confined to
// workDir). When workDir is empty (untested-config path), the existing
// behavior is preserved.
func (t *WriteEnrichedSprintTool) resolveOutputDir(outputDir string) (string, error) {
	if outputDir == "" || outputDir == "." {
		outputDir = t.workDir
	}
	if outputDir == "" {
		outputDir = "."
	}
	if t.workDir == "" {
		// No workspace to anchor to; preserve historical behavior.
		return absOutputDirNoRoot(outputDir)
	}
	return tools.ResolveUnderRoot(t.workDir, outputDir)
}

// absOutputDirNoRoot returns the absolute form of outputDir when there is no
// workDir to confine it under. Split out of resolveOutputDir to keep that
// method's branching within the complexity budget (#452 relocation).
func absOutputDirNoRoot(outputDir string) (string, error) {
	if !filepath.IsAbs(outputDir) {
		abs, err := filepath.Abs(outputDir)
		if err != nil {
			return "", err
		}
		return abs, nil
	}
	return outputDir, nil
}

// Execute is the LLM-tool entry point. Parses args, loads contract, runs one sprint.
func (t *WriteEnrichedSprintTool) Execute(ctx context.Context, input json.RawMessage) (string, error) {
	var args struct {
		Path         string `json:"path"`
		Description  string `json:"description"`
		Contract     string `json:"contract"`
		ContractFile string `json:"contract_file"`
		OutputDir    string `json:"output_dir"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("parse args: %w", err)
	}

	contract := args.Contract
	if contract == "" {
		c, err := t.LoadContract(args.ContractFile)
		if err != nil {
			return "", fmt.Errorf("write_enriched_sprint: %w", err)
		}
		contract = c
	}
	outputDir, err := t.resolveOutputDir(args.OutputDir)
	if err != nil {
		return "", fmt.Errorf("write_enriched_sprint: %w", err)
	}

	r, err := t.RunOne(ctx, contract, args.Path, args.Description, outputDir)
	if err != nil {
		return "", fmt.Errorf("write_enriched_sprint: %w", err)
	}
	return fmt.Sprintf("Wrote %s (%d bytes, audit=%s, patches=%d). Model: %s. Tokens: author %d in / %d out, audit %d in / %d out.",
		r.Path, r.Bytes, r.Verdict, r.PatchesApplied, r.Model,
		r.AuthorIn, r.AuthorOut, r.AuditIn, r.AuditOut), nil
}

// writeSprintFile prefers the configured ExecutionEnvironment when set —
// that routes through the writable_paths fs-jail (#272) WriteOpener so
// generated sprint files are bounded just like apply_patch/edit/write.
// Falls back to direct os.WriteFile for unjailed sessions. #275 audit pass.
//
// The os.* fallback below is reachable ONLY when t.env == nil. backend_native
// wires the JAILED *LocalEnvironment into t.env whenever writable_paths is set
// (it refuses-to-start otherwise), so env==nil implies no active jail and the
// fallback has nothing to bypass. That env==nil invariant is enforced by
// backend_native's refuse-to-start, not by the linter: the jailcheck linter
// (#283) only requires this direct-os.* fallback to carry the marker below, so
// no unannotated bypass can be added. The marker records that this exception
// is intentional and audited.
//
//jail:allow-unjailed-fallback env==nil ⟹ no active jail; see agent-tool-jail-checklist.md
func (t *WriteEnrichedSprintTool) writeSprintFile(ctx context.Context, path string, content string) error {
	if t.env != nil {
		rel, err := filepath.Rel(t.env.WorkingDir(), path)
		if err != nil {
			return fmt.Errorf("compute path relative to env workdir %q for %q: %w", t.env.WorkingDir(), path, err)
		}
		return t.env.WriteFile(ctx, rel, content)
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", dir, err)
	}
	return os.WriteFile(path, []byte(content), 0o644)
}
