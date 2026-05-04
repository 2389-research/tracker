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

# SCOPE OF RESPONSIBILITY (read this first — what's yours vs what's already pinned)

You operate inside a two-layer pipeline:

- **Cross-sprint architect (Opus)** wrote the project-wide ` + "`contract.md`" + ` you receive on every call. The contract pins everything that crosses sprint boundaries: stack + runtime, project-wide conventions, the full data model (every Enum, ORM model, and Pydantic schema with field signatures), the cross-sprint type/symbol ownership map, the file-ownership map (which sprint owns which files; which files are FROZEN), the cross-sprint dependency edges, mandatory rules, and tricky semantics that span sprints. The contract is the truth.
- **You (Sonnet, within-sprint writer)** expand ONE per-sprint slice into the full enriched spec. Your job is to translate the per-sprint description plus the relevant slice of the contract into a SPRINT-NNN.md the local generator can transcribe deterministically.

What this means in practice:

- **Do NOT redefine cross-sprint shapes.** When sprint 002 needs ` + "`Note`" + ` and ` + "`Tag`" + `, the ` + "`## Data contract`" + ` section says "no new types — referenced from sprint 001" and points back to the contract. You don't repeat the field signatures.
- **Do NOT invent cross-sprint conventions.** If the contract pins flat error shape ` + "`{\"detail\": str, \"error_code\": str}`" + `, that's the rule. Your tests assert ` + "`body[\"error_code\"]`" + `; you do not pick a different path.
- **Do NOT modify FROZEN files.** The contract lists them; for additive sprints, ` + "`## Modified files`" + ` reads ` + "`(none)`" + `.
- **DO** pull the relevant slice of contract conventions into ` + "`## Conventions (inherited from Sprint NNN — listed for tight feedback)`" + ` and the relevant slice of cross-sprint tricky semantics into ` + "`## Tricky semantics (load-bearing — read before writing routes)`" + ` so the local model has them on-hand without re-reading contract.md.
- **DO** declare new sprint-local types (request/response schemas the contract doesn't already cover, helper functions) in ` + "`## Data contract`" + ` and ` + "`## Algorithm`" + ` for THIS sprint only.

# REQUIRED SECTIONS (speedrun shape)

Every enriched sprint has these sections, in this order. Most are present every sprint; ` + "`## Verbatim files`" + ` is conditional.

1. ` + "`# Sprint NNN — <Title> (enriched spec)`" + ` — top-level heading. Foundation sprints use a descriptive title (e.g. "Sprint 001 — Foundation (full schema + auth, front-loaded)"). Additive sprints append "(additive)".
2. ` + "`## Scope`" + ` — 2-4 sentences. For additive sprints, explicitly note: "Purely additive — no edits to any sprint NNN file."
3. ` + "`## Non-goals`" + ` — bullets of what this sprint does NOT include, with cross-references to which later sprint handles each excluded thing.
4. ` + "`## Dependencies`" + ` — for sprint 001: "None — first sprint." For sprints 002+: a tightly-scoped list of prior-sprint contracts this sprint imports, by exact symbol name. Header for additives: ` + "`## Dependencies (sprint NNN contracts this sprint imports — none get redefined here)`" + `.
5. ` + "`## Conventions`" + ` — sprint 001 declares them in full (module path, language version + package manager, web framework + ASGI server, ORM driver + style, validation lib, auth lib, test framework + asyncio mode, lint rules, ID type, error-raising convention, all timestamps timezone). Sprints 002+ use header ` + "`## Conventions (inherited from Sprint 001 — listed here for tight feedback)`" + ` and re-iterate the load-bearing ones (router declaration, AppError vs raw HTTPException, async def for routes/tests, fixtures-as-parameters, full-Python-statement imports, etc.).
6. ` + "`## Tricky semantics`" + ` — load-bearing. The most important section. Numbered list of project conventions where each rule has the rule itself + a brief WHY (often: the runtime symptom that occurs without it) + an optional code-pattern example. Sprint 001's master list covers cross-sprint conventions; sprints 002+ extract a sprint-relevant subset (e.g., "Cancelled registrations do NOT count toward shift capacity" for a registration-routes sprint). Use header ` + "`## Tricky semantics (load-bearing — READ BEFORE WRITING ANY CODE)`" + ` for sprint 001, ` + "`## Tricky semantics (load-bearing — read before writing routes)`" + ` for additive sprints.
7. ` + "`## Data contract`" + ` — sprint 001: full Enums + ORM models + Pydantic schemas with every field signature (` + "`Mapped[uuid.UUID] = mapped_column(primary_key=True, default=uuid.uuid4)`" + `, ` + "`server_default=func.now()`" + `, ` + "`lazy=\"selectin\"`" + ` on collection-side relationships, ` + "`back_populates`" + ` on both sides). Subsections: ` + "`### Enums (declared in app/models.py)`" + `, ` + "`### ORM models (declared in app/models.py, FROZEN after this sprint)`" + `, ` + "`### Pydantic schemas (declared in app/schemas.py, FROZEN after this sprint)`" + `. Sprints 002+ that introduce no new types: header ` + "`## Data contract (no new types — referenced from sprint 001)`" + ` followed by a one-paragraph note redirecting to sprint 001.
8. ` + "`## API contract`" + ` — the sprint's routes. A pipe-separated table with columns ` + "`Method | Path | Auth | Request | Response (status) | Errors`" + `. **The route order in this table MUST match the declaration order in the router file** (M-1: structural sections embody structural rules — see below). For routers with both static and parameterized paths, static comes first. Above the table, list the path/query parameter Python types ("` + "`location_id`" + `, ` + "`shift_id`" + ` are ` + "`uuid.UUID`" + `; ` + "`on_date`" + ` is ` + "`date | None = None`" + `") and the empty-string-vs-trailing-slash convention ("collection routes use ` + "`\"\"`" + `, not ` + "`\"/\"`" + `").
9. ` + "`## Algorithm`" + ` — per-route subsections (` + "`### `\\``POST /registrations`\\`` — `\\``create_registration`\\`" + `") with numbered prose steps. Each step names types, exceptions, and symbols by exact identifier — no English placeholders. For routes that construct a Read schema explicitly with derived fields, list the EXACT field set ("Construct ShiftRead with EXACTLY these fields and no others: id, location_id, ...; do NOT pass any other field"). Foundation sprints also have subsections for ` + "`### Enums`" + ` / ` + "`### ORM models`" + ` / ` + "`### Pydantic schemas`" + ` if those declarations need explanation beyond the data contract.
10. ` + "`## Test contract`" + ` — per-test-file tables (one table per file). Header sentence describes shared rules ("All tests are ` + "`async def`" + `. None have decorators. All take ` + "`client`" + ` from conftest as a parameter; auth-required routes also take ` + "`auth_headers`" + `."). Each table has columns ` + "`Test | Action | Asserts`" + `. Test names are explicit (` + "`test_register_full_shift_returns_409_capacity_full(client, auth_headers, test_shift, db_session)`" + `). Asserts state EXACT JSON paths matching the contract's pinned exception handler shape. When tests need a second instance with unique constraints, the EXACT distinguishing values are pinned ("Construct other_volunteer with email='other@example.com', phone='+15559999999'"). Sprint 001 also has a ` + "`### tests/conftest.py fixtures`" + ` subsection naming each fixture and its dependencies.
11. ` + "`## Verbatim files`" + ` — **conditional.** Present when the sprint owns tiny data-shaped files where exact text matters more than logic. Subsection per file (` + "`### `\\``backend/app/exceptions.py`\\`" + ` "). Includes ` + "`pyproject.toml`" + `, the auto-discovery ` + "`main.py`" + `, ` + "`exceptions.py`" + `, ` + "`config.py`" + `, ` + "`database.py`" + `, ` + "`scripts/init_db.py`" + ` for a foundation sprint. **Omit this section entirely if the sprint introduces no new tiny configs** (most additive sprints).
12. ` + "`## New files`" + ` — bullets, one per NEW file: ` + "`- `\\``backend/app/routers/notes.py`\\`` — short purpose. Imports (use these EXACT statements): ...`" + `. Sprint executors extract the FIRST backticked path of each bullet to drive code generation. The bullet body explicitly lists the imports (full Python statements, never bare module names — for class-vs-module collisions like ` + "`datetime`" + ` / ` + "`date`" + ` / ` + "`time`" + `, use ` + "`from X import Y`" + `).
13. ` + "`## Modified files`" + ` — bullets in same format. Use ` + "`(none — sprint 001's main.py auto-discovers ...)`" + ` for purely additive sprints.
14. ` + "`## Rules`" + ` — sprint-local constraints. Inherits the project-wide rules implicitly via contract.md. Restate the load-bearing within-sprint ones for tight feedback (path/query param typing, empty-string collection paths, route declaration order, schema field-set discipline, ` + "`uuid.UUID(...)`" + ` parsing of response IDs, ` + "`.isoformat()`" + ` / ` + "`str(...)`" + ` for JSON request bodies, fixtures-as-parameters).
15. ` + "`## DoD`" + ` — checkable items ` + "`- [ ] ...`" + ` (5-10 items, every item machine-verifiable; first item should be writing the failing test). Includes per-test-file pytest commands plus a "no FROZEN files modified" item for additive sprints.
16. ` + "`## Validation`" + ` — fenced bash code block with the exact shell commands that prove the sprint is done.

The two reference examples below show the foundation shape (sprint 001) and the additive shape (sprint 002). Match the shape and density of whichever applies to your sprint.

# DENSITY: signatures + algorithm prose vs verbatim full bodies

Two density modes are acceptable, picked by file type:

1. **Verbatim full text** (mode 1) — required ONLY for **tiny data-shaped files** where exact text matters more than logic and where the local generator cannot reliably invent the content: ` + "`pyproject.toml`" + `, ` + "`package.json`" + `, build-system manifests, framework config files (` + "`tsconfig.json`" + `, ` + "`alembic.ini`" + `), small exception/error-code constants files (` + "`exceptions.py`" + ` with ` + "`AppError`" + ` + ~10 error code constants), the auto-discovery app factory (` + "`main.py`" + ` with ` + "`pkgutil.iter_modules`" + `), small async runners (` + "`init_db.py`" + `), all empty package markers (` + "`__init__.py`" + `).

2. **Signatures + algorithm prose** (mode 2) — preferred for all substantive code: routers, services, business logic, test files, ORM models with non-trivial relationships. Provide every type signature in ` + "`## Data contract`" + `; provide every per-route or per-function logic flow in ` + "`## Algorithm`" + ` as numbered prose steps that name types/exceptions/symbols by exact identifier; provide per-file imports as full statements in the ` + "`## New files`" + ` bullets. The local model writes idiomatic bodies that match the contract.

The empirical reason for mode 2 (validated end-to-end on NIFB Apr 30 2026, 21/21 first-pass tests on sprint 001 + 26/26 on sprint 002 + 14/14 on sprint 003): when the architect writes verbatim bodies for a complex route handler, the bodies tend to carry stale unused imports, dead branches (` + "`if False`" + `), or impl-grade bugs that the local model faithfully transcribes. Numbered prose is more robust because the local model writes a clean body that obeys the contract.

**Rule of thumb: if a file is in ` + "`## Verbatim files`" + `, mode 1. Otherwise mode 2.** Never mode-1 a router, a service, an ORM model, or a test file. The contract pins which files belong in mode 1; if you're unsure, mode 2 is the safe default.

# TRIVIAL EMPTY FILES

Empty package markers (` + "`__init__.py`" + `, ` + "`mod.rs`" + `, ` + "`.gitkeep`" + `) MUST have a verbatim block in ` + "`## Verbatim files`" + ` even though they're empty:

` + "```" + `markdown
### Trivial file contents

**` + "`backend/app/__init__.py`" + `** — empty file. Content:
` + "```python" + `
` + "```" + `

**` + "`backend/app/routers/__init__.py`" + `** — empty file. Content:
` + "```python" + `
` + "```" + `
` + "```" + `

If the trivial file truly is empty, the inner code block has zero lines between fences. The local model copies the literal content. Never describe trivial files as "empty package init" or "package marker" without the verbatim block — that wording forces the model to make a decision and is the failure mode this rule prevents.

# NEW vs MODIFIED FILE SPLIT (LOAD-BEARING)

The sprint executor decides per-file behavior from the section the file appears in:
- ` + "`## New files`" + ` — executor calls a "generate from scratch" code path; if the file already exists it is SKIPPED (won't overwrite prior sprints' work).
- ` + "`## Modified files`" + ` — executor reads the existing file and asks the model to apply ONLY the changes described under this section, returning the full updated file.

Misclassifying a modified file as new will cause the executor to overwrite a prior sprint's contribution. Misclassifying a new file as modified will fail because there's no existing file to read. The architect's per-sprint description tells you which category each file belongs to (and the contract.md file-ownership map confirms it). Follow strictly.

For sprints under the front-loaded foundation pattern, additive sprints typically have ` + "`## Modified files: (none — sprint 001's main.py auto-discovers ...)`" + ` because they only drop new files in ` + "`app/routers/`" + ` and ` + "`tests/`" + `. ALWAYS prefer additive over modify when the architecture allows.

Bullets in both sections share the same format. The executor's parser takes the FIRST backtick-wrapped token as the path:

` + "`- `\\``backend/app/routers/notes.py`\\`` — short purpose. Imports (use these EXACT statements): import uuid, from fastapi import APIRouter, Depends`" + `

Other inline backticks in the descriptive tail are fine; the parser only takes the first.

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

# COMPLETE REFERENCE EXAMPLES (foundation + additive)

Two reference sprints from a validated NIFB run (Apr 30 2026; sprint 001 generated 21/21 first-pass tests, sprint 002 generated 26/26). The first is the foundation sprint shape (front-loaded — every ORM model, every Pydantic schema, every error code, every fixture, plus the working slice for the first feature). The second is an additive sprint shape (new router files only; FROZEN sprint-001 files unchanged; conventions and tricky semantics inherited with brief redirect notes).

Match the section names, ordering, density, and use of fenced code blocks shown below. Foundation sprints look like the first example; additive sprints (sprints 002+ in front-loaded designs) look like the second. If the architect's per-sprint description marks this as a foundation sprint, follow example 1; otherwise follow example 2. Adapt language idioms (Python here; Go/TS/Rust as the contract specifies).

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

## Non-overlap rule (load-bearing for sequential block apply)

Blocks are applied in source order against the evolving draft. A block emitted later cannot find its anchor if an earlier block has already replaced that text. Avoid this:

- **No two blocks may have overlapping SEARCH regions.** If two of your fixes target the same lines (or adjacent lines whose context surrounds both), MERGE them into a single SR block whose REPLACE contains both fixes.
- **No block's SEARCH may include text that another block's REPLACE will produce.** This shouldn't happen in normal patching, but watch for it when you patch a Rule and a Table that share wording.
- **Keep blocks small and targeted.** A block that spans 20 lines is far more likely to overlap with sibling blocks than one that spans 5 lines. Prefer narrow context windows (3-5 lines around the change) over broad ones.

If you find yourself emitting many blocks for the same section (e.g., 4 separate edits to one Algorithm subsection), strongly consider rewriting that subsection in a single block: SEARCH the original subsection in full, REPLACE with the corrected version. One bigger block is more likely to apply cleanly than four small blocks fighting over overlapping anchors.

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
func (t *WriteEnrichedSprintTool) LoadContract(contractFile string) (string, error) {
	if contractFile == "" {
		contractFile = ".ai/contract.md"
	}
	if !filepath.IsAbs(contractFile) && t.workDir != "" {
		contractFile = filepath.Join(t.workDir, contractFile)
	}
	data, err := os.ReadFile(contractFile)
	if err != nil {
		return "", fmt.Errorf("read contract from %s: %w (write the contract file first)", contractFile, err)
	}
	return string(data), nil
}

// resolveOutputDir applies the same defaulting rules Execute uses.
func (t *WriteEnrichedSprintTool) resolveOutputDir(outputDir string) string {
	if outputDir == "" || outputDir == "." {
		outputDir = t.workDir
	}
	if outputDir == "" {
		outputDir = "."
	}
	if !filepath.IsAbs(outputDir) && t.workDir != "" {
		outputDir = filepath.Join(t.workDir, outputDir)
	}
	return outputDir
}

// RunOne authors + audits + writes one sprint file. Caller supplies the contract
// (already loaded) and a fully-resolved output directory. Returns a structured
// result so callers (Execute, dispatch_sprints) can format their own summaries.
func (t *WriteEnrichedSprintTool) RunOne(ctx context.Context, contract, path, description, outputDir string) (*SprintRunResult, error) {
	if path == "" {
		return nil, fmt.Errorf("path is required")
	}
	if description == "" {
		return nil, fmt.Errorf("description is required")
	}

	authorSystemPrompt := sprintSystemPromptHeader + enrichedSprintExample

	authorUserPrompt := fmt.Sprintf(
		"CONTRACT (project-wide architectural map shared across all sprints):\n\n%s\n\n"+
			"SPRINT TO WRITE: %s\n\n"+
			"Per-sprint description from the architect:\n%s\n\n"+
			"Write the complete enriched sprint markdown for the file above. "+
			"Output ONLY the raw markdown — first line must be the `# Sprint NNN — Title (enriched spec)` heading.",
		contract, path, description,
	)

	// PASS 1 — Author. Produces the draft.
	// MaxTokens 16384 accommodates foundation sprints with full data contracts
	// (e.g., NIFB SPRINT-001's 18 ORM models + 25 schemas + verbatim conftest +
	// per-route algorithm prose hits ~15K output tokens). The provider default
	// (~4-8K depending on model) is too small for these and silently truncates.
	authorMaxTokens := 16384
	authorResp, err := t.client.Complete(ctx, &llm.Request{
		Model:     t.model,
		Provider:  t.provider,
		MaxTokens: &authorMaxTokens,
		Messages: []llm.Message{
			{Role: llm.RoleSystem, Content: []llm.ContentPart{{Kind: llm.KindText, Text: authorSystemPrompt}}},
			llm.UserMessage(authorUserPrompt),
		},
	})
	if err != nil {
		return nil, fmt.Errorf("author pass failed for %s: %w", path, err)
	}

	draft := trimEnclosingMarkdownFence(authorResp.Text())

	// PASS 2 — Auditor. Reviews the draft against the within-sprint patterns; either
	// returns the draft verbatim (AUDIT-VERDICT: PASS) or a patched version (AUDIT-VERDICT: PATCHED).
	// Different system prompt narrows the auditor's role; lower temperature makes the
	// audit deterministic.
	auditTemperature := 0.2
	// MaxTokens 16384 — for PATCHED verdicts the audit may emit several SR
	// blocks; for PASS it's a single line. Headroom accommodates large patch
	// sets without truncating mid-block.
	auditMaxTokens := 16384
	auditUserPrompt := fmt.Sprintf(
		"CONTRACT (project-wide architectural map shared across all sprints):\n\n%s\n\n"+
			"SPRINT BEING AUDITED: %s\n\n"+
			"Per-sprint description from the architect:\n%s\n\n"+
			"DRAFT (the author's first attempt) — audit this against the within-sprint patterns:\n\n"+
			"---BEGIN DRAFT---\n%s\n---END DRAFT---\n\n"+
			"Output: first line `AUDIT-VERDICT: PASS` (return draft verbatim) OR `AUDIT-VERDICT: PATCHED` (return patched version). "+
			"Second line must be the `# Sprint NNN — Title (enriched spec)` heading. No commentary outside the markdown.",
		contract, path, description, draft,
	)

	auditResp, err := t.client.Complete(ctx, &llm.Request{
		Model:       t.model,
		Provider:    t.provider,
		Temperature: &auditTemperature,
		MaxTokens:   &auditMaxTokens,
		Messages: []llm.Message{
			{Role: llm.RoleSystem, Content: []llm.ContentPart{{Kind: llm.KindText, Text: auditSystemPrompt}}},
			llm.UserMessage(auditUserPrompt),
		},
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "write_enriched_sprint: audit pass failed for %s (using draft as-is): %v\n", path, err)
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
			fmt.Fprintf(os.Stderr, "write_enriched_sprint: audit response malformed for %s (using draft as-is): %v\n", path, parseErr)
			verdict = "PASS-FALLBACK-MALFORMED"
		} else {
			verdict = v
			if v == "PATCHED" && len(blocks) > 0 {
				// Partial-apply: each block is independent. Some may fail (e.g.,
				// later block's SEARCH was modified by an earlier block's REPLACE);
				// we still ship whatever applied. Only fall back to draft if
				// ZERO blocks could be applied.
				patched, n, skipped := applySRBlocks(draft, blocks)
				for _, s := range skipped {
					fmt.Fprintf(os.Stderr, "write_enriched_sprint: %s for %s (skipped, partial apply continues)\n", s, path)
				}
				if n == 0 {
					verdict = "PASS-FALLBACK-NOMATCH"
				} else {
					final = patched
					patchesApplied = n
					if n < len(blocks) {
						verdict = "PATCHED-PARTIAL"
					}
				}
			}
		}
		auditInTokens = auditResp.Usage.InputTokens
		auditOutTokens = auditResp.Usage.OutputTokens
	}

	fullPath := filepath.Join(outputDir, path)
	if err := writeSprintFile(fullPath, final); err != nil {
		return nil, err
	}

	return &SprintRunResult{
		Path:           path,
		Bytes:          len(final),
		Verdict:        verdict,
		PatchesApplied: patchesApplied,
		Model:          t.model,
		AuthorIn:       authorResp.Usage.InputTokens,
		AuthorOut:      authorResp.Usage.OutputTokens,
		AuditIn:        auditInTokens,
		AuditOut:       auditOutTokens,
	}, nil
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
	outputDir := t.resolveOutputDir(args.OutputDir)

	r, err := t.RunOne(ctx, contract, args.Path, args.Description, outputDir)
	if err != nil {
		return "", fmt.Errorf("write_enriched_sprint: %w", err)
	}
	return fmt.Sprintf("Wrote %s (%d bytes, audit=%s, patches=%d). Model: %s. Tokens: author %d in / %d out, audit %d in / %d out.",
		r.Path, r.Bytes, r.Verdict, r.PatchesApplied, r.Model,
		r.AuthorIn, r.AuthorOut, r.AuditIn, r.AuditOut), nil
}

// srBlock is one Aider-style SEARCH/REPLACE patch.
type srBlock struct {
	Search  string
	Replace string
}

// parseAuditResponse parses the auditor's output. The verdict line
// `AUDIT-VERDICT: PASS|PATCHED` may appear anywhere in the first ~10 lines —
// not just the literal first line — to tolerate models that prepend a brief
// preamble ("Looking at the draft...", "After review..."), a markdown fence,
// or a leading blank line despite the prompt's instructions.
//
// For PATCHED, everything AFTER the verdict line is parsed as Aider-style
// SEARCH/REPLACE blocks:
//
//	<<<<<<< SEARCH
//	<old text>
//	=======
//	<new text>
//	>>>>>>> REPLACE
//
// Returns the verdict, the parsed blocks, and an error if no verdict line is
// found in the first 10 lines or the verdict value is unrecognized.
func parseAuditResponse(s string) (verdict string, blocks []srBlock, err error) {
	trimmed := strings.TrimSpace(s)
	if trimmed == "" {
		return "", nil, fmt.Errorf("empty audit response")
	}

	// Strip a single enclosing markdown fence, e.g. ```\n...\n```
	trimmed = trimEnclosingMarkdownFence(trimmed)

	// Search for the verdict line within the first ~10 non-empty lines.
	const verdictPrefix = "AUDIT-VERDICT:"
	const verdictSearchLines = 10
	lines := strings.Split(trimmed, "\n")
	verdictIdx := -1
	scanned := 0
	for i, ln := range lines {
		stripped := strings.TrimSpace(ln)
		if stripped == "" {
			continue
		}
		// Accept the verdict prefix even when wrapped in inline backticks or
		// bold/italic markdown (e.g., **AUDIT-VERDICT: PASS**, `AUDIT-VERDICT: PATCHED`).
		bare := strings.TrimFunc(stripped, func(r rune) bool {
			return r == '`' || r == '*' || r == '_'
		})
		if strings.HasPrefix(bare, verdictPrefix) {
			verdictIdx = i
			break
		}
		scanned++
		if scanned >= verdictSearchLines {
			break
		}
	}
	if verdictIdx == -1 {
		return "", nil, fmt.Errorf("no %q line found in first %d non-empty lines", verdictPrefix, verdictSearchLines)
	}

	// Extract verdict value, trimming markdown decorations.
	verdictLine := strings.TrimSpace(lines[verdictIdx])
	verdictLine = strings.TrimFunc(verdictLine, func(r rune) bool {
		return r == '`' || r == '*' || r == '_'
	})
	verdict = strings.TrimSpace(strings.TrimPrefix(verdictLine, verdictPrefix))
	// Some models append trailing markdown decoration after the verdict word.
	verdict = strings.TrimFunc(verdict, func(r rune) bool {
		return r == '`' || r == '*' || r == '_' || r == '.'
	})
	verdict = strings.TrimSpace(verdict)
	if verdict != "PASS" && verdict != "PATCHED" {
		return "", nil, fmt.Errorf("unrecognized verdict %q (must be PASS or PATCHED)", verdict)
	}
	if verdict == "PASS" {
		return "PASS", nil, nil
	}

	// PATCHED — parse SR blocks from everything after the verdict line.
	if verdictIdx+1 >= len(lines) {
		return verdict, nil, fmt.Errorf("PATCHED verdict but no body after verdict line")
	}
	body := strings.Join(lines[verdictIdx+1:], "\n")
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

// applySRBlocks applies the given SEARCH/REPLACE blocks to the draft in source
// order, matching each block via a series of fallback strategies (mirrors
// pipelines/lib/merge_sr.py): exact substring match → indent-preserving →
// whitespace-insensitive → fuzzy (Levenshtein-ratio ≥ 0.9). The first strategy
// that finds a match wins; subsequent strategies are not tried for that block.
//
// Partial-apply semantics: each block is applied independently against the
// current state. A block that fails to match (no strategy succeeds) is logged
// and skipped; subsequent blocks continue. This trades the all-or-nothing
// safety of strict apply for resilience to sequential overlap (block N's
// SEARCH anchored on text changed by block M<N).
//
// Returns the patched text, the count of successfully applied blocks, and a
// slice of "block %d: <reason>" strings for blocks that were skipped. If zero
// blocks applied, the caller can treat that as "no patch happened" and fall
// back to the unaudited draft.
func applySRBlocks(draft string, blocks []srBlock) (patched string, applied int, skipped []string) {
	out := draft
	for i, b := range blocks {
		if b.Search == "" {
			skipped = append(skipped, fmt.Sprintf("block %d: empty SEARCH text", i))
			continue
		}
		matched := false
		for _, st := range srMatchStrategies {
			next, ok := st.fn(out, b.Search, b.Replace)
			if ok {
				out = next
				matched = true
				applied++
				break
			}
		}
		if !matched {
			skipped = append(skipped, fmt.Sprintf("block %d: SEARCH text not found (tried exact, indent, whitespace, fuzzy)", i))
		}
	}
	return out, applied, skipped
}

type srMatchStrategy struct {
	name string
	fn   func(content, search, replace string) (string, bool)
}

// srMatchStrategies are tried in order. Indent precedes whitespace so that
// uniform-indent SEARCH blocks get their outer indent re-applied to REPLACE,
// rather than the whitespace strategy matching first and substituting REPLACE
// verbatim (which would lose the chunk's outer indent).
var srMatchStrategies = []srMatchStrategy{
	{"exact", trySRExact},
	{"indent", trySRIndent},
	{"whitespace", trySRWhitespace},
	{"fuzzy", trySRFuzzy},
}

// trySRExact returns content with the first occurrence of search replaced by
// replace, or false if search isn't present.
func trySRExact(content, search, replace string) (string, bool) {
	if !strings.Contains(content, search) {
		return content, false
	}
	return strings.Replace(content, search, replace, 1), true
}

// trySRWhitespace matches search against any N-line window of content after
// collapsing runs of whitespace on both sides, where N is the number of lines
// in search. Returns content with the matched chunk replaced verbatim by
// replace.
func trySRWhitespace(content, search, replace string) (string, bool) {
	needle := collapseWhitespace(search)
	if needle == "" {
		return content, false
	}
	contentLines := splitKeepNewlines(content)
	n := strings.Count(search, "\n") + 1
	if n > len(contentLines) {
		return content, false
	}
	for i := 0; i <= len(contentLines)-n; i++ {
		chunk := strings.Join(contentLines[i:i+n], "")
		if collapseWhitespace(chunk) == needle {
			return strings.Replace(content, chunk, replace, 1), true
		}
	}
	return content, false
}

// trySRIndent matches a dedented form of search against content's lines, then
// re-indents replace using the matched chunk's leading whitespace. Useful when
// the LLM emits SEARCH at one indent level and the draft has it at another
// (common when patching nested code or markdown).
func trySRIndent(content, search, replace string) (string, bool) {
	sIndent := commonLeadingIndent(search)
	if sIndent == "" {
		return content, false
	}
	dedentedSearch := dedentLines(search, sIndent)

	contentLines := splitKeepNewlines(content)
	n := strings.Count(search, "\n") + 1
	if n > len(contentLines) {
		return content, false
	}
	for i := 0; i <= len(contentLines)-n; i++ {
		chunk := strings.Join(contentLines[i:i+n], "")
		chunkIndent := commonLeadingIndent(chunk)
		if chunkIndent == "" {
			continue
		}
		dedentedChunk := dedentLines(chunk, chunkIndent)
		if strings.TrimRight(dedentedChunk, "\n") == strings.TrimRight(dedentedSearch, "\n") {
			// Apply the same indent offset to replace: dedent by sIndent (the
			// indent SEARCH was emitted with), then re-indent by chunkIndent
			// (the actual draft's indent). When sIndent == chunkIndent this is
			// a no-op; otherwise the offset shifts replace's content into the
			// chunk's column space.
			dedentedReplace := dedentLines(replace, sIndent)
			indentedReplace := indentLines(dedentedReplace, chunkIndent)
			chunkRStripped := strings.TrimRight(chunk, "\n")
			return strings.Replace(content, chunkRStripped, indentedReplace, 1), true
		}
	}
	return content, false
}

// trySRFuzzy slides an N-line window through content (N = lines in search),
// computes a Levenshtein-distance-based similarity ratio against search for
// each window, and accepts the highest-scoring window if its ratio ≥ 0.9.
// This catches cases where the LLM's SEARCH has minor character drift (typos,
// reformatted indentation, slightly-different wording) that the earlier
// strategies miss.
func trySRFuzzy(content, search, replace string) (string, bool) {
	const threshold = 0.9
	contentLines := splitKeepNewlines(content)
	n := strings.Count(search, "\n") + 1
	if n == 0 || n > len(contentLines) {
		return content, false
	}
	bestRatio := 0.0
	bestIdx := -1
	for i := 0; i <= len(contentLines)-n; i++ {
		chunk := strings.Join(contentLines[i:i+n], "")
		r := similarityRatio(chunk, search)
		if r > bestRatio {
			bestRatio = r
			bestIdx = i
		}
	}
	if bestIdx < 0 || bestRatio < threshold {
		return content, false
	}
	chunk := strings.Join(contentLines[bestIdx:bestIdx+n], "")
	rep := replace
	if !strings.HasSuffix(rep, "\n") && strings.HasSuffix(chunk, "\n") {
		rep += "\n"
	}
	return strings.Replace(content, chunk, rep, 1), true
}

// collapseWhitespace returns s with all runs of whitespace collapsed to a single
// space and leading/trailing whitespace stripped. Used for whitespace-insensitive
// SR-block matching.
func collapseWhitespace(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// splitKeepNewlines splits s on '\n' but keeps the trailing newline on each
// preceding element so concatenating the result reproduces s exactly. Used by
// the windowed SR-block strategies to slide N-line chunks through content.
func splitKeepNewlines(s string) []string {
	if s == "" {
		return nil
	}
	var lines []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			lines = append(lines, s[start:i+1])
			start = i + 1
		}
	}
	if start < len(s) {
		lines = append(lines, s[start:])
	}
	return lines
}

// commonLeadingIndent returns the longest sequence of spaces/tabs that prefixes
// every non-blank line in s.
func commonLeadingIndent(s string) string {
	lines := strings.Split(s, "\n")
	var nonEmpty []string
	for _, ln := range lines {
		if strings.TrimSpace(ln) != "" {
			nonEmpty = append(nonEmpty, ln)
		}
	}
	if len(nonEmpty) == 0 {
		return ""
	}
	prefix := nonEmpty[0]
	for _, ln := range nonEmpty[1:] {
		prefix = stringCommonPrefix(prefix, ln)
		if prefix == "" {
			return ""
		}
	}
	end := 0
	for end < len(prefix) && (prefix[end] == ' ' || prefix[end] == '\t') {
		end++
	}
	return prefix[:end]
}

// stringCommonPrefix returns the longest common prefix of a and b.
func stringCommonPrefix(a, b string) string {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	for i := 0; i < n; i++ {
		if a[i] != b[i] {
			return a[:i]
		}
	}
	return a[:n]
}

// dedentLines strips the given indent from the start of each line in s that
// begins with it, leaving other lines unchanged.
func dedentLines(s, indent string) string {
	if indent == "" {
		return s
	}
	lines := strings.Split(s, "\n")
	for i, ln := range lines {
		if strings.HasPrefix(ln, indent) {
			lines[i] = ln[len(indent):]
		}
	}
	return strings.Join(lines, "\n")
}

// indentLines prepends the given indent to each non-blank line of s.
func indentLines(s, indent string) string {
	if indent == "" {
		return s
	}
	lines := strings.Split(s, "\n")
	for i, ln := range lines {
		if ln != "" {
			lines[i] = indent + ln
		}
	}
	return strings.Join(lines, "\n")
}

// similarityRatio approximates Python's difflib.SequenceMatcher.ratio() using a
// Levenshtein-edit-distance-based ratio: 1 - dist/max(len(a), len(b)). For our
// use case (catching minor character drift in audit SR blocks) the two metrics
// agree closely on whether two strings are "approximately equal" at threshold
// 0.9.
func similarityRatio(a, b string) float64 {
	if a == b {
		return 1.0
	}
	la := len(a)
	lb := len(b)
	if la == 0 && lb == 0 {
		return 1.0
	}
	if la == 0 || lb == 0 {
		return 0.0
	}
	dist := levenshteinDistance(a, b)
	maxLen := la
	if lb > maxLen {
		maxLen = lb
	}
	return 1.0 - float64(dist)/float64(maxLen)
}

// levenshteinDistance returns the edit distance between a and b using a
// rolling two-row DP. O(len(a) * len(b)) time, O(min(la, lb)) space.
func levenshteinDistance(a, b string) int {
	ar := []rune(a)
	br := []rune(b)
	la := len(ar)
	lb := len(br)
	if la == 0 {
		return lb
	}
	if lb == 0 {
		return la
	}
	if la < lb {
		ar, br = br, ar
		la, lb = lb, la
	}
	prev := make([]int, lb+1)
	curr := make([]int, lb+1)
	for j := 0; j <= lb; j++ {
		prev[j] = j
	}
	for i := 1; i <= la; i++ {
		curr[0] = i
		for j := 1; j <= lb; j++ {
			cost := 1
			if ar[i-1] == br[j-1] {
				cost = 0
			}
			ins := curr[j-1] + 1
			del := prev[j] + 1
			sub := prev[j-1] + cost
			m := ins
			if del < m {
				m = del
			}
			if sub < m {
				m = sub
			}
			curr[j] = m
		}
		prev, curr = curr, prev
	}
	return prev[lb]
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
