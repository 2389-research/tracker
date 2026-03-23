# Glimpser-to-Go Porting Pipeline Design

## Summary

A commit-by-commit porting pipeline (DOT file) that walks the entire git history of the Python glimpser project and ports each commit's semantic changes into idiomatic Go in a new standalone repository.

## Source & Target

- **Source**: `/Users/harper/workspace/personal/glimpser` (Python/Flask surveillance platform)
- **Target**: `/Users/harper/workspace/personal/glimpser-go` (empty, standalone Go repo)
- **Go module**: `github.com/harperreed/glimpser-go`

## Working Directories

All pipeline nodes execute with the **glimpser-go repo as the working directory**. When nodes need to inspect the source repo, they must use absolute paths or `cd` into `/Users/harper/workspace/personal/glimpser` explicitly, then return. The `$goal` attribute includes both paths for reference.

## Porting Strategy

- Walk the entire git history of glimpser from the very first commit
- Process one commit per loop iteration
- Each commit is analyzed semantically, then either ported to idiomatic Go or acknowledged (skipped)
- The pipeline bootstraps the Go repo on first run if empty

## Model Assignments

| Role | Model | Provider |
|------|-------|----------|
| Analysis & Planning | Opus 4.6 (`claude-opus-4-6`) | Anthropic |
| Implementation & Testing | GPT-5.4 (`gpt-5.4`) | OpenAI |

## Pipeline Nodes

### Start → Bootstrap

The Bootstrap node runs first. If the Go repo is empty (no `go.mod`), it:

1. Runs `go mod init github.com/harperreed/glimpser-go`
2. Creates initial directory structure
3. Copies/creates `ledger.py` for commit tracking
4. Creates `ledger.tsv` (empty, with headers)
5. Creates `.ai/` directory for intermediate artifacts
6. Commits the bootstrap

If the repo already has `go.mod` AND `ledger.py`, this node passes through. If `go.mod` exists but `ledger.py` is missing, it creates only the missing pieces.

- **Model**: GPT-5.4 / OpenAI
- **Node type**: `stack.observe` (single default edge to FetchNextCommit)
- **is_codergen**: true
- **allow_partial**: false

### Node 1: Fetch & Identify Next Commit

Reads `ledger.tsv` for the earliest commit with disposition `new`.

If no `new` commits exist:
1. Scan glimpser git history (`git log --format='%h %cI' --reverse` in the glimpser repo)
2. Add any commits not already in the ledger via `python3 ledger.py add <shortsha> <timestamp>`
3. Sort with `python3 ledger.py sort`
4. Pick the earliest `new` commit

If fully caught up (no new commits after fetching): write completion report and exit.

- **Model**: Opus 4.6 / Anthropic
- **Node type**: `stack.steer`
- **is_codergen**: true
- **allow_partial**: false
- **Outcomes**: `process` → AnalyzePlanPort, `done` → Exit

### Node 2: Analyze & Plan Port

Examines the single commit in the glimpser repo using `git show`.

Analyzes:
- What functionality changed (semantic, not syntactic)
- Whether the change is relevant to a Go port (vs. Python-specific, docs-only, CI config, etc.)

**If acknowledging (skip)**:
1. Update ledger: `python3 ledger.py update <shortsha> acknowledged`
2. Commit the ledger change with descriptive message
3. Outcome: `skip` → loop back to FetchNextCommit

**If porting**:
1. Write `.ai/glimpser_plan_opus.md` with: commit info, semantic analysis, Go-idiomatic port plan with target package/file references, acceptance criteria
2. Outcome: `port` → FinalizePlan

- **Model**: Opus 4.6 / Anthropic
- **Node type**: `stack.steer`
- **is_codergen**: true
- **allow_partial**: false
- **Outcomes**: `port` → FinalizePlan, `skip` → FetchNextCommit (loop_restart)

### Node 3: Finalize Plan

Editorial pass on `.ai/glimpser_plan_opus.md`. Produces `.ai/glimpser_plan_finalized.md`.

Ensures:
- Concrete Go file paths and package targets
- Clear acceptance criteria per task
- No vague language — directly executable instructions
- Idiomatic Go patterns (interfaces, error returns, etc.)

- **Model**: GPT-5.4 / OpenAI
- **Node type**: `stack.observe`
- **is_codergen**: true
- **allow_partial**: false

### Node 4: Implement Port

Follows `.ai/glimpser_plan_finalized.md`. Writes idiomatic Go code.

Guidelines:
- Proper Go packages, interfaces, error handling
- No structural mirroring of Python layout
- Use standard library where possible, well-known libraries (chi, sqlx, etc.) where needed
- Log all changes to `.ai/glimpser_impl.log`

- **Model**: GPT-5.4 / OpenAI
- **Node type**: `stack.observe`
- **is_codergen**: true
- **allow_partial**: true
- **Timeout**: 2400s (large commits may need time)

### Node 5: Test/Validate

Runs:
1. `go build ./...` — compilation check
2. `go vet ./...` — static analysis
3. `go test ./...` — test execution

Verifies semantic equivalence with the upstream commit's intent.

Writes results to `.ai/glimpser_validation_report_NN.md`.

- **Model**: GPT-5.4 / OpenAI
- **Node type**: `stack.steer`
- **is_codergen**: true
- **allow_partial**: true
- **Outcomes**: `yes` (pass) → FinalizeAndUpdateLedger, `retry` (fail) → AnalyzeFailure

### Node 5a: Analyze Failure

Inspects validation reports, build errors, test failures. Writes `.ai/glimpser_failure_opus.md` with:
- Root causes
- Impacted files with line references
- Concrete fix recommendations

- **Model**: Opus 4.6 / Anthropic
- **Node type**: `stack.observe`
- **is_codergen**: true
- **allow_partial**: false

Flows into → ImplementPort for a retry attempt. The `default_max_retry="4"` controls how many times this retry sub-loop can execute before the pipeline fails the commit.

### Node 6: Finalize & Update Ledger

1. Synthesize implementation summary into `.ai/glimpser_implementation_summary.md`
2. Update ledger: `python3 ledger.py update <shortsha> implemented`
3. Sort ledger: `python3 ledger.py sort`
4. Verify with `python3 ledger.py stats`
5. Commit all changes (implementation + ledger update)
6. Loop back to FetchNextCommit

- **Model**: GPT-5.4 / OpenAI
- **Node type**: `stack.observe`
- **is_codergen**: true
- **allow_partial**: false

### Exit

- **Node type**: `exit`
- **Shape**: `doublecircle`

### Start

- **Node type**: `start`
- **Shape**: `circle`

Terminal node. Reached when all commits are processed.

## Pipeline Graph Edges

```
Start → Bootstrap
Bootstrap → FetchNextCommit
FetchNextCommit → AnalyzePlanPort [condition="outcome=process"]
FetchNextCommit → Exit [condition="outcome=done"]
AnalyzePlanPort → FinalizePlan [condition="outcome=port"]
AnalyzePlanPort → FetchNextCommit [condition="outcome=skip", loop_restart=true]
FinalizePlan → ImplementPort
ImplementPort → TestValidate
TestValidate → FinalizeAndUpdateLedger [condition="outcome=yes"]
TestValidate → AnalyzeFailure [condition="outcome=retry"]
AnalyzeFailure → ImplementPort (retry sub-loop, bounded by default_max_retry=4)
FinalizeAndUpdateLedger → FetchNextCommit [loop_restart=true]
```

## Ledger Tooling

A Python script (`ledger.py`) in the glimpser-go repo root, managing `ledger.tsv`.

### TSV Columns

| Column | Description |
|--------|-------------|
| `shortsha` | 7-char git hash from the glimpser (source) repo |
| `timestamp` | ISO 8601 commit date |
| `disposition` | `new`, `implemented`, `acknowledged` |
| `summary` | Brief commit description |

### Commands

- `python3 ledger.py add <shortsha> <timestamp> [summary]` — add a new entry with disposition `new` (summary auto-populated from git log if omitted)
- `python3 ledger.py update <shortsha> <disposition>` — change disposition
- `python3 ledger.py earliest` — print the earliest `new` entry
- `python3 ledger.py sort` — sort by timestamp
- `python3 ledger.py stats` — print counts by disposition

## Artifacts Directory

All intermediate files in `.ai/` within the glimpser-go repo:

- `.ai/glimpser_new_commits.md` — current commit being processed
- `.ai/glimpser_plan_opus.md` — analysis and port plan (from Opus)
- `.ai/glimpser_plan_finalized.md` — finalized plan (from GPT-5.4)
- `.ai/glimpser_impl.log` — implementation log
- `.ai/glimpser_validation_report_NN.md` — test/build results
- `.ai/glimpser_failure_opus.md` — failure analysis
- `.ai/glimpser_implementation_summary.md` — per-commit summary

## Pipeline Configuration

- `rankdir="LR"` — left-to-right layout
- `goal` — "Port the Python glimpser project at /Users/harper/workspace/personal/glimpser to idiomatic Go at /Users/harper/workspace/personal/glimpser-go (module github.com/harperreed/glimpser-go). Walk the git history commit-by-commit, analyze each commit semantically, and port relevant changes using Go idioms and standard patterns. Track disposition in ledger.tsv. The working directory is the glimpser-go repo."
- `context_fidelity_default="truncate"`
- `context_thread_default="glimpser-port"`
- `default_max_retry="4"` — applies per-node; if a node (e.g., TestValidate) exhausts retries, the pipeline halts for that commit
- `max_agent_turns="8"` per node
- `reasoning_effort="high"` for all nodes

## Key Design Decisions

1. **Commit-by-commit** preserves the incremental build-up of the codebase, making each ported piece reviewable
2. **Opus 4.6 for analysis** because planning and failure analysis benefit from stronger reasoning
3. **GPT-5.4 for implementation** because it excels at code generation with clear specifications
4. **Idiomatic Go from scratch** — no attempt to mirror Python's Flask/SQLAlchemy patterns; use Go-native approaches
5. **Bootstrap node** handles empty repo setup so the pipeline is self-contained
6. **Ledger tooling** copied from the semport pattern for consistency across tracker pipelines
