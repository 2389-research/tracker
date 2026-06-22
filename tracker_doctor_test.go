// ABOUTME: Tests for the Doctor library API — preflight health checks.
// ABOUTME: Verifies probe opt-in, provider detection, and pipeline validation.
package tracker

import (
	"bufio"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/2389-research/tracker/internal/dipxtest"
)

func TestKnownProvidersGatewayRefusalSurfaces(t *testing.T) {
	// Doctor's adapter probes must use the strict resolver: a refused
	// gateway route (bedrock kind defines no openai-compat suffix) must
	// surface as an error, not silently fall back to the public SDK
	// default endpoint while `tracker run` hard-fails on the same config.
	t.Setenv("OPENAI_COMPAT_BASE_URL", "")
	t.Setenv("TRACKER_GATEWAY_URL", "https://gw.example.com")
	t.Setenv("TRACKER_GATEWAY_KIND", "bedrock")

	var def *providerDef
	for i := range knownProviders {
		if knownProviders[i].name == "OpenAI-Compat" {
			def = &knownProviders[i]
			break
		}
	}
	if def == nil {
		t.Fatal("OpenAI-Compat provider def not found")
	}
	_, err := def.buildAdapter("test-key")
	if err == nil {
		t.Fatal("expected gateway-route-refused error, got working adapter")
	}
	if !errors.Is(err, ErrGatewayRouteRefused) {
		t.Errorf("error = %v, want ErrGatewayRouteRefused", err)
	}
}

func TestDoctor_NoProbe_KeyPresent(t *testing.T) {
	workdir := t.TempDir()
	t.Setenv("ANTHROPIC_API_KEY", "sk-ant-test-12345678901234567890")

	r, err := Doctor(context.Background(), DoctorConfig{WorkDir: workdir, ProbeProviders: false})
	if err != nil {
		t.Fatalf("Doctor: %v", err)
	}
	var providersCheck *CheckResult
	for i := range r.Checks {
		if r.Checks[i].Name == "LLM Providers" {
			providersCheck = &r.Checks[i]
		}
	}
	if providersCheck == nil {
		t.Fatal("LLM Providers check not found")
	}
	if providersCheck.Status != "ok" && providersCheck.Status != "skip" {
		t.Errorf("LLM Providers status = %q, want ok or skip", providersCheck.Status)
	}
}

func TestDoctor_NoProviderKeys(t *testing.T) {
	workdir := t.TempDir()
	for _, k := range []string{"ANTHROPIC_API_KEY", "OPENAI_API_KEY", "GEMINI_API_KEY", "GOOGLE_API_KEY", "OPENAI_COMPAT_API_KEY"} {
		t.Setenv(k, "")
	}

	r, err := Doctor(context.Background(), DoctorConfig{WorkDir: workdir, ProbeProviders: false})
	if err != nil {
		t.Fatalf("Doctor: %v", err)
	}
	if r.OK {
		t.Error("expected OK=false when no providers configured")
	}
}

func TestCheckProviders_ProbeProvidersTrueUsesProbeFunction(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "sk-ant-test-12345678901234567890")
	for _, k := range []string{"OPENAI_API_KEY", "GEMINI_API_KEY", "GOOGLE_API_KEY", "OPENAI_COMPAT_API_KEY"} {
		t.Setenv(k, "")
	}

	called := false
	orig := probeProviderFn
	probeProviderFn = func(_ context.Context, p providerDef, key string) (bool, string, bool) {
		called = true
		if p.name != "Anthropic" {
			t.Fatalf("unexpected provider probed: %s", p.name)
		}
		if key == "" {
			t.Fatal("expected non-empty key")
		}
		return true, "", false
	}
	t.Cleanup(func() { probeProviderFn = orig })

	r := checkProviders(context.Background(), true)
	if !called {
		t.Fatal("expected probe provider function to be called")
	}
	if r.Status != CheckStatusOK {
		t.Fatalf("status = %q, want ok", r.Status)
	}
}

func TestDoctor_PipelineFileValidation(t *testing.T) {
	workdir := t.TempDir()
	pf := filepath.Join(workdir, "ok.dip")
	const src = `workflow X
  goal: "x"
  start: S
  exit: E
  agent S
    label: "S"
    prompt: "hi"
  agent E
    label: "E"
    prompt: "bye"
  S -> E
`
	must(t, os.WriteFile(pf, []byte(src), 0o644))

	r, err := Doctor(context.Background(), DoctorConfig{WorkDir: workdir, PipelineFile: pf, ProbeProviders: false})
	if err != nil {
		t.Fatalf("Doctor: %v", err)
	}
	var pipelineCheck *CheckResult
	for i := range r.Checks {
		if r.Checks[i].Name == "Pipeline File" {
			pipelineCheck = &r.Checks[i]
		}
	}
	if pipelineCheck == nil {
		t.Fatal("Pipeline File check missing when PipelineFile set")
	}
}

// TestDoctor_PipelineFileBundle exercises the .dipx branch of checkPipelineFile.
// A real bundle is packed via dipxtest.PackTestBundle and handed to Doctor;
// the result must be a successful "Pipeline File" check with no warnings about
// unrecognized extensions or parse errors. Regression guard for the bug where
// doctor rejected .dipx as "not a .dip or .dot file" and then tried to parse
// the ZIP bytes as text.
func TestDoctor_PipelineFileBundle(t *testing.T) {
	workdir := t.TempDir()
	srcDir := t.TempDir()
	entryPath := filepath.Join(srcDir, "entry.dip")
	must(t, os.WriteFile(entryPath, []byte(dipxtest.MinimalDip("doctor_bundle", "start", "exit")), 0o644))
	bundlePath := dipxtest.PackTestBundle(t, entryPath)

	r, err := Doctor(context.Background(), DoctorConfig{WorkDir: workdir, PipelineFile: bundlePath, ProbeProviders: false})
	if err != nil {
		t.Fatalf("Doctor: %v", err)
	}
	var pipelineCheck *CheckResult
	for i := range r.Checks {
		if r.Checks[i].Name == "Pipeline File" {
			pipelineCheck = &r.Checks[i]
		}
	}
	if pipelineCheck == nil {
		t.Fatal("Pipeline File check missing when PipelineFile set to a .dipx")
	}
	// A valid bundle reports ok, or warn for advisory dippin lints unrelated to
	// this test's intent (e.g. v0.38.0's DIP144 "agent node has no failure
	// route" fires on dipxtest.MinimalDip's start node — an upstream fixture we
	// don't control). This test guards the .dipx parse path, not lint
	// cleanliness; the detail/message checks below pin the warnings that would
	// actually be a regression (extension or parse errors).
	if pipelineCheck.Status != CheckStatusOK && pipelineCheck.Status != CheckStatusWarn {
		t.Errorf("Pipeline File status = %q, want ok or warn; details=%+v message=%q",
			pipelineCheck.Status, pipelineCheck.Details, pipelineCheck.Message)
	}
	// The pre-fix warning leaked through as a detail or message — guard against it.
	for _, d := range pipelineCheck.Details {
		if strings.Contains(d.Message, "not a .dip") {
			t.Errorf("bundle should not produce extension warning, got detail: %q", d.Message)
		}
		if strings.Contains(d.Message, "parse error") {
			t.Errorf("bundle should not produce parse error (ZIP read as text), got detail: %q", d.Message)
		}
	}
	if strings.Contains(pipelineCheck.Message, "parse error") {
		t.Errorf("bundle should not produce parse error, got message: %q", pipelineCheck.Message)
	}
}

func TestSanitizeProviderError(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "anthropic key",
			in:   "auth failed: sk-ant-api03-abcdef1234567890abcdef",
			want: "auth failed: [redacted-key]",
		},
		{
			name: "openai key",
			in:   "invalid key sk-abcdef1234567890abcdef",
			want: "invalid key [redacted-key]",
		},
		{
			name: "google key",
			in:   "request failed AIzaSyAbcDef1234567890abcdef_01",
			want: "request failed [redacted-key]",
		},
		{
			name: "bearer token",
			in:   "401 Unauthorized: Bearer abc.def.ghi12345",
			want: "401 Unauthorized: Bearer [redacted]",
		},
		{
			name: "plain message",
			in:   "connection refused",
			want: "connection refused",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := sanitizeProviderError(c.in); got != c.want {
				t.Errorf("sanitizeProviderError(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

// TestSanitizeThenTrim_NoPartialKeyLeak verifies the sanitize-before-trim
// ordering in probeProvider. A key that straddles the trim boundary must
// not produce a leaked prefix after truncation. Regression guard for PR
// feedback on issue #106 follow-up.
func TestSanitizeThenTrim_NoPartialKeyLeak(t *testing.T) {
	// Construct a message where the key starts at char 50 and runs past
	// the 80-char truncation point. Trimming first would leave a 30-char
	// prefix of the key that's shorter than the regex minimum, so the
	// regex would miss it and the prefix would leak.
	key := "sk-ant-api03-" + strings.Repeat("A", 60)
	msg := strings.Repeat("x", 50) + key

	// Correct order: sanitize first, then trim.
	got := trimErrMsg(sanitizeProviderError(msg), 80)

	if strings.Contains(got, "sk-ant-") {
		t.Errorf("got = %q; leaked key prefix (must be redacted before trim)", got)
	}
	if !strings.Contains(got, "[redacted-key]") {
		t.Errorf("got = %q; want [redacted-key] substitution", got)
	}
}

// TestCheckWorkdir_DistinguishesErrorKinds verifies that permission-denied
// and other non-ENOENT stat failures are reported with the right remediation
// hint, rather than being reported as "does not exist" + an mkdir hint.
func TestCheckWorkdir_DistinguishesErrorKinds(t *testing.T) {
	t.Run("missing path", func(t *testing.T) {
		r := checkWorkdir(filepath.Join(t.TempDir(), "does-not-exist"))
		if r.Status != CheckStatusError {
			t.Fatalf("status = %q, want error", r.Status)
		}
		if !strings.Contains(r.Message, "does not exist") {
			t.Errorf("message = %q, want 'does not exist'", r.Message)
		}
		if !strings.Contains(r.Hint, "mkdir") {
			t.Errorf("hint = %q, want mkdir suggestion", r.Hint)
		}
	})
	t.Run("permission denied", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("permission semantics differ on Windows")
		}
		if os.Geteuid() == 0 {
			t.Skip("permission tests are meaningless as root")
		}
		parent := t.TempDir()
		inner := filepath.Join(parent, "locked")
		must(t, os.Mkdir(inner, 0o755))
		// Revoke search/execute on parent → stat of inner fails with EACCES.
		must(t, os.Chmod(parent, 0o000))
		t.Cleanup(func() { _ = os.Chmod(parent, 0o755) })

		r := checkWorkdir(inner)
		if r.Status != CheckStatusError {
			t.Fatalf("status = %q, want error", r.Status)
		}
		if strings.Contains(r.Hint, "mkdir") {
			t.Errorf("permission error should not suggest mkdir, got hint = %q", r.Hint)
		}
		if !strings.Contains(r.Message, "permission denied") &&
			!strings.Contains(r.Message, "cannot stat") {
			t.Errorf("message = %q, want permission-denied or stat-error wording", r.Message)
		}
	})
}

// TestCheckArtifactDirs_NonENOENTStatError verifies that a stat failure on
// .ai that is not ENOENT (e.g. permission denied) is reported as an error
// rather than silently treated as "will be created on first run".
func TestCheckArtifactDirs_NonENOENTStatError(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("permission semantics differ on Windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("permission tests are meaningless as root")
	}
	workdir := t.TempDir()
	aiDir := filepath.Join(workdir, ".ai")
	must(t, os.Mkdir(aiDir, 0o755))
	// Make workdir unreadable/unsearchable so stat on .ai fails with EACCES
	// (not ENOENT — .ai does exist).
	must(t, os.Chmod(workdir, 0o000))
	t.Cleanup(func() { _ = os.Chmod(workdir, 0o755) })

	r := checkArtifactDirs(workdir)
	if r.Status != CheckStatusError {
		t.Fatalf("status = %q, want error (permission failure must not be reported as OK)", r.Status)
	}
	// No detail should say "will be created on first run" — that's the lie we're guarding against.
	for _, d := range r.Details {
		if strings.Contains(d.Message, "will be created") {
			t.Errorf("detail %q hides the real permission error", d.Message)
		}
	}
}

// TestPinnedDippinVersionMatchesGoMod verifies that PinnedDippinVersion is kept
// in sync with the actual dippin-lang version declared in go.mod.
func TestPinnedDippinVersionMatchesGoMod(t *testing.T) {
	f, err := os.Open("go.mod")
	if err != nil {
		t.Fatalf("open go.mod: %v", err)
	}
	defer f.Close()

	const prefix = "\tgithub.com/2389-research/dippin-lang "
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, prefix) {
			goModVersion := strings.TrimPrefix(line, prefix)
			// Strip any indirect annotation (e.g. " // indirect")
			if idx := strings.Index(goModVersion, " "); idx >= 0 {
				goModVersion = goModVersion[:idx]
			}
			if goModVersion != PinnedDippinVersion {
				t.Errorf("PinnedDippinVersion = %q, but go.mod has dippin-lang %q; update PinnedDippinVersion in tracker_doctor.go",
					PinnedDippinVersion, goModVersion)
			}
			return
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scan go.mod: %v", err)
	}
	t.Fatal("dippin-lang not found in go.mod")
}

// preflightDoctorPipeline is a fixture .dip source for the Git Requires
// checks WITHOUT a source-level `requires:` declaration — exercises the
// CLI-override (WithGitConfig(Require/Off, ...)) path.
const preflightDoctorPipeline = `workflow PreflightDoctor
  goal: "doctor preflight test"
  start: Start
  exit: Done

  agent Start
    label: Start

  agent Done
    label: Done

  edges
    Start -> Done
`

// preflightDoctorPipelineRequiresGit declares `requires: git` in the
// workflow header. Exercises the full source → dippin parser → adapter
// → graph.Attrs → Doctor decision matrix path (dippin-lang v0.26.0+).
const preflightDoctorPipelineRequiresGit = `workflow PreflightDoctorReq
  goal: "doctor preflight test with requires"
  requires: git
  start: Start
  exit: Done

  agent Start
    label: Start

  agent Done
    label: Done

  edges
    Start -> Done
`

// preflightDoctorPipelineRequiresGitAndUnknown adds an unrecognized
// dependency alongside git so tests can verify Doctor preserves
// unknown-dep warning severity through every "Git Requires" success
// branch (CodeRabbit:3260803551).
const preflightDoctorPipelineRequiresGitAndUnknown = `workflow PreflightDoctorReqUnknown
  goal: "doctor preflight test with requires + unknown dep"
  requires: git, docker
  start: Start
  exit: Done

  agent Start
    label: Start

  agent Done
    label: Done

  edges
    Start -> Done
`

func writeDoctorFixture(t *testing.T) (workDir, pipelineFile string) {
	t.Helper()
	workDir = t.TempDir()
	pipelineFile = filepath.Join(workDir, "wf.dip")
	if err := os.WriteFile(pipelineFile, []byte(preflightDoctorPipeline), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	return workDir, pipelineFile
}

func findCheck(rep *DoctorReport, name string) *CheckResult {
	for i := range rep.Checks {
		if rep.Checks[i].Name == name {
			return &rep.Checks[i]
		}
	}
	return nil
}

func TestDoctor_GitRequires_NoForce_NoRequiresInWorkflow(t *testing.T) {
	dir, pf := writeDoctorFixture(t)
	rep, err := Doctor(context.Background(), DoctorConfig{
		WorkDir:        dir,
		PipelineFile:   pf,
		ProbeProviders: false,
	})
	if err != nil {
		t.Fatal(err)
	}
	gr := findCheck(rep, "Git Requires")
	if gr == nil {
		t.Fatal("no Git Requires check in report")
	}
	if gr.Status != CheckStatusOK {
		t.Errorf("workflow without requires:git should be OK, got %s: %s", gr.Status, gr.Message)
	}
}

func TestDoctor_GitRequires_ForceRequire_MissingFail(t *testing.T) {
	requireGit(t)
	dir, pf := writeDoctorFixture(t)
	rep, _ := Doctor(context.Background(), DoctorConfig{
		WorkDir:      dir,
		PipelineFile: pf,
	}, WithGitConfig(GitPreflightRequire, false))
	gr := findCheck(rep, "Git Requires")
	if gr == nil || gr.Status != CheckStatusError {
		t.Fatalf("want error, got %v", gr)
	}
	if !strings.Contains(gr.Hint, "git init") {
		t.Errorf("hint must include 'git init': %v", gr.Hint)
	}
}

// TestDoctor_GitRequires_WarnDoesNotForce verifies that `--git=warn`
// does NOT secretly force the check when the workflow has no `requires:`
// declaration. Warn only downgrades a hard failure to a warning when
// some OTHER signal would have caused the failure. With nothing
// declared and warn policy, the result is OK. (Previously named
// ForceWarn_Downgrades, which described the opposite of what the
// test actually pins; renamed per PR #235 round-3 Copilot review.)
func TestDoctor_GitRequires_WarnDoesNotForce(t *testing.T) {
	dir, pf := writeDoctorFixture(t)
	rep, _ := Doctor(context.Background(), DoctorConfig{
		WorkDir:      dir,
		PipelineFile: pf,
	}, WithGitConfig(GitPreflightWarn, false))
	gr := findCheck(rep, "Git Requires")
	if gr == nil || gr.Status != CheckStatusOK {
		t.Fatalf("warn without force should be OK (no requires:git), got %v", gr)
	}
}

// TestDoctor_GitRequires_WarnDowngradesSourceLevelRequires confirms the
// `--git=warn` downgrade path: a workflow that declares `requires: git`
// running in a non-repo dir produces an Error under auto/require/init
// policy, but a Warn under warn policy. (PR #235 review: the prior test
// named "...DowngradesToWarn" was misleading — it passed GitPreflightOff
// and asserted Skip. This is the real warn-downgrade coverage now that
// dippin v0.26.0 allows source-level `requires: git`.)
func TestDoctor_GitRequires_WarnDowngradesSourceLevelRequires(t *testing.T) {
	requireGit(t)
	dir := t.TempDir()
	pf := filepath.Join(dir, "wf.dip")
	if err := os.WriteFile(pf, []byte(preflightDoctorPipelineRequiresGit), 0o644); err != nil {
		t.Fatal(err)
	}
	rep, _ := Doctor(context.Background(), DoctorConfig{
		WorkDir:      dir,
		PipelineFile: pf,
	}, WithGitConfig(GitPreflightWarn, false))
	gr := findCheck(rep, "Git Requires")
	if gr == nil {
		t.Fatal("no Git Requires check in report")
	}
	if gr.Status != CheckStatusWarn {
		t.Fatalf("want warn (downgrade on missing repo + requires:git), got %s: %s", gr.Status, gr.Message)
	}
	if !strings.Contains(gr.Hint, "git init") {
		t.Errorf("hint must include 'git init' remediation: %v", gr.Hint)
	}
}

// TestDoctor_GitRequires_OffStillWarnsForUnknownDeps pins the PR #235
// round-4 fix from Copilot: --git=off bypasses git enforcement but must
// still surface "requires: docker not yet implemented" warnings so the
// doctor preview matches runtime preflight behavior. Top-level Status
// promotes to Warn (not Skip) when warning details are present so
// `tracker doctor`'s exit code reflects the diagnostic.
func TestDoctor_GitRequires_OffStillWarnsForUnknownDeps(t *testing.T) {
	requireGit(t)
	dir := t.TempDir()
	pf := filepath.Join(dir, "wf.dip")
	// Fixture with `requires: git, docker` — docker is the forward-declared
	// dep that should warn under any policy.
	const src = `workflow OffWarnsTest
  goal: "test"
  requires: git, docker
  start: Start
  exit: Done

  agent Start
    label: Start
  agent Done
    label: Done
  edges
    Start -> Done
`
	if err := os.WriteFile(pf, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	rep, _ := Doctor(context.Background(), DoctorConfig{
		WorkDir:      dir,
		PipelineFile: pf,
	}, WithGitConfig(GitPreflightOff, false))
	gr := findCheck(rep, "Git Requires")
	if gr == nil {
		t.Fatal("no Git Requires check in report")
	}
	if gr.Status != CheckStatusWarn {
		t.Fatalf("want Warn (off bypass + docker warning), got %s: %s", gr.Status, gr.Message)
	}
	foundDocker := false
	for _, d := range gr.Details {
		if d.Status == CheckStatusWarn && strings.Contains(d.Message, "docker") {
			foundDocker = true
		}
	}
	if !foundDocker {
		t.Errorf("expected a Warn detail for docker, got details: %+v", gr.Details)
	}
}

// TestDoctor_GitRequires_OffSkipsSourceLevel re-pins the off-policy skip path,
// matching what the misleading old test actually verified.
func TestDoctor_GitRequires_OffSkipsSourceLevel(t *testing.T) {
	dir := t.TempDir()
	pf := filepath.Join(dir, "wf.dip")
	if err := os.WriteFile(pf, []byte(preflightDoctorPipelineRequiresGit), 0o644); err != nil {
		t.Fatal(err)
	}
	rep, _ := Doctor(context.Background(), DoctorConfig{
		WorkDir:      dir,
		PipelineFile: pf,
	}, WithGitConfig(GitPreflightOff, false))
	gr := findCheck(rep, "Git Requires")
	if gr == nil || gr.Status != CheckStatusSkip {
		t.Fatalf("off should skip, got %v", gr)
	}
}

// TestDoctor_GitRequires_InitAllowInitPreviewsOK pins the PR #235 Codex P2 +
// Copilot fix: under `--git=init --allow-init` in a clean directory,
// runtime preflight would auto-run `git init` and the run would succeed.
// Pre-fix, Doctor reported a false Error for this configuration. The check
// now models the auto-init path: if pipeline.SafetyLatches passes, Doctor
// reports OK with a hint explaining what would happen at run start.
func TestDoctor_GitRequires_InitAllowInitPreviewsOK(t *testing.T) {
	requireGit(t)
	// WorkDir must be empty for the auto-init preview to be OK: PR #235
	// round-7 added a workdir-content latch (Copilot:3260568814) — if the
	// dir contains files, auto-init would refuse because an empty initial
	// commit would leave those files outside HEAD. Keep wf.dip in a
	// separate dir so the workdir under test stays empty.
	dir := t.TempDir()
	pfDir := t.TempDir()
	pf := filepath.Join(pfDir, "wf.dip")
	if err := os.WriteFile(pf, []byte(preflightDoctorPipelineRequiresGit), 0o644); err != nil {
		t.Fatal(err)
	}
	rep, _ := Doctor(context.Background(), DoctorConfig{
		WorkDir:      dir,
		PipelineFile: pf,
	}, WithGitConfig(GitPreflightInit, true))
	gr := findCheck(rep, "Git Requires")
	if gr == nil {
		t.Fatal("no Git Requires check in report")
	}
	if gr.Status != CheckStatusOK {
		t.Fatalf("want OK (auto-init would succeed in clean dir), got %s: %s", gr.Status, gr.Message)
	}
	if !strings.Contains(gr.Message, "auto-init") {
		t.Errorf("message must mention auto-init to explain the preview, got: %s", gr.Message)
	}
}

// TestDoctor_GitRequires_InitAllowInit_PreservesUnknownDepsWarn pins
// CodeRabbit:3260803559 — the auto-init success branch must surface
// the same CheckStatusWarn that the born-HEAD-success branch does when
// the workflow has unrecognized requires: entries. Pre-fix the branch
// returned CheckStatusOK unconditionally, swallowing the warning even
// though the individual unrecognized-dep warnings had been emitted.
func TestDoctor_GitRequires_InitAllowInit_PreservesUnknownDepsWarn(t *testing.T) {
	requireGit(t)
	// Empty workdir so the auto-init preview reaches the OK/Warn branch.
	dir := t.TempDir()
	pfDir := t.TempDir()
	pf := filepath.Join(pfDir, "wf.dip")
	if err := os.WriteFile(pf, []byte(preflightDoctorPipelineRequiresGitAndUnknown), 0o644); err != nil {
		t.Fatal(err)
	}
	rep, _ := Doctor(context.Background(), DoctorConfig{
		WorkDir:      dir,
		PipelineFile: pf,
	}, WithGitConfig(GitPreflightInit, true))
	gr := findCheck(rep, "Git Requires")
	if gr == nil {
		t.Fatal("no Git Requires check in report")
	}
	if gr.Status != CheckStatusWarn {
		t.Fatalf("want CheckStatusWarn (unknown dep present), got %s: %s", gr.Status, gr.Message)
	}
	if !strings.Contains(gr.Message, "auto-init") {
		t.Errorf("message must still describe the auto-init preview, got: %s", gr.Message)
	}
	if !strings.Contains(gr.Message, "unrecognized requires") {
		t.Errorf("message must mention the unknown-deps reason for Warn, got: %s", gr.Message)
	}
}

// TestDoctor_GitRequires_InitAllowInit_NonEmptyWorkdirRefuses pins that
// the doctor preview models the workdir-content latch added in PR #235
// round-7 (Copilot:3260568814). Pre-fix the preview said OK in a
// non-empty workdir while the runtime would refuse — the goal of doctor
// is "don't lie about what the run would do."
func TestDoctor_GitRequires_InitAllowInit_NonEmptyWorkdirRefuses(t *testing.T) {
	requireGit(t)
	dir := t.TempDir()
	// Existing user file in the workdir → auto-init refuses at runtime.
	if err := os.WriteFile(filepath.Join(dir, "SPEC.md"), []byte("# spec\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Pipeline file in a separate dir so SPEC.md is the only file in workdir.
	pfDir := t.TempDir()
	pf := filepath.Join(pfDir, "wf.dip")
	if err := os.WriteFile(pf, []byte(preflightDoctorPipelineRequiresGit), 0o644); err != nil {
		t.Fatal(err)
	}
	rep, _ := Doctor(context.Background(), DoctorConfig{
		WorkDir:      dir,
		PipelineFile: pf,
	}, WithGitConfig(GitPreflightInit, true))
	gr := findCheck(rep, "Git Requires")
	if gr == nil {
		t.Fatal("no Git Requires check in report")
	}
	if gr.Status != CheckStatusError {
		t.Fatalf("want Error (non-empty workdir blocks auto-init), got %s: %s", gr.Status, gr.Message)
	}
	if !strings.Contains(gr.Message, "not empty") {
		t.Errorf("message must mention non-empty workdir, got: %s", gr.Message)
	}
}

// TestDoctor_GitRequires_UnbornHEADReportsError pins the new born-HEAD
// preview from PR #235 round-7 (Copilot:3260568737). Pre-fix Doctor
// reported OK for a `git init`'d-but-uncommitted repo while the runtime
// preflight failed with ErrGitUnbornHEAD.
func TestDoctor_GitRequires_UnbornHEADReportsError(t *testing.T) {
	requireGit(t)
	dir := t.TempDir()
	// `git init` only — HEAD is unborn.
	cmd := exec.CommandContext(context.Background(), "git", "init", "-q")
	cmd.Dir = dir
	cmd.Env = cleanGitEnv()
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	pfDir := t.TempDir()
	pf := filepath.Join(pfDir, "wf.dip")
	if err := os.WriteFile(pf, []byte(preflightDoctorPipelineRequiresGit), 0o644); err != nil {
		t.Fatal(err)
	}
	rep, _ := Doctor(context.Background(), DoctorConfig{
		WorkDir:      dir,
		PipelineFile: pf,
	})
	gr := findCheck(rep, "Git Requires")
	if gr == nil {
		t.Fatal("no Git Requires check in report")
	}
	if gr.Status != CheckStatusError {
		t.Fatalf("want Error for unborn HEAD, got %s: %s", gr.Status, gr.Message)
	}
	if !strings.Contains(gr.Message, "unborn HEAD") {
		t.Errorf("message must mention unborn HEAD, got: %s", gr.Message)
	}
}

// TestDoctor_GitRequires_BundleInputDetectsSourceLevelRequires pins the
// PR #235 review case from Codex P2 + Copilot: when the user passes a
// `.dipx` bundle to `tracker doctor`, the Git Requires check should
// preview the entry workflow's `requires:` exactly as runtime preflight
// would. Pre-fix, doctor read raw ZIP bytes via parsePipelineSource,
// failed to parse, and silently Skip'd — bundles got no preview at all.
func TestDoctor_GitRequires_BundleInputDetectsSourceLevelRequires(t *testing.T) {
	requireGit(t)
	tmp := t.TempDir()

	// Build a fixture .dip and pack it into a .dipx.
	srcDir := t.TempDir()
	entryPath := filepath.Join(srcDir, "entry.dip")
	if err := os.WriteFile(entryPath, []byte(preflightDoctorPipelineRequiresGit), 0o644); err != nil {
		t.Fatal(err)
	}
	bundlePath := dipxtest.PackTestBundle(t, entryPath)

	rep, _ := Doctor(context.Background(), DoctorConfig{
		WorkDir:      tmp, // non-repo
		PipelineFile: bundlePath,
	})
	gr := findCheck(rep, "Git Requires")
	if gr == nil {
		t.Fatal("no Git Requires check in report")
	}
	if gr.Status != CheckStatusError {
		t.Fatalf("bundle with requires:git in a non-repo dir should report Error, got %s: %s", gr.Status, gr.Message)
	}
	if !strings.Contains(gr.Hint, "git init") {
		t.Errorf("hint must include 'git init': %v", gr.Hint)
	}
}

// TestDoctor_GitRequires_BareRepoHintMentionsCheckout pins the PR #235
// round-5 review fix (Copilot:3251112125): pre-fix, a bare repo
// collapsed to the same "isRepo=false" state as a plain non-repo, and
// Doctor showed the generic "run git init" hint — wrong remediation
// for bare repos. Doctor now distinguishes the case and emits "cd into
// a checkout (clone or worktree)" as the hint instead.
func TestDoctor_GitRequires_BareRepoHintMentionsCheckout(t *testing.T) {
	requireGit(t)
	tmp := t.TempDir()
	bare := filepath.Join(tmp, "bare.git")
	cmd := exec.CommandContext(context.Background(), "git", "init", "--bare", "-q", bare)
	cmd.Env = cleanGitEnv()
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init --bare: %v: %s", err, out)
	}
	pf := filepath.Join(tmp, "wf.dip")
	if err := os.WriteFile(pf, []byte(preflightDoctorPipelineRequiresGit), 0o644); err != nil {
		t.Fatal(err)
	}
	rep, _ := Doctor(context.Background(), DoctorConfig{
		WorkDir:      bare,
		PipelineFile: pf,
	})
	gr := findCheck(rep, "Git Requires")
	if gr == nil || gr.Status != CheckStatusError {
		t.Fatalf("want Error for bare-repo workdir, got %v", gr)
	}
	if !strings.Contains(gr.Message, "bare git repository") {
		t.Errorf("message should mention 'bare git repository', got: %s", gr.Message)
	}
	if strings.Contains(gr.Hint, "git init") {
		t.Errorf("hint should NOT suggest 'git init' for bare repo; got: %s", gr.Hint)
	}
	if !strings.Contains(gr.Hint, "checkout") {
		t.Errorf("hint should suggest 'checkout' (clone or worktree) for bare repo; got: %s", gr.Hint)
	}
}

// TestDoctor_GitRequires_BareRepoReportsError pins the PR #235 Copilot fix:
// a bare repo passes `git rev-parse --git-dir` but has no work tree, so
// `git commit` / `git merge` (the operations `requires: git` workflows
// actually use) would fail. Doctor must report Error (or Warn under warn
// policy), not OK, so the user gets the actionable remediation before
// running the workflow.
func TestDoctor_GitRequires_BareRepoReportsError(t *testing.T) {
	requireGit(t)
	tmp := t.TempDir()
	bare := filepath.Join(tmp, "bare.git")
	cmd := exec.CommandContext(context.Background(), "git", "init", "--bare", "-q", bare)
	cmd.Env = cleanGitEnv()
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init --bare: %v: %s", err, out)
	}
	pf := filepath.Join(tmp, "wf.dip")
	if err := os.WriteFile(pf, []byte(preflightDoctorPipelineRequiresGit), 0o644); err != nil {
		t.Fatal(err)
	}
	rep, _ := Doctor(context.Background(), DoctorConfig{
		WorkDir:      bare,
		PipelineFile: pf,
	})
	gr := findCheck(rep, "Git Requires")
	if gr == nil || gr.Status != CheckStatusError {
		t.Fatalf("want Error for bare repo (no work tree), got %v", gr)
	}
}

func TestDoctor_GitRequires_ForceRequire_AfterGitInit_Passes(t *testing.T) {
	dir, pf := writeDoctorFixture(t)
	mustGitInitForDoctor(t, dir)
	rep, _ := Doctor(context.Background(), DoctorConfig{
		WorkDir:      dir,
		PipelineFile: pf,
	}, WithGitConfig(GitPreflightRequire, false))
	gr := findCheck(rep, "Git Requires")
	if gr == nil || gr.Status != CheckStatusOK {
		t.Fatalf("want ok after git init, got %v", gr)
	}
}

// TestDoctor_GitRequires_SourceLevelDetected exercises the workflow header's
// `requires: git` declaration end-to-end through Doctor — no CLI override.
// Confirms the dippin v0.26.0 syntax flows through the adapter to where
// Doctor reads it.
func TestDoctor_GitRequires_SourceLevelDetected(t *testing.T) {
	requireGit(t)
	dir := t.TempDir()
	pf := filepath.Join(dir, "wf.dip")
	if err := os.WriteFile(pf, []byte(preflightDoctorPipelineRequiresGit), 0o644); err != nil {
		t.Fatal(err)
	}
	rep, _ := Doctor(context.Background(), DoctorConfig{
		WorkDir:      dir,
		PipelineFile: pf,
	})
	gr := findCheck(rep, "Git Requires")
	if gr == nil || gr.Status != CheckStatusError {
		t.Fatalf("workflow declares requires:git in a non-repo dir, want error; got %v", gr)
	}
	if !strings.Contains(gr.Hint, "git init") {
		t.Errorf("hint must include 'git init': %v", gr.Hint)
	}
}

func TestDoctor_GitRequires_SourceLevelSatisfied(t *testing.T) {
	dir := t.TempDir()
	mustGitInitForDoctor(t, dir)
	pf := filepath.Join(dir, "wf.dip")
	if err := os.WriteFile(pf, []byte(preflightDoctorPipelineRequiresGit), 0o644); err != nil {
		t.Fatal(err)
	}
	rep, _ := Doctor(context.Background(), DoctorConfig{
		WorkDir:      dir,
		PipelineFile: pf,
	})
	gr := findCheck(rep, "Git Requires")
	if gr == nil || gr.Status != CheckStatusOK {
		t.Fatalf("workflow declares requires:git in a repo dir, want OK; got %v", gr)
	}
}

func mustGitInitForDoctor(t *testing.T, dir string) {
	t.Helper()
	requireGit(t)
	cmd := exec.CommandContext(context.Background(), "git", "init", "-q")
	cmd.Dir = dir
	cmd.Env = cleanGitEnv()
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init in %s: %v: %s", dir, err, out)
	}
	// Born HEAD: PR #235 round-7 added an unborn-HEAD check to Doctor's
	// Git Requires preview, so a fresh `git init` alone now reports
	// Error("no commits"). All callers want a usable repo; bake the
	// `--allow-empty` initial commit into the helper so each test
	// doesn't have to repeat it. (Copilot:3260568737)
	commitCmd := exec.CommandContext(context.Background(), "git",
		"-c", "user.name=t", "-c", "user.email=t@t",
		"commit", "--allow-empty", "-q", "-m", "init")
	commitCmd.Dir = dir
	commitCmd.Env = cleanGitEnv()
	if out, err := commitCmd.CombinedOutput(); err != nil {
		t.Fatalf("git commit --allow-empty in %s: %v: %s", dir, err, out)
	}
}

// clearGatewayEnv unsets every env var the Gateway Routing check consults so
// each test starts from a known-empty baseline regardless of the developer's
// shell environment.
func clearGatewayEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{
		"TRACKER_GATEWAY_URL", "TRACKER_GATEWAY_KIND",
		"OPENAI_API_KEY",
		"ANTHROPIC_BASE_URL", "OPENAI_BASE_URL", "GEMINI_BASE_URL", "OPENAI_COMPAT_BASE_URL",
	} {
		t.Setenv(k, "")
	}
}

// gatewayDetailContains reports whether any detail message in the check
// contains substr.
func gatewayDetailContains(c CheckResult, substr string) bool {
	for _, d := range c.Details {
		if strings.Contains(d.Message, substr) {
			return true
		}
	}
	return false
}

func TestCheckGatewayRouting_BedrockMasqueradeNote(t *testing.T) {
	clearGatewayEnv(t)
	t.Setenv("TRACKER_GATEWAY_KIND", "bedrock")
	t.Setenv("TRACKER_GATEWAY_URL", "https://gw.example.com")
	t.Setenv("OPENAI_API_KEY", "sk-test-12345678901234567890")

	c := checkGatewayRouting()
	if c.Status != CheckStatusOK {
		t.Fatalf("status = %q, want ok (informational only)", c.Status)
	}
	if !gatewayDetailContains(c, "route to Claude Sonnet 4.6") {
		t.Errorf("expected bedrock masquerade note; details = %+v", c.Details)
	}
}

func TestCheckGatewayRouting_NoMasqueradeWithoutGatewayURL(t *testing.T) {
	clearGatewayEnv(t)
	// kind=bedrock + OPENAI_API_KEY but no gateway URL: openai resolves to
	// the SDK default (api.openai.com), so traffic never traverses a gateway
	// and is not masqueraded.
	t.Setenv("TRACKER_GATEWAY_KIND", "bedrock")
	t.Setenv("OPENAI_API_KEY", "sk-test-12345678901234567890")

	c := checkGatewayRouting()
	if gatewayDetailContains(c, "route to Claude") {
		t.Errorf("masquerade note should not fire without a gateway URL; details = %+v", c.Details)
	}
}

func TestCheckGatewayRouting_NoMasqueradeWithoutOpenAIKey(t *testing.T) {
	clearGatewayEnv(t)
	t.Setenv("TRACKER_GATEWAY_KIND", "bedrock")
	t.Setenv("TRACKER_GATEWAY_URL", "https://gw.example.com")

	c := checkGatewayRouting()
	if gatewayDetailContains(c, "route to Claude") {
		t.Errorf("masquerade note should not fire without OPENAI_API_KEY; details = %+v", c.Details)
	}
}

func TestCheckGatewayRouting_NoMasqueradeUnderCFAIG(t *testing.T) {
	clearGatewayEnv(t)
	t.Setenv("TRACKER_GATEWAY_KIND", "cf-aig")
	t.Setenv("TRACKER_GATEWAY_URL", "https://gw.example.com")
	t.Setenv("OPENAI_API_KEY", "sk-test-12345678901234567890")

	c := checkGatewayRouting()
	if gatewayDetailContains(c, "route to Claude") {
		t.Errorf("masquerade note is bedrock-only; details = %+v", c.Details)
	}
}

func TestCheckGatewayRouting_NoMasqueradeWhenOpenAIBaseURLOverridden(t *testing.T) {
	clearGatewayEnv(t)
	// OPENAI_BASE_URL wins over the gateway in the resolver, so OpenAI
	// traffic bypasses the bedrock gateway entirely — no masquerade.
	t.Setenv("TRACKER_GATEWAY_KIND", "bedrock")
	t.Setenv("TRACKER_GATEWAY_URL", "https://gw.example.com")
	t.Setenv("OPENAI_API_KEY", "sk-test-12345678901234567890")
	t.Setenv("OPENAI_BASE_URL", "https://direct.openai.example.com")

	c := checkGatewayRouting()
	if gatewayDetailContains(c, "route to Claude") {
		t.Errorf("masquerade note should be suppressed when OPENAI_BASE_URL overrides routing; details = %+v", c.Details)
	}
	// The precedence note should still cover the OpenAI override.
	if !gatewayDetailContains(c, "openai (OPENAI_BASE_URL)") {
		t.Errorf("expected precedence note for the OpenAI override; details = %+v", c.Details)
	}
}

func TestCheckGatewayRouting_PerProviderPrecedenceNote(t *testing.T) {
	clearGatewayEnv(t)
	t.Setenv("TRACKER_GATEWAY_URL", "https://gw.example.com")
	t.Setenv("ANTHROPIC_BASE_URL", "https://direct.anthropic.example.com")

	c := checkGatewayRouting()
	if c.Status != CheckStatusOK {
		t.Fatalf("status = %q, want ok (informational only)", c.Status)
	}
	if !gatewayDetailContains(c, "per-provider overrides win over TRACKER_GATEWAY_URL") {
		t.Errorf("expected precedence note; details = %+v", c.Details)
	}
	if !gatewayDetailContains(c, "anthropic (ANTHROPIC_BASE_URL)") {
		t.Errorf("expected the overriding provider named; details = %+v", c.Details)
	}
}

func TestCheckGatewayRouting_NoPrecedenceNoteWithoutGatewayURL(t *testing.T) {
	clearGatewayEnv(t)
	// Kind set but no gateway URL: a *_BASE_URL is a direct override, not a
	// conflict with any gateway, so no precedence note.
	t.Setenv("TRACKER_GATEWAY_KIND", "bedrock")
	t.Setenv("ANTHROPIC_BASE_URL", "https://direct.anthropic.example.com")

	c := checkGatewayRouting()
	if gatewayDetailContains(c, "per-provider overrides win") {
		t.Errorf("precedence note requires TRACKER_GATEWAY_URL; details = %+v", c.Details)
	}
	// The summary must not imply gateway routing is in effect without a URL.
	if !strings.Contains(c.Message, "no gateway routing in effect") {
		t.Errorf("summary should note no routing without a URL; got %q", c.Message)
	}
}

func TestDoctor_GatewayRoutingCheckGatedOnEnv(t *testing.T) {
	clearGatewayEnv(t)
	t.Setenv("ANTHROPIC_API_KEY", "sk-ant-test-12345678901234567890")
	workdir := t.TempDir()

	// No gateway env → check absent.
	r, err := Doctor(context.Background(), DoctorConfig{WorkDir: workdir})
	if err != nil {
		t.Fatalf("Doctor: %v", err)
	}
	if findCheck(r, "Gateway Routing") != nil {
		t.Error("Gateway Routing check should be absent when no gateway env is set")
	}

	// Gateway env present → check appears.
	t.Setenv("TRACKER_GATEWAY_KIND", "bedrock")
	r, err = Doctor(context.Background(), DoctorConfig{WorkDir: workdir})
	if err != nil {
		t.Fatalf("Doctor: %v", err)
	}
	if findCheck(r, "Gateway Routing") == nil {
		t.Error("Gateway Routing check should appear when TRACKER_GATEWAY_KIND is set")
	}
}
