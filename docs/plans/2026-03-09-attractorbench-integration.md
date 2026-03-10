# AttractorBench Integration Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Benchmark tracker against AttractorBench in two ways: (A) direct conformance testing of tracker-conformance binary, and (B) tracker as a Harbor coding agent.

**Architecture:** Part A runs the AttractorBench verifier scripts directly on aibox01 against tracker's pre-built `tracker-conformance` binary. Part B creates a custom Harbor adapter that wraps tracker's agent loop in a DOT pipeline, so Harbor can benchmark tracker as a coding agent alongside claude-code/codex/gemini-cli.

**Tech Stack:** Go (tracker), Python (AttractorBench verifier/mock server/scoring), Harbor (agent eval framework), tmux (remote execution on aibox01)

---

## Part A: Direct Conformance Testing

### Task 1: Build tracker on aibox01

**Context:** The tracker repo is already cloned at `~/src/tracker` on aibox01. We need to build both binaries.

**Step 1: Build tracker binaries on aibox01**

Run on aibox01 (via tmux `attractor-evals` session):
```bash
cd ~/src/tracker && make build
```
Expected: `bin/tracker` and `bin/tracker-conformance` produced, exit 0.

**Step 2: Verify conformance binary exists and runs**

```bash
./bin/tracker-conformance --help 2>&1 || ./bin/tracker-conformance 2>&1 | head -5
```
Expected: Shows usage with subcommands (client-from-env, list-models, complete, etc.)

---

### Task 2: Create workspace that mirrors AttractorBench's expected layout

**Context:** AttractorBench's verifier expects files at specific paths: `/workspace/bin/conformance`, `/workspace/Makefile`, `/tests/`, `/logs/verifier/`. We replicate this structure locally.

**Files:**
- Create: `~/src/attractorbench-workspace/` (workspace root)
- Create: `~/src/attractorbench-workspace/Makefile`
- Symlink: `~/src/attractorbench-workspace/bin/conformance` → `~/src/tracker/bin/tracker-conformance`

**Step 1: Create workspace directory and symlink**

```bash
mkdir -p ~/src/attractorbench-workspace/bin
ln -sf ~/src/tracker/bin/tracker-conformance ~/src/attractorbench-workspace/bin/conformance
```

**Step 2: Create Makefile that builds tracker and runs its tests**

Write `~/src/attractorbench-workspace/Makefile`:
```makefile
.PHONY: build test

build:
	cd ~/src/tracker && make build
	mkdir -p bin
	ln -sf ~/src/tracker/bin/tracker-conformance bin/conformance

test:
	cd ~/src/tracker && make test
```

**Step 3: Verify the workspace layout**

```bash
ls -la ~/src/attractorbench-workspace/bin/conformance
~/src/attractorbench-workspace/bin/conformance 2>&1 | head -3
make -C ~/src/attractorbench-workspace build
```
Expected: Binary exists, is executable, shows usage, make build exits 0.

---

### Task 3: Create a local verifier script

**Context:** The AttractorBench `test.sh` uses hardcoded container paths (`/workspace`, `/tests`, `/logs`). We need a local adaptation that points to the right places on aibox01.

**Files:**
- Create: `~/src/attractorbench-workspace/run-verifier.sh`

**Step 1: Write the local verifier script**

Write `~/src/attractorbench-workspace/run-verifier.sh`:
```bash
#!/bin/bash
# Local AttractorBench verifier for tracker-conformance
# Mirrors the container test.sh but with local paths
set -uo pipefail
set +e

WORKSPACE="$HOME/src/attractorbench-workspace"
TESTS="$HOME/src/attractorbench/tasks/main/tests"
LOGS="$WORKSPACE/logs/verifier"

cleanup() {
  if [ -n "${MOCK_PID:-}" ]; then
    kill "$MOCK_PID" 2>/dev/null || true
  fi
}
trap cleanup EXIT

mkdir -p "$LOGS"
cd "$WORKSPACE"

# Kill anything on port 9999
fuser -k 9999/tcp 2>/dev/null || true
sleep 0.5

# Start mock LLM server
python3 "$TESTS/mock_server.py" >> "$LOGS/mock-server.log" 2>&1 &
MOCK_PID=$!

# Wait for readiness
for _ in {1..20}; do
  if curl -fsS http://localhost:9999/health >/dev/null 2>&1; then
    break
  fi
  sleep 0.5
done

if ! curl -fsS http://localhost:9999/health >/dev/null 2>&1; then
  echo "Mock LLM server failed to start" | tee -a "$LOGS/conformance.log"
  exit 1
fi

echo "Mock server running on port 9999"

# Phase 1: Build
echo "=== Phase 1: Build ===" | tee "$LOGS/build.log"
make build >> "$LOGS/build.log" 2>&1
BUILD_EXIT=$?
echo "Build exit code: $BUILD_EXIT" | tee -a "$LOGS/build.log"

# Phase 2: Self-test
echo "=== Phase 2: Self-test ===" | tee "$LOGS/self-test.log"
make test >> "$LOGS/self-test.log" 2>&1
SELFTEST_EXIT=$?
echo "Self-test exit code: $SELFTEST_EXIT" | tee -a "$LOGS/self-test.log"

# Phase 3: Conformance (per-tier)
export OPENAI_API_KEY=test-key
export OPENAI_BASE_URL=http://localhost:9999/v1
export ANTHROPIC_API_KEY=test-key
export ANTHROPIC_BASE_URL=http://localhost:9999
export GEMINI_API_KEY=test-key
export GEMINI_BASE_URL=http://localhost:9999

for TIER in 1 2 3; do
  echo "=== Phase 3: Tier $TIER Conformance ===" | tee "$LOGS/conformance_tier${TIER}.log"
  curl -fsS http://localhost:9999/requests/reset >/dev/null 2>&1 || true
  python3 "$TESTS/conformance/run_conformance.py" \
    --tier "$TIER" --suite full \
    --output "$LOGS/conformance_results_tier${TIER}.json" \
    >> "$LOGS/conformance_tier${TIER}.log" 2>&1
  echo "Tier $TIER exit: $?" | tee -a "$LOGS/conformance_tier${TIER}.log"
done

# Score
python3 "$TESTS/score.py" \
  --build-exit $BUILD_EXIT \
  --selftest-exit $SELFTEST_EXIT \
  --selftest-log "$LOGS/self-test.log" \
  --conformance-tier1 "$LOGS/conformance_results_tier1.json" \
  --conformance-tier2 "$LOGS/conformance_results_tier2.json" \
  --conformance-tier3 "$LOGS/conformance_results_tier3.json" \
  --output "$LOGS/reward.json"

echo ""
echo "=== RESULTS ==="
cat "$LOGS/reward.json"
echo ""
cat "$LOGS/reward_details.json" 2>/dev/null
```

**Step 2: Make executable**

```bash
chmod +x ~/src/attractorbench-workspace/run-verifier.sh
```

---

### Task 4: Run the conformance tests

**Step 1: Patch the conformance runner for local paths**

The `run_conformance.py` uses hardcoded `/workspace/bin/conformance`. We need to either symlink or set an env override. Check if it reads from env:
```bash
grep -n "CONFORMANCE_BIN\|/workspace" ~/src/attractorbench/tasks/main/tests/conformance/run_conformance.py | head -5
```

If hardcoded, create a symlink:
```bash
sudo mkdir -p /workspace/bin /logs/verifier /tests
sudo ln -sf ~/src/attractorbench-workspace/bin/conformance /workspace/bin/conformance
sudo ln -sf ~/src/attractorbench/tasks/main/tests/conformance /tests/conformance
```

Or alternatively, run the verifier script which handles paths.

**Step 2: Run the verifier**

```bash
cd ~/src/attractorbench-workspace && bash run-verifier.sh
```

Expected output: JSON with per-tier scores and composite score.

**Step 3: Analyze results**

Read the output files:
- `logs/verifier/reward.json` - composite score
- `logs/verifier/reward_details.json` - per-tier breakdown
- `logs/verifier/conformance_results_tier{1,2,3}.json` - individual test results

---

### Task 5: Fix conformance failures and iterate

**Context:** Based on the results from Task 4, identify which conformance tests fail and fix the tracker-conformance binary.

**Step 1: Read per-tier failure details**

```bash
cat logs/verifier/conformance_results_tier1.json | python3 -m json.tool | grep -A2 '"passed": false'
cat logs/verifier/conformance_results_tier2.json | python3 -m json.tool | grep -A2 '"passed": false'
cat logs/verifier/conformance_results_tier3.json | python3 -m json.tool | grep -A2 '"passed": false'
```

**Step 2: Fix failures in tracker codebase**

Edit files in `~/src/tracker/cmd/tracker-conformance/main.go` and related packages.

**Step 3: Rebuild and re-run**

```bash
cd ~/src/tracker && make build && cd ~/src/attractorbench-workspace && bash run-verifier.sh
```

**Step 4: Commit passing improvements**

```bash
cd ~/src/tracker && git add -A && git commit -m "fix(conformance): address attractorbench tier N failures"
```

Repeat Steps 1-4 until scores stabilize.

---

## Part B: Tracker as a Harbor Agent

### Task 6: Create Harbor adapter for tracker

**Context:** Harbor adapters live at `~/.local/share/uv/tools/harbor/lib/python3.12/site-packages/harbor/agents/installed/`. We need `tracker.py` and `install-tracker.sh.j2`.

**Files:**
- Create: `tracker.py` (Harbor adapter)
- Create: `install-tracker.sh.j2` (installer template)

**Step 1: Write the install template**

Write `install-tracker.sh.j2`:
```bash
#!/bin/bash
set -eu

# Install Go if not present
if ! command -v go &>/dev/null; then
  curl -fsSL https://go.dev/dl/go1.23.6.linux-amd64.tar.gz | tar -C /usr/local -xzf -
  export PATH="/usr/local/go/bin:$PATH"
fi

# Clone and build tracker
cd /tmp
git clone https://github.com/2389-research/tracker.git
cd tracker
make build

# Install binaries to PATH
cp bin/tracker /usr/local/bin/tracker
cp bin/tracker-conformance /usr/local/bin/tracker-conformance
```

**Step 2: Write the adapter**

Write `tracker.py`. Key responsibilities:
- Generate a DOT pipeline from the instruction text
- Run `tracker pipeline.dot --no-tui -w /workspace`
- Pass the model through via env vars

```python
import os
import shlex
from pathlib import Path

from harbor.agents.installed.base import BaseInstalledAgent, ExecInput
from harbor.models.agent.name import AgentName


class Tracker(BaseInstalledAgent):
    SUPPORTS_ATIF: bool = False

    @staticmethod
    def name() -> str:
        return "tracker"

    @property
    def _install_agent_template_path(self) -> Path:
        return Path(__file__).parent / "install-tracker.sh.j2"

    def create_run_agent_commands(self, instruction: str) -> list[ExecInput]:
        escaped = shlex.quote(instruction)

        # Determine provider and model from Harbor config
        provider = "anthropic"
        model = "claude-sonnet-4-6"
        if self.model_name:
            parts = self.model_name.split("/", 1)
            if len(parts) == 2:
                provider_map = {
                    "anthropic": "anthropic",
                    "openai": "openai",
                    "google": "gemini",
                }
                provider = provider_map.get(parts[0], parts[0])
                model = parts[1]
            else:
                model = self.model_name

        env = {
            "ANTHROPIC_API_KEY": os.environ.get("ANTHROPIC_API_KEY", ""),
            "OPENAI_API_KEY": os.environ.get("OPENAI_API_KEY", ""),
            "GEMINI_API_KEY": os.environ.get("GEMINI_API_KEY", ""),
            "TRACKER_PROVIDER": provider,
            "TRACKER_MODEL": model,
        }
        env = {k: v for k, v in env.items() if v}

        # Generate DOT pipeline and run tracker
        return [
            ExecInput(
                command=(
                    f'cat > /workspace/pipeline.dot << \'DOTEOF\'\n'
                    f'digraph AttractorBench {{\n'
                    f'  graph [\n'
                    f'    goal="Implement the system described in the instruction.",\n'
                    f'    default_max_retry=5\n'
                    f'  ];\n'
                    f'  Start [shape=Mdiamond];\n'
                    f'  Exit [shape=Msquare];\n'
                    f'  Implement [\n'
                    f'    shape=box,\n'
                    f'    label="Implement System",\n'
                    f'    llm_provider="{provider}",\n'
                    f'    llm_model="{model}",\n'
                    f'    reasoning_effort="high",\n'
                    f'    prompt={escaped}\n'
                    f'  ];\n'
                    f'  BuildTest [\n'
                    f'    shape=parallelogram,\n'
                    f'    label="Build and Test",\n'
                    f'    tool_command="cd /workspace && make build && make test"\n'
                    f'  ];\n'
                    f'  RunConformance [\n'
                    f'    shape=parallelogram,\n'
                    f'    label="Run Quick Conformance",\n'
                    f'    tool_command="cd /workspace && python3 /tests/conformance/run_conformance.py --tier 1 --suite quick 2>&1 || true"\n'
                    f'  ];\n'
                    f'  AnalyzeAndFix [\n'
                    f'    shape=box,\n'
                    f'    label="Analyze Failures and Fix",\n'
                    f'    llm_provider="{provider}",\n'
                    f'    llm_model="{model}",\n'
                    f'    reasoning_effort="high",\n'
                    f'    goal_gate=true,\n'
                    f'    retry_target="Implement",\n'
                    f'    prompt="Read the conformance test results. If all tests pass, return success. Otherwise analyze the failures, fix the code, rebuild, and return retry to re-run conformance."\n'
                    f'  ];\n'
                    f'  Start -> Implement;\n'
                    f'  Implement -> BuildTest;\n'
                    f'  BuildTest -> RunConformance [condition="outcome=success"];\n'
                    f'  BuildTest -> Implement [condition="outcome=fail"];\n'
                    f'  RunConformance -> AnalyzeAndFix;\n'
                    f'  AnalyzeAndFix -> Exit [condition="outcome=success"];\n'
                    f'  AnalyzeAndFix -> Implement [condition="outcome=retry"];\n'
                    f'}}\n'
                    f'DOTEOF\n'
                ),
                env=env,
            ),
            ExecInput(
                command=(
                    'tracker /workspace/pipeline.dot --no-tui -w /workspace '
                    '2>&1 | tee /logs/agent/tracker.txt'
                ),
                env=env,
            ),
        ]
```

**Step 3: Register the adapter with Harbor**

Copy both files to Harbor's installed agents directory:
```bash
cp tracker.py ~/.local/share/uv/tools/harbor/lib/python3.12/site-packages/harbor/agents/installed/
cp install-tracker.sh.j2 ~/.local/share/uv/tools/harbor/lib/python3.12/site-packages/harbor/agents/installed/
```

Register in Harbor's agent name enum (or use a string override if Harbor supports it).

---

### Task 7: Test the Harbor adapter with tier0 smoke test

**Step 1: Run tier0 with tracker agent**

```bash
cd ~/src/attractorbench
harbor run --path ./tasks/tier0-smoke-test --agent tracker \
  --model anthropic/claude-sonnet-4-6 --env docker --job-name tracker-tier0
```

**Step 2: Score the result**

```bash
uv run attractorbench score jobs/tracker-tier0
```

**Step 3: Debug and iterate**

If the run fails, check logs:
```bash
cat jobs/tracker-tier0/*/logs/agent/tracker.txt
cat jobs/tracker-tier0/*/logs/verifier/conformance.log
```

---

### Task 8: Run full benchmark and compare

**Step 1: Run the full benchmark**

```bash
harbor run --path ./tasks --agent tracker \
  --model anthropic/claude-sonnet-4-6 --env docker --job-name tracker-sonnet46-full
```

**Step 2: Score and compare**

```bash
uv run attractorbench score jobs/tracker-sonnet46-full
uv run attractorbench leaderboard jobs/tracker-sonnet46-full
```

**Step 3: Compare against existing leaderboard entries**

```bash
uv run attractorbench compare jobs/tracker-sonnet46-full
```

---

## Execution Order

1. **Tasks 1-4** (Part A) - Fast, free, immediate results. ~15 minutes.
2. **Task 5** (Part A iteration) - Fix failures based on results. Variable time.
3. **Tasks 6-7** (Part B setup) - Create adapter, test with smoke. ~30 minutes.
4. **Task 8** (Part B full run) - Full benchmark. ~25 min, ~$7-10 in API costs.
