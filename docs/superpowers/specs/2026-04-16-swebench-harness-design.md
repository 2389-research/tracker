# SWE-bench Harness for Tracker Agent

**Date:** 2026-04-16
**Status:** Draft
**Goal:** Benchmark tracker's code agent (Layer 2) against SWE-bench Lite, producing standardized predictions for the official evaluator.

## Overview

A standalone Go binary (`cmd/tracker-swebench/`) that runs tracker's `agent.Session` against SWE-bench Lite instances inside Docker containers, producing a `predictions.jsonl` file compatible with the official `swebench` Python evaluator.

This benchmarks the agent layer discretely — no pipeline orchestration, no TUI, no `.dip` files. Pure Layer 2 agent performance on real-world GitHub issues.

## Architecture

```
┌─────────────────────────────────────────────────┐
│  tracker-swebench (Go binary, runs on host)     │
│                                                  │
│  1. Read instance from SWE-bench Lite JSONL      │
│  2. Spin up Docker container                     │
│  3. Copy agent-runner binary into container       │
│  4. docker exec → agent runs inside container    │
│  5. docker exec → git diff → capture patch       │
│  6. Append to predictions.jsonl                  │
│  7. Tear down container                          │
│  8. Repeat (sequential)                          │
└─────────────────────────────────────────────────┘
                    ▼
        predictions.jsonl (standard format)
                    ▼
    swebench Python evaluator (official, separate)
```

## Two Binaries

### Orchestrator: `cmd/tracker-swebench/main.go`

Runs on the host. Manages the full lifecycle: reading the dataset, Docker container orchestration, collecting patches, writing results.

**CLI interface:**

```
tracker-swebench run \
  --dataset ./swebench_lite.jsonl \
  --model claude-sonnet-4-6 \
  --provider anthropic \
  --gateway-url https://bedrock-gateway.2389-research-inc.workers.dev \
  --output ./predictions.jsonl \
  --max-turns 50 \
  --timeout 10m \
  --instance django__django-11099   # optional: single instance
  --force                           # optional: re-run completed instances
```

**Flags:**

| Flag | Default | Description |
|------|---------|-------------|
| `--dataset` | (required) | Path to SWE-bench Lite JSONL file |
| `--model` | `claude-sonnet-4-6` | Model name passed to agent |
| `--provider` | `anthropic` | Provider name (`anthropic`, `openai`) |
| `--gateway-url` | `""` | Cloudflare AI Gateway URL (sets `TRACKER_GATEWAY_URL`) |
| `--output` | `./predictions.jsonl` | Output predictions file (append-mode) |
| `--results-dir` | `./results` | Directory for logs and metadata |
| `--max-turns` | `50` | Agent turn ceiling per instance |
| `--timeout` | `10m` | Wall-clock timeout per instance |
| `--instance` | `""` | Run single instance by ID (for debugging) |
| `--force` | `false` | Re-run already-completed instances |
| `--docker-image` | `tracker-swebench-base` | Base Docker image name |

### Agent Runner: `cmd/tracker-swebench/agent-runner/main.go`

Runs inside the Docker container. Minimal binary that creates an `agent.Session` directly and runs it.

**Input (env vars):**

| Env Var | Description |
|---------|-------------|
| `SWEBENCH_INSTANCE` | The issue text / problem statement |
| `SWEBENCH_REPO_DIR` | Path to the checked-out repo (default `/workspace`) |
| `SWEBENCH_MODEL` | Model name |
| `SWEBENCH_PROVIDER` | Provider name |
| `SWEBENCH_MAX_TURNS` | Turn ceiling |
| `SWEBENCH_TIMEOUT` | Wall-clock timeout |
| `TRACKER_GATEWAY_URL` | Gateway URL (passed through from orchestrator) |
| `ANTHROPIC_API_KEY` | API key / CF AIG token (used by Anthropic SDK) |
| `OPENAI_API_KEY` | API key / CF AIG token (used by OpenAI SDK) |

**Output protocol:**
- Exit code 0 on success, non-zero on failure
- Last line of stdout is a JSON summary: `{"turns": 23, "input_tokens": 45000, "output_tokens": 3200, "duration_ms": 14200}`
- All other stdout/stderr is the agent event log (captured to per-instance log file)
- The orchestrator collects `git diff` separately via `docker exec` after the agent exits

**Agent session config:**

```go
agent.SessionConfig{
    Model:                flagModel,
    Provider:             flagProvider,
    MaxTurns:             maxTurns,
    CommandTimeout:       30 * time.Second,
    MaxCommandTimeout:    5 * time.Minute,
    ContextCompaction:    agent.CompactionAuto,
    CompactionThreshold:  0.7,
    ReflectOnError:       true,
    WorkingDir:           "/workspace",
    SystemPrompt:         swebenchSystemPrompt,
}
```

**Tools registered:** The standard coding agent set — `read`, `write`, `edit`, `glob`, `grep_search`, `bash`. No `spawn_agent`, no custom tools.

## System Prompt

```
You are an expert software engineer tasked with fixing a GitHub issue.

You have access to the repository at /workspace. The repository is already
checked out at the correct commit.

## Your task
Fix the issue described below. Make the minimal changes necessary to resolve
the issue. Do not refactor unrelated code.

## Approach
1. Read the issue carefully. Understand what's broken and what the expected behavior is.
2. Explore the relevant code. Use grep_search and glob to find the right files.
3. Write a fix. Make targeted edits — smallest diff that solves the problem.
4. Run the existing test suite to verify your fix doesn't break anything.
5. If there are specific test commands mentioned in the issue, run those.

## Rules
- Do NOT create new test files. The evaluation uses the repo's existing test suite.
- Do NOT modify test files unless the issue specifically requires it.
- Keep your changes minimal and focused.
- If you're unsure about the fix, read more code before editing.
```

## Docker Strategy

### Base Image

One generic base image (`tracker-swebench-base`) built from a Dockerfile in `cmd/tracker-swebench/`:

```dockerfile
FROM python:3.11-bookworm
RUN apt-get update && apt-get install -y \
    git build-essential curl wget \
    && rm -rf /var/lib/apt/lists/*
COPY agent-runner /usr/local/bin/agent-runner
```

The agent-runner binary is cross-compiled for `linux/amd64` and baked into the image.

### Per-Instance Setup

The orchestrator runs these steps via `docker exec` after container creation:

1. Clone the repo (or use cached bare clone via `--reference`)
2. `git checkout <base_commit>`
3. Install dependencies (`pip install -e .` or instance-specific setup)
4. Run the agent: `docker exec -e SWEBENCH_INSTANCE=... <container> agent-runner`
5. Capture: `docker exec <container> git -C /workspace diff`

### Repo Clone Caching

Many SWE-bench Lite instances share the same repo (e.g., 30+ django instances). The orchestrator maintains a host-side cache of bare git clones per repo. Each container bind-mounts the cache read-only and uses `git clone --reference /cache/<repo>` for fast setup.

Cache directory: `<results-dir>/repo-cache/`

### Container Lifecycle

```
docker create --name swe-<instance_id> <base_image>
docker start swe-<instance_id>
docker exec: git clone, checkout, pip install
docker exec: agent-runner (with env vars)
docker exec: git diff → capture patch
docker stop swe-<instance_id>
docker rm swe-<instance_id>
```

Containers are stateless and disposable. On failure: capture whatever partial patch exists, log the error, continue to the next instance.

### SWE-bench Dataset Fields

The orchestrator parses these fields from each JSONL instance:

| Field | Used By | Purpose |
|-------|---------|---------|
| `instance_id` | Orchestrator | Container naming, output keying, resumability |
| `repo` | Orchestrator | Git clone URL (`https://github.com/<repo>`) |
| `base_commit` | Orchestrator | `git checkout` target |
| `problem_statement` | Agent runner | The issue text passed as user input |
| `hints_text` | Agent runner | Appended to problem statement if non-empty |
| `version` | Orchestrator | Python package version for `pip install` |
| `environment_setup_commit` | Orchestrator | Commit for environment/dep setup |
| `FAIL_TO_PASS` | Not used | Evaluation-only (official evaluator handles this) |
| `PASS_TO_PASS` | Not used | Evaluation-only |
| `patch` | Not used | Gold patch (evaluation-only) |
| `test_patch` | Not used | Test patch (evaluation-only) |

The dataset is 300 test instances + 23 dev instances. The harness runs against the `test` split by default.

### Instance Environment Setup

The orchestrator uses `environment_setup_commit` and `version` to install dependencies:

1. Clone repo, checkout `base_commit`
2. If `environment_setup_commit` differs from `base_commit`, use it for `pip install -e .`
3. Fall back to `pip install -e .` at `base_commit` if no separate setup commit
4. Switch Python versions via pyenv if the `version` field indicates a mismatch with the base image

## LLM Routing

All LLM calls route through the Cloudflare Workers Bedrock Gateway at:

```
https://bedrock-gateway.2389-research-inc.workers.dev
```

The gateway accepts requests in native Anthropic and OpenAI SDK formats and routes them to AWS Bedrock. Authentication uses the standard SDK headers:

- Anthropic: `x-api-key` header (from `ANTHROPIC_API_KEY`)
- OpenAI: `Authorization: Bearer` header (from `OPENAI_API_KEY`)

The CF AIG token is set as the API key for whichever provider is in use. No special gateway header support needed — the gateway reads auth from the SDK's native headers.

**Configuration:**

```
TRACKER_GATEWAY_URL=https://bedrock-gateway.2389-research-inc.workers.dev
ANTHROPIC_API_KEY=<cf_aig_token>   # for --provider anthropic
OPENAI_API_KEY=<cf_aig_token>      # for --provider openai
```

**Target models:**

| Model | Provider | Bedrock Alias |
|-------|----------|---------------|
| `claude-sonnet-4-6` | `anthropic` | Routes to Anthropic Claude on Bedrock |
| `gpt-5.4` | `openai` | Routes to OpenAI model on Bedrock |

## Output Format

### predictions.jsonl

One JSON object per line, compatible with the official `swebench` evaluator:

```json
{"instance_id": "django__django-11099", "model_name_or_path": "claude-sonnet-4-6", "model_patch": "<unified diff>"}
```

### Results Directory

```
results/
  predictions.jsonl            # main output (append-only for resumability)
  run_meta.json                # model, provider, gateway, start time, flags
  logs/
    django__django-11099.log   # per-instance agent event log
    django__django-11133.log
    ...
```

Per-instance logs capture the agent's event stream for post-hoc debugging.

### run_meta.json

```json
{
  "model": "claude-sonnet-4-6",
  "provider": "anthropic",
  "gateway_url": "https://bedrock-gateway.2389-research-inc.workers.dev",
  "dataset": "swebench_lite",
  "max_turns": 50,
  "timeout": "10m",
  "started_at": "2026-04-16T10:00:00Z",
  "tracker_version": "v0.17.0",
  "agent_runner_commit": "abc123"
}
```

## Resumability

- `predictions.jsonl` is append-only
- On startup, the orchestrator reads existing entries and builds a set of completed `instance_id`s
- Completed instances are skipped unless `--force` is set
- Resuming a run: re-run the same command — picks up where it left off
- Interrupted runs produce valid partial results (the file is always in a valid state)

## Progress Reporting

**During run (stdout):**

```
[1/300] django__django-11099 ... 23 turns, 14.2s, patch: 47 lines
[2/300] django__django-11133 ... 18 turns, 9.8s, timeout (no patch)
[3/300] django__django-11179 ... 31 turns, 22.1s, patch: 12 lines
...
```

**Run summary (on completion or Ctrl+C):**

```
Completed: 287/300
Skipped (setup failure): 4
Timed out: 9
Patches produced: 274/300 (91.3%)
Total tokens: 12.4M input / 1.8M output
Estimated cost: $XX.XX
Output: ./predictions.jsonl
```

Token/cost tracking uses `llm.TokenTracker`. The agent-runner writes a summary JSON line to stdout after the session completes, which the orchestrator parses.

## Evaluation (Separate Step)

Evaluation uses the official `swebench` Python package:

```bash
pip install swebench
python -m swebench.harness.run_evaluation \
  --predictions_path ./results/predictions.jsonl \
  --swe_bench_tasks swebench/lite \
  --run_id tracker-sonnet-4.6
```

This is intentionally decoupled — the Go binary produces patches, the Python evaluator scores them. The evaluator handles its own Docker containers, conda environments, and test execution.

## Scope Boundaries

**In scope:**
- Orchestrator binary (`cmd/tracker-swebench/main.go`)
- Agent runner binary (`cmd/tracker-swebench/agent-runner/main.go`)
- Dockerfile for the base image
- SWE-bench Lite dataset parsing
- Docker container lifecycle management
- Patch extraction and predictions.jsonl output
- Per-instance logging
- Resumability via append-only output
- Progress reporting and run summaries

**Out of scope:**
- Evaluation (delegated to official `swebench` package)
- Parallel execution (sequential only for v1)
- Pipeline orchestration (this is Layer 2 only)
- Custom Docker images per repo (use generic base image)
- Leaderboard submission automation
