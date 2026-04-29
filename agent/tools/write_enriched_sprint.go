// ABOUTME: Tool that writes one enriched sprint markdown file per call from a project-wide contract + per-sprint description.
// ABOUTME: Architect (strong model) supplies the project map; this tool calls a mid-tier model (e.g. Sonnet) once per sprint.
package tools

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/2389-research/tracker/llm"
)

//go:embed write_enriched_sprint_example.md
var enrichedSprintExample string

// WriteEnrichedSprintTool calls a mid-tier LLM once per sprint to write an enriched sprint
// markdown file from a project-wide architectural contract plus a per-sprint description.
type WriteEnrichedSprintTool struct {
	client   Completer
	model    string
	provider string
	workDir  string
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

// NewWriteEnrichedSprintTool creates a tool that writes enriched sprint markdown.
func NewWriteEnrichedSprintTool(client Completer, opts ...WriteEnrichedSprintOption) *WriteEnrichedSprintTool {
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

const sprintSystemPromptHeader = `You are writing one enriched sprint specification for a software project.

Your output is a markdown file consumed by a local code-generation LLM (e.g. qwen3.6:35b). The local LLM cannot infer English; it pattern-matches on exact section headers and copies code blocks verbatim. Density and structure matter more than prose.

# REQUIRED SECTIONS (in order)

Every enriched sprint MUST contain these sections, in this order:

1. ` + "`# Sprint NNN — Title (enriched spec)`" + ` — top-level heading; the title comes from the contract or per-sprint description
2. ` + "`## Scope`" + ` — 2-3 sentences of what this sprint delivers
3. ` + "`## Non-goals`" + ` — bullets of what's explicitly excluded
4. ` + "`## Dependencies`" + ` — bullets like "Sprint 002: cmd/agent/main.go exists with os.Args dispatch and printUsage"
5. ` + "`## <Language>/runtime conventions`" + ` (or "(inherited from Sprint NNN)" if applicable) — module/package name, error patterns, library choices, version constraints — copied or inherited from the contract
6. ` + "`## File structure`" + ` — fenced code block listing every file the sprint owns with one line per file: ` + "`path/to/file.ext — exported: Name1, Name2`" + `
7. ` + "`## Interface contract`" + ` — fenced code blocks with the exact types, function signatures, constants, and SQL DDL the sprint introduces
8. ` + "`## Imports per file`" + ` — for each new file in the sprint, a labeled fenced code block with the exact import statement(s)
9. ` + "`## Algorithm notes`" + ` — for any non-trivial logic, fenced code blocks with verbatim implementation the local LLM can copy
10. ` + "`## Test plan`" + ` — exact test function names with one-line expected behavior; subtests listed individually
11. ` + "`## Rules`" + ` — bullets of constraints (no third-party libs in this sprint, naming rules, exit-only-from-main, build tag exclusions, etc.)
12. ` + "`## New files`" + ` — bullets, one per NEW file this sprint creates: ` + "`- `\\``path/to/file.ext`\\`` — short purpose`" + `. Sprint executors extract the FIRST backticked path of each bullet to drive code generation.
13. ` + "`## Modified files`" + ` — bullets, one per file this sprint MODIFIES (i.e. files that already exist from prior sprints and need targeted changes — appends, dispatch additions, schema extensions). Same bullet format. Use ` + "`(none)`" + ` if the sprint introduces only new files.
14. ` + "`## Expected Artifacts`" + ` — flat list of every file this sprint produces (new + modified, paths only). Kept as a redundant index for human review and legacy tooling.
15. ` + "`## DoD`" + ` — checkable items ` + "`- [ ] ...`" + ` (5-10 items, every item machine-verifiable; first item should be writing the failing test)
16. ` + "`## Validation`" + ` — fenced bash code block with exact shell commands that prove the sprint is done

# DENSITY AND STYLE RULES

- Use code syntax, not prose. Write actual Go/TypeScript/Python/SQL, not English descriptions of them.
- For non-trivial logic, embed verbatim code blocks the executor can copy directly.
- Use exact import paths, exact field names, exact constants — no placeholders.
- For seed data and fixtures, list the exact values, not "example values".
- Target ~200-500 lines per sprint; longer is fine if the sprint is large.
- Match the language and library choices declared in the contract — never introduce a library the contract doesn't list.

# VERBATIM-CONTENT RULE FOR NON-CODE FILES (LOAD-BEARING)

The local code-generator (e.g. qwen) is a transcriber, not a designer — it must NEVER have to decide what file content looks like. Every file listed in ` + "`## New files`" + ` MUST have its complete, literal content shown somewhere in the document.

- **Code files** (.py, .ts, .go, .rs implementations): ` + "`## Interface contract`" + ` + ` + "`## Algorithm notes`" + ` together must give every type signature, every function body for non-trivial logic, and every import. If a code file has any unique behavior, the algorithm or full body must appear verbatim.

- **Config / manifest files** (` + "`pyproject.toml`" + `, ` + "`package.json`" + `, ` + "`tsconfig.json`" + `, ` + "`vite.config.ts`" + `, ` + "`Cargo.toml`" + `, ` + "`go.mod`" + `, ` + "`.eslintrc`" + `, ` + "`alembic.ini`" + `, ` + "`pytest.ini`" + `, ` + "`uvicorn`" + ` config, etc.): MUST appear in full as a fenced code block. The local model cannot reliably invent dependency lists, build-system config (e.g. ` + "`[tool.hatch.build.targets.wheel]`" + `), version pins, or path mappings.

- **Trivial/empty files** (empty ` + "`__init__.py`" + `, ` + "`mod.rs`" + `, ` + "`.gitkeep`" + `, license headers, one-line comments, marker files): MUST have a verbatim block, even if empty (show ` + "```python\n```" + ` for an empty file).

Add a dedicated subsection (e.g., ` + "`### Trivial file contents`" + `) under ` + "`## Algorithm notes`" + ` or ` + "`## Interface contract`" + ` that lists every trivial file and its exact body. Format:

` + "```" + `markdown
### Trivial file contents

**` + "`backend/app/__init__.py`" + `** — empty file. Content:
` + "```python" + `
` + "```" + `

**` + "`backend/app/routers/__init__.py`" + `** — empty file. Content:
` + "```python" + `
` + "```" + `

**` + "`backend/tests/__init__.py`" + `** — empty file. Content:
` + "```python" + `
` + "```" + `
` + "```" + `

If the trivial file truly is empty, the inner code block has zero lines between fences. The local model copies the literal content. Never describe trivial files only as "empty package init" or "package marker" without the verbatim block — that wording forces the model to make a decision and is the failure mode this rule prevents.

# CROSS-SPRINT REFERENCES

When this sprint depends on prior-sprint outputs, declare them in ` + "`## Dependencies`" + ` precisely:
- "Sprint 002: cmd/agent/main.go exists with os.Args dispatch and printUsage"
- "Sprint 003: internal/domain types exist (Budget, ProviderPolicy)"

The contract you receive lists every cross-sprint dependency edge. Surface the relevant ones in ` + "`## Dependencies`" + ` and use them in ` + "`## Imports per file`" + ` and ` + "`## Interface contract`" + `.

# NEW vs MODIFIED FILE SPLIT (LOAD-BEARING)

The sprint executor decides per-file behavior from the section the file appears in:
- ` + "`## New files`" + ` — executor calls a "generate from scratch" code path; if the file already exists it is SKIPPED (won't overwrite prior sprints' work).
- ` + "`## Modified files`" + ` — executor reads the existing file and asks the model to apply ONLY the changes described under this section, returning the full updated file.

Misclassifying a modified file as new will cause the executor to overwrite a prior sprint's contribution. Misclassifying a new file as modified will fail because there's no existing file to read. The architect's per-sprint description tells you which category each file belongs to — follow it strictly.

Bullets in both sections share the same format and are parsed by an awk/sed extractor that takes the FIRST backtick-wrapped token as the path:

` + "`- `\\``cmd/agent/main.go`\\`` — entrypoint with os.Args dispatch`" + `

Other inline backticks in the descriptive tail are fine; the parser only takes the first.

If the architect's description does not categorize a file, default rule: a file is NEW if no prior sprint owns it (per the contract's file-ownership map); otherwise it is MODIFIED.

# OUTPUT FORMAT

Output ONLY the raw markdown content of the sprint file. No commentary, no explanations, no fences wrapping the whole document. The first line of your output must be exactly:

` + "`# Sprint NNN — Title (enriched spec)`" + `

# COMPLETE REFERENCE EXAMPLE

The following is a complete enriched sprint at the target density and structure. Match this style — same section names, same density, same use of fenced code blocks for all structural content. (This example is for a Go project; adapt language idioms to whatever the contract specifies.)

---

`

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
	if args.Path == "" {
		return "", fmt.Errorf("write_enriched_sprint: path is required")
	}
	if args.Description == "" {
		return "", fmt.Errorf("write_enriched_sprint: description is required")
	}

	contract := args.Contract
	if contract == "" {
		contractPath := args.ContractFile
		if contractPath == "" {
			contractPath = ".ai/contract.md"
		}
		if !filepath.IsAbs(contractPath) && t.workDir != "" {
			contractPath = filepath.Join(t.workDir, contractPath)
		}
		data, err := os.ReadFile(contractPath)
		if err != nil {
			return "", fmt.Errorf("write_enriched_sprint: read contract from %s: %w (provide `contract` inline or write the contract file first)", contractPath, err)
		}
		contract = string(data)
	}

	if args.OutputDir == "" || args.OutputDir == "." {
		args.OutputDir = t.workDir
	}
	if args.OutputDir == "" {
		args.OutputDir = "."
	}
	if !filepath.IsAbs(args.OutputDir) && t.workDir != "" {
		args.OutputDir = filepath.Join(t.workDir, args.OutputDir)
	}

	systemPrompt := sprintSystemPromptHeader + enrichedSprintExample

	userPrompt := fmt.Sprintf(
		"CONTRACT (project-wide architectural map shared across all sprints):\n\n%s\n\n"+
			"SPRINT TO WRITE: %s\n\n"+
			"Per-sprint description from the architect:\n%s\n\n"+
			"Write the complete enriched sprint markdown for the file above. "+
			"Output ONLY the raw markdown — first line must be the `# Sprint NNN — Title (enriched spec)` heading.",
		contract, args.Path, args.Description,
	)

	resp, err := t.client.Complete(ctx, &llm.Request{
		Model:    t.model,
		Provider: t.provider,
		Messages: []llm.Message{
			{Role: llm.RoleSystem, Content: []llm.ContentPart{{Kind: llm.KindText, Text: systemPrompt}}},
			llm.UserMessage(userPrompt),
		},
	})
	if err != nil {
		return "", fmt.Errorf("write_enriched_sprint: failed writing %s: %w", args.Path, err)
	}

	content := resp.Text()
	content = trimEnclosingMarkdownFence(content)

	path := filepath.Join(args.OutputDir, args.Path)
	if err := writeSprintFile(path, content); err != nil {
		return "", err
	}

	return fmt.Sprintf("Wrote %s (%d bytes). Model: %s. Tokens: %d in / %d out.",
		args.Path, len(content), t.model, resp.Usage.InputTokens, resp.Usage.OutputTokens), nil
}

// trimEnclosingMarkdownFence removes a single pair of markdown fences wrapping
// the entire response (some models wrap markdown output in ```markdown ... ```).
// It does NOT touch internal fenced code blocks within the document.
func trimEnclosingMarkdownFence(s string) string {
	trimmed := strings.TrimSpace(s)
	lines := strings.Split(trimmed, "\n")
	if len(lines) < 2 {
		return s
	}
	first := strings.TrimSpace(lines[0])
	last := strings.TrimSpace(lines[len(lines)-1])
	if strings.HasPrefix(first, "```") && last == "```" {
		return strings.Join(lines[1:len(lines)-1], "\n")
	}
	return s
}

func writeSprintFile(path string, content string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", dir, err)
	}
	return os.WriteFile(path, []byte(content), 0o644)
}
