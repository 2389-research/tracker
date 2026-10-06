package pipeline_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// pipeStressPreamble is the BASH_ENV preamble that makes the SIGPIPE race of
// #658 deterministic: it redefines bash's printf/echo builtins to emit one
// write(2) per line with a scheduler yield between lines, so any fixture
// assertion that pipes a multi-line producer into an early-exiting consumer
// (`printf '%s' "$OUT" | grep -q NEEDLE` under pipefail) fails every time,
// not one run in a few hundred under CI load.
const pipeStressPreamble = "../scripts/shell/pipe-stress.bash"

// pipeStressOptOut is the environment variable that disables the preamble
// (`TRACKER_SCRIPT_PIPE_STRESS=0`), e.g. to bisect a failure that only shows
// up with the amplifier on.
const pipeStressOptOut = "TRACKER_SCRIPT_PIPE_STRESS"

// pipeStressEnv appends BASH_ENV=<preamble> to a fixture environment unless
// the opt-out is set. Only the suite's own bash reads BASH_ENV; the scripts
// under test run via `sh` (dippin uses `sh -c`), which ignores it, so the
// behaviour under test is unchanged — only the suite's assertions are
// stressed. The preamble must exist: a moved file would silently turn the
// amplifier off.
func pipeStressEnv(t *testing.T, env []string) []string {
	t.Helper()
	if os.Getenv(pipeStressOptOut) == "0" {
		return env
	}
	abs, err := filepath.Abs(pipeStressPreamble)
	if err != nil {
		t.Fatalf("pipe-stress preamble path: %v", err)
	}
	if _, err := os.Stat(abs); err != nil {
		t.Fatalf("pipe-stress preamble missing (%v) — the fixture suites would run un-amplified", err)
	}
	return append(env, "BASH_ENV="+abs)
}

// runStressFixture writes body as a bash script and runs it under the
// preamble (or without it when withPreamble is false), returning trimmed
// combined output.
func runStressFixture(t *testing.T, body string, withPreamble bool) string {
	t.Helper()
	dir := t.TempDir()
	script := filepath.Join(dir, "fixture.sh")
	if err := os.WriteFile(script, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "bash", script)
	env := hermeticScriptEnv(os.Environ(), dir)
	if withPreamble {
		env = pipeStressEnv(t, env)
	}
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("fixture exited non-zero: %v\n%s", err, out)
	}
	return strings.TrimSpace(string(out))
}

// The known-bad shape from #658: 50 lines piped into grep -q, which exits on
// the first match; under pipefail the producer's SIGPIPE (141) wins and the
// helper answers "no" for a needle that is present.
const badFixture = `set -o pipefail
OUT="$(seq 1 50)"
printf '%s' "$OUT" | grep -qF -- 1 && echo yes || echo no
`

// The same assertion rewritten pipe-free (the test_helpers.sh contains()
// shape): no producer process, so nothing to SIGPIPE.
const goodFixture = `set -o pipefail
OUT="$(seq 1 50)"
case "$OUT" in *"1"*) echo yes ;; *) echo no ;; esac
`

// TestPipeStressPreambleReproducesSIGPIPE proves the amplifier works: the
// bad fixture prints "no" under the preamble (deterministically — every run),
// while the pipe-free rewrite prints "yes". Without this the harness could
// silently stop stressing the suites (preamble edited, BASH_ENV dropped) and
// the migration guard would be gone.
func TestPipeStressPreambleReproducesSIGPIPE(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skipf("bash not on PATH: %v", err)
	}
	t.Setenv(pipeStressOptOut, "")
	for i := 0; i < 5; i++ {
		if got := runStressFixture(t, badFixture, true); got != "no" {
			t.Fatalf("run %d: bad fixture under the preamble printed %q, want \"no\" — the SIGPIPE amplifier is not biting", i, got)
		}
	}
	if got := runStressFixture(t, goodFixture, true); got != "yes" {
		t.Fatalf("pipe-free fixture under the preamble printed %q, want \"yes\"", got)
	}
	// The preamble must not change what well-formed output looks like.
	const shapes = `printf 'a\nb\n'; printf '%s' x; echo; echo -n y; echo; printf '%d-%b\n' 7 'c\td'; printf -v v '%s' hi; printf '%s\n' "$v"`
	if got := runStressFixture(t, shapes, true); got != "a\nb\nx\ny\n7-c\td\nhi" {
		t.Fatalf("preamble altered printf/echo output: %q", got)
	}
}

func TestPipeStressEnvOptOut(t *testing.T) {
	base := []string{"PATH=/usr/bin"}
	t.Setenv(pipeStressOptOut, "0")
	if got := pipeStressEnv(t, base); len(got) != 1 {
		t.Fatalf("opt-out should leave the env alone, got %v", got)
	}
	t.Setenv(pipeStressOptOut, "")
	got := pipeStressEnv(t, base)
	if len(got) != 2 || !strings.HasPrefix(got[1], "BASH_ENV=") || !strings.HasSuffix(got[1], "pipe-stress.bash") {
		t.Fatalf("expected BASH_ENV pointing at the preamble, got %v", got)
	}
}
