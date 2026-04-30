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
- Use exact import paths, exact field names, exact constants — no placeholders.
- For seed data and fixtures, list the exact values, not "example values".
- Target ~200-500 lines per sprint; longer is fine if the sprint is large (foundation sprints with full schemas often run 600-1000 lines).
- Match the language and library choices declared in the contract — never introduce a library the contract doesn't list.

# DENSITY: signatures + algorithm prose vs verbatim full bodies

Two density modes are acceptable, picked by file type:

1. **Verbatim full text** — required for tiny data-shaped files where exact text matters more than logic: ` + "`pyproject.toml`" + `, ` + "`package.json`" + `, build-system manifests, framework config files (` + "`tsconfig.json`" + `, ` + "`vite.config.ts`" + `, ` + "`alembic.ini`" + `, etc.), small exception/error-code constants files, the auto-discovery app factory (e.g., a FastAPI ` + "`main.py`" + ` with ` + "`pkgutil.iter_modules`" + ` router discovery), small async runners (` + "`init_db.py`" + `), all empty package markers.

2. **Signatures + algorithm prose** — preferred for substantive code (routers, services, business logic, test files). Provide every type signature in ` + "`## Interface contract`" + `; provide every per-route or per-function logic flow in ` + "`## Algorithm notes`" + ` as numbered prose steps that name types/exceptions/symbols by exact identifier; provide per-file imports as full statements. The local model writes idiomatic bodies that match the contract.

The empirical reason for mode 2 (validated end-to-end Apr 30 2026): when the architect writes verbatim bodies for a complex route handler, the bodies tend to carry stale unused imports, dead branches (` + "`if False`" + `), or impl-grade bugs that the local model faithfully transcribes. Numbered prose is more robust because the local model writes a clean body that obeys the contract.

When in doubt: prefer mode 2. Use mode 1 only when EXACT TEXT is what matters (config blocks, manifest pinning).

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

# WITHIN-SPRINT PATTERNS TO APPLY (load-bearing — read before producing the sprint)

These patterns are the architect-side rules that close gaps the local code generator would otherwise fill in wrong. They are NOT a checklist to mechanically tick off — they are pattern categories to recognize and close in your spec output. The contract.md you receive will pin cross-sprint conventions; the patterns below are the within-sprint application.

## P-1. Per-file imports as complete Python/Go/TS statements

Every entry in ` + "`## New files`" + ` includes the EXACT imports list — full language statements, never bare module names. For class-vs-module collisions (Python: ` + "`datetime`" + `, ` + "`date`" + `, ` + "`time`" + `, ` + "`decimal.Decimal`" + `; TS/JS: relative path extensions), use the unambiguous import form explicitly.

Why: the local model treats a bare ` + "`datetime`" + ` token as ` + "`import datetime`" + ` (the module). Subsequent ` + "`Mapped[datetime]`" + ` annotations then crash because the type needs the CLASS, not the module.

## P-2. Route discipline (within a router file)

- Path/query parameters annotated with real Python types (` + "`uuid.UUID`" + `, ` + "`date`" + `, etc.) — never default to ` + "`str`" + `. Without the annotation, FastAPI doesn't auto-parse and downstream type-bound code crashes.
- Collection routes use empty-string path ` + "`\"\"`" + ` (not ` + "`\"/\"`" + `). Trailing-slash form causes 307 redirects when tests call without the slash.
- Static-path routes declared BEFORE parameterized routes that share their prefix. FastAPI matches in declaration order.
- The route order in the API contract table MUST match the declaration order in the router file (this is the structural-section rule below — prose alone won't work).

## P-3. Schema construction discipline

When the algorithm constructs a Read schema explicitly (e.g., ` + "`ShiftRead(...)`" + ` with a derived field), list the EXACT field set in the algorithm step: "construct ShiftRead with EXACTLY these fields and no others: id, location_id, ... — do NOT pass any other field." Plus a Rule reinforcement.

Pydantic / Pydantic-equivalent Read schemas have exactly the fields declared in contract.md's data contract; do NOT invent fields based on "what should be there." (e.g., ` + "`Shift`" + ` has no ` + "`station_id`" + ` field — that lives on ` + "`Assignment`" + ` — even though the local model might infer one given the relationship between shifts and stations.)

## P-4. Test fixture usage

Tests take fixtures (` + "`client`" + `, ` + "`db_session`" + `, ` + "`auth_headers`" + `, etc.) as **function parameters**. Test files contain only test functions; they NEVER construct ` + "`AsyncClient`" + `, engines, or sessions inline. The conftest.py is pinned in contract.md (foundation sprint owns it).

## P-5. Test data serialization

For HTTP request bodies (httpx ` + "`json=`" + ` argument or equivalent):
- ` + "`date`" + ` / ` + "`time`" + ` / ` + "`datetime`" + ` → ` + "`.isoformat()`" + ` before passing
- ` + "`uuid.UUID`" + ` → ` + "`str(...)`" + ` before passing
- Other primitives pass through as-is

For ORM queries that filter on UUID columns from JSON-string ids:
` + "```python\nreg_id = uuid.UUID(response.json()[\"id\"])\nresult = await db_session.execute(select(Registration).where(Registration.id == reg_id))\n```" + `

For test assertions on error responses, USE THE EXACT JSON PATH that contract.md's error handler shape produces. If contract.md pins a flat ` + "`{\"detail\": str, \"error_code\": str}`" + ` shape, tests assert ` + "`body[\"error_code\"]`" + `; do NOT assert ` + "`body[\"detail\"][\"error_code\"]`" + `.

## P-6. Tests need second-instance values pinned

When a test needs a second instance of a model with unique constraints (second volunteer with different email/phone, second location, etc.), specify EXACT values in the test contract that don't collide with the fixture's primary instance. Example: "Construct other_volunteer with email='other@example.com', phone='+15559999999'."

If you say "use a different value" without pinning what, the local model picks unpredictably and may collide with other tests.

## P-7. Idempotent endpoint test patterns

When testing idempotent endpoints (waiver/sign, orientation/complete), the test contract names what to capture and what to compare:

> Sign once; capture ` + "`signed_at_1`" + `; sign again; capture ` + "`signed_at_2`" + `. Both 201; ` + "`signed_at_1 == signed_at_2`" + ` (same row, no new insert).

# ARCHITECT-DISCIPLINE META-RULES

## M-1. Structural sections must embody structural rules

When you state a structural rule (e.g., "static routes before parameterized routes"), the structured sections MUST reflect it. The API contract table, signature blocks, algorithm subsections — all of them. The local model replicates the structure of the spec, not the prose rules. If a Rule says X but the table shows Y, the local model writes Y.

## M-2. Make concrete choices

Where the per-sprint description is ambiguous, commit to one answer. Don't hedge with "you may use X or Y." Don't write "choose based on your preference." The local model has no preference — it needs one answer.

## M-3. Pin exact field sets

When defining types, schemas, or constructed objects, list every field with its type. Never write partial examples with "...etc." The local model invents fields when given an incomplete shape.

## M-4. Respect contract.md's FROZEN list and pinned conventions

contract.md may declare a list of FROZEN files (under the front-loaded foundation pattern: ` + "`app/main.py`" + `, ` + "`app/models.py`" + `, ` + "`app/schemas.py`" + `, ` + "`app/exceptions.py`" + `, ` + "`app/database.py`" + `, ` + "`app/config.py`" + `, ` + "`tests/conftest.py`" + `). For sprints other than the foundation, ` + "`## Modified files: (none)`" + ` is the expected output — the foundation owns those files and the auto-discovery main.py picks up new router files automatically.

If the contract pins a specific exception-handler JSON shape (flat vs nested), every test's error-response assertion in this sprint MUST match that shape.

If the contract pins async loading strategy (` + "`lazy=\"selectin\"`" + ` on collection-side relationships), every relationship in any model this sprint touches MUST honor it.

# TWO-PASS REVIEW

After producing the SPRINT-NNN.md (Pass 1 — convert into the speedrun format applying the patterns above), run **Pass 2** before saving:

Read the spec as if you were the local code-generator — what could you misinterpret?

- Does each Tricky semantics rule have a WHY (what runtime symptom occurs without it)?
- Does every collection-side relationship have ` + "`lazy=\"selectin\"`" + ` (or contract-pinned equivalent)?
- Are path/query parameter types annotated everywhere with real Python types, not ` + "`str`" + `?
- Are static routes ordered before parameterized routes in the API table?
- When constructing a schema explicitly, is the EXACT field set listed?
- Do the Imports lists for each file actually cover every symbol the Algorithm references?
- Does the Test contract's response-shape assertions match contract.md's pinned exception handler shape (flat ` + "`body[\"error_code\"]`" + ` vs nested ` + "`body[\"detail\"][\"error_code\"]`" + `)?
- When a test creates a second instance with unique constraints, are EXACT distinguishing values specified?
- Does the API contract's route order match the implied algorithm-section order?
- Are dates/times/UUIDs in JSON bodies serialized correctly (` + "`.isoformat()`" + ` / ` + "`str()`" + `)?
- Are FROZEN files cross-referenced consistently with contract.md?
- Are there fields that look like "common sense additions" the local model might invent? (Pin against them with explicit "no other fields" rules.)

If you find an ambiguity, patch the spec. The point isn't perfection — it's making the local generator's output deterministic enough to be reliable.

# OUTPUT FORMAT

Output ONLY the raw markdown content of the sprint file. No commentary, no explanations, no fences wrapping the whole document. The first line of your output must be exactly:

` + "`# Sprint NNN — Title (enriched spec)`" + `

# COMPLETE REFERENCE EXAMPLE

The following is a complete enriched sprint at the target density and structure. Match this style — same section names, same density, same use of fenced code blocks for all structural content. (This example is for a Go project; adapt language idioms to whatever the contract specifies.)

---

`

const auditSystemPrompt = `You are auditing a draft enriched sprint specification produced by a sibling author model. You receive: the project-wide contract, the per-sprint description from the architect, and the author's draft. Your job is narrow: detect ambiguity-class issues that would cause the local code-generator (qwen3.6:35b or similar) to produce broken output. If you find issues, emit precise SEARCH/REPLACE patches. If the draft is clean, declare PASS.

There is no third pass after you. Be decisive and minimal — preserve everything in the draft that's correct; patch surgically.

# OUTPUT FORMAT — TWO ALTERNATIVES

Your output starts with EXACTLY one of these markers on the first line:

## Option A: ` + "`AUDIT-VERDICT: PASS`" + `

The draft is clean; no patches needed. Output ONLY this single line and nothing else.

## Option B: ` + "`AUDIT-VERDICT: PATCHED`" + `

One or more issues were found. Follow the verdict line with one or more SEARCH/REPLACE blocks describing surgical edits to apply to the draft. Each block has this exact form (Aider-style):

` + "```" + `
<<<<<<< SEARCH
<exact text from the draft to find>
=======
<replacement text>
>>>>>>> REPLACE
` + "```" + `

Critical block rules:
- The SEARCH section MUST match the draft EXACTLY — every character including whitespace, indentation, and line breaks. The tool does byte-exact matching; whitespace drift breaks the patch.
- Include enough context lines around the change to make the SEARCH text unique within the draft. 3-5 surrounding lines is usually enough.
- For pure inserts (e.g., adding a new Rule line), the SEARCH text is a unique anchor (the line before or after the insertion point) and the REPLACE text is the anchor + the new content.
- Multiple non-adjacent edits → multiple blocks. Each block applies independently, in order.
- Do NOT rewrite the whole file. Do NOT emit a full ` + "`# Sprint NNN — Title`" + ` markdown header. Emit only the SEARCH/REPLACE blocks.

After your last block, output nothing — no commentary, no "FIXED:" notes, no audit summary.

# WHEN TO PASS vs PATCH

**PATCH only when the issue is CLEAR and ambiguity-class** — the local generator would produce broken output without the fix. Examples:
- Path parameter typed as ` + "`str`" + ` where it should be ` + "`uuid.UUID`" + ` (defect class 7)
- Test assertion uses ` + "`body[\"detail\"][\"error_code\"]`" + ` but contract pins flat shape (defect class 14)
- Imports list missing a symbol the Algorithm references (defect class 2)
- Schema construction missing the "do NOT pass any other field" guard (defect class 8)

**PASS when uncertain or when the issue is stylistic.** The auditor is for catching CLEAR ambiguity-class issues, not for stylistic improvements. If you cannot decide, PASS.

# AUDIT CHECKLIST

Run through every category. For each: if the draft satisfies it, leave that section unchanged. If not, patch the relevant section minimally.

## Pattern coverage (within-sprint)

- Tricky semantics rules each have a WHY (what runtime symptom occurs without that rule)
- Every collection-side relationship has ` + "`lazy=\"selectin\"`" + ` (or contract-pinned equivalent)
- Bidirectional relationships have ` + "`back_populates`" + ` on BOTH sides with matching names
- Path/query parameters annotated with real Python types (` + "`uuid.UUID`" + `, ` + "`date`" + `, etc.) — never default to ` + "`str`" + `
- Collection routes use empty-string path ` + "`\"\"`" + ` (not ` + "`\"/\"`" + `) — trailing-slash form causes 307 redirects
- Static-path routes declared BEFORE parameterized routes that share their prefix — IN BOTH the rule AND the API contract table (structural-section consistency)
- When the algorithm constructs a Read schema explicitly, the EXACT field set is listed with "do NOT pass any other field"
- Imports lists for each file cover EVERY symbol the Algorithm references (full Python statements, not bare module names; address class-vs-module collisions like ` + "`datetime`" + ` / ` + "`date`" + ` / ` + "`time`" + ` explicitly)
- Test contract assertion paths match the contract's pinned exception handler shape (flat ` + "`body[\"error_code\"]`" + ` vs nested ` + "`body[\"detail\"][\"error_code\"]`" + ` — verify by checking the contract)
- Tests creating second instances with unique constraints specify EXACT distinguishing values (e.g., second_volunteer email/phone given verbatim, not "different value")
- Dates/times/UUIDs in JSON request bodies use ` + "`.isoformat()`" + ` / ` + "`str(...)`" + ` serialization
- String UUIDs from ` + "`response.json()[\"id\"]`" + ` are parsed via ` + "`uuid.UUID(...)`" + ` before ORM queries
- No invented schema fields (e.g., ` + "`Shift.station_id`" + ` when not in the contract's data model)

## Architect-discipline meta-rules

- Structural sections embody structural rules (table order matches the Rule's stated order)
- Concrete choices made (no "you may use X or Y", no "choose based on preference")
- Exact field sets pinned (no partial examples with "...etc.")
- contract.md's FROZEN list and pinned conventions respected — for sprints other than the foundation, ` + "`## Modified files: (none)`" + ` is the expected output
- Trivial files have verbatim content blocks (even empty files show ` + "```python\n```" + `)

## Cross-section consistency

- Imports lists cover every symbol the Algorithm sections reference
- Test fixture parameters match what the test bodies actually use
- API contract route order matches Algorithm subsection order
- "New files" bullets and "Expected Artifacts" agree

# PATCHING DISCIPLINE

When emitting SEARCH/REPLACE blocks:
- Preserve every section the draft got right.
- Patch the smallest unit that closes the issue (a Rule line, a table row, an algorithm step, an imports list).
- Do NOT introduce new ambiguities while patching. If you patch a Rule, the structural sections must still match (per M-1) — emit additional blocks for any structural sections that need updating.
- Each block must be self-contained: the SEARCH text must be unique within the draft (include 3-5 lines of surrounding context if needed for uniqueness).

# EXAMPLE OF A CORRECT PATCHED RESPONSE

Suppose the draft has this in the API contract table:

` + "```" + `
| GET | /shifts/{shift_id} | none | — | ShiftRead (200) | 404 NOT_FOUND |
| GET | /shifts/browse     | none | — | list[ShiftRead] (200) | — |
` + "```" + `

…but the spec's Rule says "static routes before parameterized." The auditor patches:

` + "```" + `
AUDIT-VERDICT: PATCHED
<<<<<<< SEARCH
| GET | /shifts/{shift_id} | none | — | ShiftRead (200) | 404 NOT_FOUND |
| GET | /shifts/browse     | none | — | list[ShiftRead] (200) | — |
=======
| GET | /shifts/browse     | none | — | list[ShiftRead] (200) | — |
| GET | /shifts/{shift_id} | none | — | ShiftRead (200) | 404 NOT_FOUND |
>>>>>>> REPLACE
` + "```" + `

That's a 5-line patch (2 SEARCH lines, 2 REPLACE lines, plus markers) instead of regenerating the whole spec.
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

	authorSystemPrompt := sprintSystemPromptHeader + enrichedSprintExample

	authorUserPrompt := fmt.Sprintf(
		"CONTRACT (project-wide architectural map shared across all sprints):\n\n%s\n\n"+
			"SPRINT TO WRITE: %s\n\n"+
			"Per-sprint description from the architect:\n%s\n\n"+
			"Write the complete enriched sprint markdown for the file above. "+
			"Output ONLY the raw markdown — first line must be the `# Sprint NNN — Title (enriched spec)` heading.",
		contract, args.Path, args.Description,
	)

	// PASS 1 — Author. Produces the draft.
	authorResp, err := t.client.Complete(ctx, &llm.Request{
		Model:    t.model,
		Provider: t.provider,
		Messages: []llm.Message{
			{Role: llm.RoleSystem, Content: []llm.ContentPart{{Kind: llm.KindText, Text: authorSystemPrompt}}},
			llm.UserMessage(authorUserPrompt),
		},
	})
	if err != nil {
		return "", fmt.Errorf("write_enriched_sprint: author pass failed for %s: %w", args.Path, err)
	}

	draft := trimEnclosingMarkdownFence(authorResp.Text())

	// PASS 2 — Auditor. Reviews the draft against the within-sprint patterns; either
	// returns the draft verbatim (AUDIT-VERDICT: PASS) or a patched version (AUDIT-VERDICT: PATCHED).
	// Different system prompt narrows the auditor's role; lower temperature makes the
	// audit deterministic.
	auditTemperature := 0.2
	auditUserPrompt := fmt.Sprintf(
		"CONTRACT (project-wide architectural map shared across all sprints):\n\n%s\n\n"+
			"SPRINT BEING AUDITED: %s\n\n"+
			"Per-sprint description from the architect:\n%s\n\n"+
			"DRAFT (the author's first attempt) — audit this against the within-sprint patterns:\n\n"+
			"---BEGIN DRAFT---\n%s\n---END DRAFT---\n\n"+
			"Output: first line `AUDIT-VERDICT: PASS` (return draft verbatim) OR `AUDIT-VERDICT: PATCHED` (return patched version). "+
			"Second line must be the `# Sprint NNN — Title (enriched spec)` heading. No commentary outside the markdown.",
		contract, args.Path, args.Description, draft,
	)

	auditResp, err := t.client.Complete(ctx, &llm.Request{
		Model:       t.model,
		Provider:    t.provider,
		Temperature: &auditTemperature,
		Messages: []llm.Message{
			{Role: llm.RoleSystem, Content: []llm.ContentPart{{Kind: llm.KindText, Text: auditSystemPrompt}}},
			llm.UserMessage(auditUserPrompt),
		},
	})
	if err != nil {
		// Audit failure is non-fatal — fall back to draft.
		// The author pass already produced something usable; an audit failure shouldn't lose it.
		fmt.Fprintf(os.Stderr, "write_enriched_sprint: audit pass failed for %s (using draft as-is): %v\n", args.Path, err)
		auditResp = nil
	}

	verdict := "PASS"
	final := draft
	patchesApplied := 0
	auditInTokens, auditOutTokens := 0, 0
	if auditResp != nil {
		auditText := trimEnclosingMarkdownFence(auditResp.Text())
		v, blocks, parseErr := parseAuditResponse(auditText)
		if parseErr != nil {
			fmt.Fprintf(os.Stderr, "write_enriched_sprint: audit response malformed for %s (using draft as-is): %v\n", args.Path, parseErr)
			verdict = "PASS-FALLBACK-MALFORMED"
		} else {
			verdict = v
			if v == "PATCHED" && len(blocks) > 0 {
				patched, applyErr := applySRBlocks(draft, blocks)
				if applyErr != nil {
					// One or more blocks failed to match. Fall back to draft (don't ship a half-patched spec).
					fmt.Fprintf(os.Stderr, "write_enriched_sprint: audit patch apply failed for %s (using draft as-is): %v\n", args.Path, applyErr)
					verdict = "PASS-FALLBACK-NOMATCH"
				} else {
					final = patched
					patchesApplied = len(blocks)
				}
			}
		}
		auditInTokens = auditResp.Usage.InputTokens
		auditOutTokens = auditResp.Usage.OutputTokens
	}

	path := filepath.Join(args.OutputDir, args.Path)
	if err := writeSprintFile(path, final); err != nil {
		return "", err
	}

	return fmt.Sprintf("Wrote %s (%d bytes, audit=%s, patches=%d). Model: %s. Tokens: author %d in / %d out, audit %d in / %d out.",
		args.Path, len(final), verdict, patchesApplied, t.model,
		authorResp.Usage.InputTokens, authorResp.Usage.OutputTokens,
		auditInTokens, auditOutTokens), nil
}

// srBlock is one Aider-style SEARCH/REPLACE patch.
type srBlock struct {
	Search  string
	Replace string
}

// parseAuditResponse parses the auditor's output. The first non-empty line must be
// `AUDIT-VERDICT: PASS` or `AUDIT-VERDICT: PATCHED`. For PATCHED, the rest of the body
// is one or more SEARCH/REPLACE blocks in Aider format:
//
//	<<<<<<< SEARCH
//	<old text>
//	=======
//	<new text>
//	>>>>>>> REPLACE
//
// Returns the verdict, the parsed blocks, and an error if the response is malformed.
func parseAuditResponse(s string) (verdict string, blocks []srBlock, err error) {
	trimmed := strings.TrimSpace(s)
	if trimmed == "" {
		return "", nil, fmt.Errorf("empty audit response")
	}
	lines := strings.SplitN(trimmed, "\n", 2)
	first := strings.TrimSpace(lines[0])
	const prefix = "AUDIT-VERDICT:"
	if !strings.HasPrefix(first, prefix) {
		return "", nil, fmt.Errorf("first line does not start with %q: got %q", prefix, first)
	}
	verdict = strings.TrimSpace(strings.TrimPrefix(first, prefix))
	if verdict != "PASS" && verdict != "PATCHED" {
		return "", nil, fmt.Errorf("unrecognized verdict: %q", verdict)
	}
	if verdict == "PASS" {
		return "PASS", nil, nil
	}
	// PATCHED — parse SR blocks from the remaining body
	if len(lines) < 2 {
		return verdict, nil, fmt.Errorf("PATCHED verdict but no blocks in body")
	}
	body := lines[1]
	blocks, err = parseSRBlocks(body)
	if err != nil {
		return verdict, nil, err
	}
	if len(blocks) == 0 {
		return verdict, nil, fmt.Errorf("PATCHED verdict but zero SR blocks parsed")
	}
	return verdict, blocks, nil
}

// parseSRBlocks extracts Aider-style SEARCH/REPLACE blocks from a body of text.
// Surrounding fences (e.g., ```) are tolerated and ignored. Returns blocks in source order.
func parseSRBlocks(body string) ([]srBlock, error) {
	const (
		searchMarker  = "<<<<<<< SEARCH"
		divideMarker  = "======="
		replaceMarker = ">>>>>>> REPLACE"
	)
	var blocks []srBlock
	rest := body
	for {
		startIdx := strings.Index(rest, searchMarker)
		if startIdx < 0 {
			break
		}
		afterStart := rest[startIdx+len(searchMarker):]
		// Skip the rest of the marker line (handles "<<<<<<< SEARCH\n" and "<<<<<<< SEARCH something\n").
		if nlIdx := strings.IndexByte(afterStart, '\n'); nlIdx >= 0 {
			afterStart = afterStart[nlIdx+1:]
		} else {
			return blocks, fmt.Errorf("malformed block: SEARCH marker without following newline")
		}
		divIdx := strings.Index(afterStart, divideMarker)
		if divIdx < 0 {
			return blocks, fmt.Errorf("malformed block: SEARCH without ======= divider")
		}
		searchText := strings.TrimRight(afterStart[:divIdx], "\n")
		afterDiv := afterStart[divIdx+len(divideMarker):]
		if nlIdx := strings.IndexByte(afterDiv, '\n'); nlIdx >= 0 {
			afterDiv = afterDiv[nlIdx+1:]
		} else {
			return blocks, fmt.Errorf("malformed block: ======= divider without following newline")
		}
		endIdx := strings.Index(afterDiv, replaceMarker)
		if endIdx < 0 {
			return blocks, fmt.Errorf("malformed block: ======= without >>>>>>> REPLACE marker")
		}
		replaceText := strings.TrimRight(afterDiv[:endIdx], "\n")
		blocks = append(blocks, srBlock{Search: searchText, Replace: replaceText})
		rest = afterDiv[endIdx+len(replaceMarker):]
	}
	return blocks, nil
}

// applySRBlocks applies the given blocks to the draft in source order. Each block's
// SEARCH text MUST appear EXACTLY ONCE in the draft for the patch to apply (this catches
// ambiguous matches early). Returns the patched text or an error if any block fails.
func applySRBlocks(draft string, blocks []srBlock) (string, error) {
	out := draft
	for i, b := range blocks {
		if b.Search == "" {
			return "", fmt.Errorf("block %d: empty SEARCH text not supported (use a unique anchor for inserts)", i)
		}
		count := strings.Count(out, b.Search)
		if count == 0 {
			return "", fmt.Errorf("block %d: SEARCH text not found in draft", i)
		}
		if count > 1 {
			return "", fmt.Errorf("block %d: SEARCH text matched %d times (must be unique — add more surrounding context)", i, count)
		}
		out = strings.Replace(out, b.Search, b.Replace, 1)
	}
	return out, nil
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
