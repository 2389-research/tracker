// ABOUTME: CLI entry point for the tracker pipeline engine.
// ABOUTME: Loads a pipeline file (.dip preferred, .dot deprecated) and runs it.
// ABOUTME: Mode 1 (default): BubbleteaInterviewer for human gates with inline TUI per gate.
// ABOUTME: Mode 2 (--tui): Full dashboard TUI with header, node list, agent log, and modal gates.
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"time"

	execpkg "github.com/2389-research/tracker/agent/exec"
	"github.com/2389-research/tracker/pipeline"
)

type runConfig struct {
	mode         commandMode
	pipelineFile string
	format       string // "dip", "dot", or "" (auto-detect from extension)
	workdir      string
	resumeID     string // run ID to resume (resolved to checkpoint path)
	noTUI        bool
	verbose      bool
	jsonOut      bool          // stream events as NDJSON to stdout
	backend      string        // agent execution backend: "" (default), "native", or "claude-code"
	autopilot    string        // persona name (lax/mid/hard/mentor) or empty
	autoApprove  bool          // deterministic auto-approve, no LLM
	probe        bool          // doctor: perform live auth validation (network call per provider)
	maxTokens    int           // halt if total tokens exceed this value (0 = no limit)
	maxCostCents int           // halt if total cost in cents exceeds this value (0 = no limit)
	maxWallTime  time.Duration // halt if wall time exceeds this duration (0 = no limit)
	sleepAware   bool          // opt-in: exclude detected suspend spans from wall/stall budgets (#422)
	// failOnOverride causes the CLI to exit with code 2 (not 0) when the run
	// terminates as pipeline.OutcomeValidationOverridden. Default false keeps
	// validation_overridden a success-equivalent exit, matching IsSuccess().
	// Also settable via TRACKER_FAIL_ON_OVERRIDE=1 (strict "=1" parsing,
	// matching the TRACKER_PASS_* convention).
	failOnOverride    bool
	params            map[string]string
	gatewayURL        string        // TRACKER_GATEWAY_URL override — synthesizes per-provider base URLs
	gatewayKind       string        // TRACKER_GATEWAY_KIND override — selects cf-aig (default) or bedrock path convention
	webhookURL        string        // POST human gate prompts to this URL and wait for callback
	gateCallbackAddr  string        // local addr for the callback server when --webhook-url is set
	gateTimeout       time.Duration // per-gate wait timeout when --webhook-url is set
	gateTimeoutAction string        // what to do on gate timeout: fail or success
	webhookAuthHeader string        // Authorization header value for outbound webhook requests
	exportBundle      string        // path for post-run git bundle export; "" = skip
	artifactDir       string        // override node state directory; "" = <workdir>/.tracker/runs
	bypassDenylist    bool          // disable the built-in tool_command denylist (SECURITY escape hatch)
	toolAllowlist     []string      // additional allowlist patterns for tool_command (repeatable)
	toolDenylistAdd   []string      // user-added denylist patterns that join the built-in denylist (repeatable)
	maxOutputLimit    int           // hard ceiling (bytes) on per-stream tool_command output; 0 = default 10MB
	// forceBundleMismatch allows resume to proceed even when the .dipx
	// bundle's content-addressed identity differs from the original run.
	// Consumed by resume identity verification to allow an explicit
	// operator override when bundle identities differ.
	forceBundleMismatch bool
	// git is the v0.29.0 preflight policy: "" (auto) | "off" | "warn" |
	// "require" | "init". Empty resolves to auto, which honors the
	// workflow's `requires:` declaration.
	git string
	// allowInit is the second latch for --git=init in non-interactive runs.
	allowInit bool
}

type commandMode string

const (
	modeRun       commandMode = "run"
	modeSetup     commandMode = "setup"
	modeAudit     commandMode = "audit"
	modeSimulate  commandMode = "simulate"
	modeValidate  commandMode = "validate"
	modeDiagnose  commandMode = "diagnose"
	modeDoctor    commandMode = "doctor"
	modeVersion   commandMode = "version"
	modeWorkflows commandMode = "workflows"
	modeInit      commandMode = "init"
	modeUpdate    commandMode = "update"
)

var errUsage = errors.New("usage")

// Build-time variables set via -ldflags.
// When installed locally via `go install`, initVersionFromVCS populates
// commit and date from Go's embedded VCS info so `tracker version`
// shows something useful even without goreleaser ldflags.
var (
	version = "dev"
	commit  = "unknown"
	date    = "unknown"
)

func init() { initVersionFromVCS() }

type commandDeps struct {
	loadEnv  func(string) error
	runSetup func() error
	run      func(pipelineFile, workdir, checkpoint, format, backend string, verbose bool, jsonOut bool) error
	runTUI   func(pipelineFile, workdir, checkpoint, format, backend string, verbose bool) error
}

type setupResult struct {
	values    map[string]string
	cancelled bool
}

func main() {
	// __jail-exec is an internal subcommand the agent runtime invokes via
	// /proc/self/exe to re-exec itself into a Landlock-sandboxed child for
	// the writable_paths fs-jail (issue #272). It MUST be dispatched before
	// flag parsing because:
	//   - We don't want flag.Parse to validate or surface help.
	//   - The child's job is to apply Landlock and syscall.Exec into sh -c.
	//   - Operators MUST NOT invoke it directly; the __ prefix signals
	//     "internal." See CLAUDE.md § Architecture Gotchas for details.
	if len(os.Args) > 1 && os.Args[1] == "__jail-exec" {
		os.Exit(execpkg.RunJailExec(os.Args[2:]))
	}

	cfg, err := parseFlags(os.Args)
	if err != nil {
		handleFlagsError(err)
		return
	}

	if cfg.workdir == "" {
		cfg.workdir = resolveMainWorkDir()
	}

	if err := runCommand(cfg); err != nil {
		exitWithError(err)
	}
}

// runCommand performs the non-jail-exec, post-flag-parse work: an optional
// pre-run update check, command execution, and a post-success update hint.
// Returns the command's error (handled by exitWithError in main).
func runCommand(cfg runConfig) error {
	if cfg.mode == modeRun {
		maybeCheckForUpdate()
	}

	err := executeCommand(cfg, commandDeps{})

	// Only nag about updates after a successful run — on failure the error
	// (printed by exitWithError) is the headline, and the hint's ≤2s wait
	// just delays it.
	if cfg.mode == modeRun && err == nil {
		printUpdateHint()
	}
	return err
}

// exitWithError maps a command error to the process exit code and exits.
// It never returns.
func exitWithError(err error) {
	var doctorWarn *DoctorWarningsError
	if errors.As(err, &doctorWarn) {
		// exit 2 = doctor finished with warnings but no failures
		os.Exit(2)
	}
	// --fail-on-override turns a validation_overridden run-mode terminal
	// status into exit 2 (distinct from generic fail=1, doctor-warning=2
	// only applies to `tracker doctor` so the two exit-2 codepaths can't
	// both fire on the same invocation). The detailed stderr message was
	// already printed by interpretRunResult (spec-format with gate ID and
	// preferred label), so we don't re-print the bare sentinel here.
	if errors.Is(err, pipeline.ErrValidationOverridden) {
		os.Exit(2)
	}
	fmt.Fprintf(os.Stderr, "error: %v\n", err)
	os.Exit(1)
}

// handleFlagsError prints usage or error and exits for flag parsing failures.
func handleFlagsError(err error) {
	if errors.Is(err, flag.ErrHelp) {
		printUsage(os.Stdout)
		os.Exit(0)
	}
	fmt.Fprintf(os.Stderr, "Error: %v\n\n", err)
	printUsage(os.Stderr)
	os.Exit(1)
}

// resolveMainWorkDir returns the current working directory, exiting on failure.
func resolveMainWorkDir() string {
	wd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: cannot determine working directory: %v\n", err)
		os.Exit(1)
	}
	return wd
}
