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
	"strings"
	"time"

	tracker "github.com/2389-research/tracker"
	execpkg "github.com/2389-research/tracker/agent/exec"
	"github.com/2389-research/tracker/pipeline"
	"github.com/2389-research/tracker/pipeline/handlers"
)

type runConfig struct {
	mode         commandMode
	pipelineFile string
	format       string // "dip", "dot", or "" (auto-detect from extension)
	workdir      string
	// workdirExplicit is true when -w/--workdir was given (workdir is then not
	// the cwd default); the printed resume command repeats it (#696).
	workdirExplicit bool
	resumeID        string // run ID to resume (resolved to checkpoint path)
	resumeFrom      string // --from <node>: re-enter a resumed run at this node (#651); requires -r
	resumeExact     bool   // --resume-no-rewind: disable the automatic resume rewind (#651); requires -r
	noTUI           bool
	verbose         bool
	jsonOut         bool                    // stream events as NDJSON to stdout
	backend         string                  // agent execution backend: "" (default), "native", or "claude-code"
	autopilot       string                  // persona name (lax/mid/hard/mentor) or empty
	autopilotModel  string                  // model --autopilot asks for at each gate; empty = the default provider's autopilot model
	autoApprove     bool                    // deterministic auto-approve, no LLM
	gates           string                  // --gates: auto / console / pause
	argv            []string                // the arguments after the program name, as given (recorded for tracker answer)
	answer          answerFlags             // tracker answer's own flags
	pendingAnswer   *handlers.PendingAnswer // set when tracker answer resumes the run
	expectDigest    string                  // set when tracker answer resumes the run: the recorded workflow digest
	probe           bool                    // doctor: perform live auth validation (network call per provider)
	maxTokens       int                     // halt if total tokens exceed this value (0 = no limit)
	maxCostCents    int                     // halt if total cost in cents exceeds this value (0 = no limit)
	maxWallTime     time.Duration           // halt if wall time exceeds this duration (0 = no limit)
	sleepAware      bool                    // opt-in: exclude detected suspend spans from wall/stall budgets (#422)
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
	// verifyRace opts `tracker verify-tests` into a `go test -race ./...` pass
	// (#489) so the gate fails on tests that pass only by timing luck.
	verifyRace bool
	// verifyCoverage opts `tracker verify-tests` into the advisory per-test
	// coverage-attribution pass (#532): flags tests that reach no production
	// code and test-local re-implementations of uncovered production logic.
	verifyCoverage bool
	// envFiles is --env-files: which .env files to load ("all" default,
	// "config", "none"). Empty defers to the shell-only TRACKER_ENV_FILES,
	// then to "all" (#659).
	envFiles string
}

type commandMode string

const (
	modeRun         commandMode = "run"
	modeSetup       commandMode = "setup"
	modeAudit       commandMode = "audit"
	modeSimulate    commandMode = "simulate"
	modeEstimate    commandMode = "estimate"
	modeValidate    commandMode = "validate"
	modeDiagnose    commandMode = "diagnose"
	modeDoctor      commandMode = "doctor"
	modeVersion     commandMode = "version"
	modeWorkflows   commandMode = "workflows"
	modeInit        commandMode = "init"
	modeUpdate      commandMode = "update"
	modeVerifyTests commandMode = "verify-tests"
	modeStatus      commandMode = "status"
	modeRunJSON     commandMode = "run-json"
	modeAnswer      commandMode = "answer"
)

var errUsage = errors.New("usage")

// errHelpAll signals that the user asked for the full flag reference
// (`tracker --help-all`) rather than the common-flags help (#463).
var errHelpAll = errors.New("help-all")

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
	// loadEnv loads the .env files for (workdir, --env-files value).
	loadEnv  func(workdir, envFilesFlag string) error
	runSetup func() error
	run      func(*runOptions) error
	runTUI   func(*runOptions) error
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

	// Route library diagnostics (#449) to stderr so the CLI's terminal output
	// is unchanged by the sink migration. Must run before any library call
	// that could emit a diagnostic. Embedders get the no-op default instead.
	installCLIDiagnostics()

	cfg, err := parseFlags(os.Args)
	if err != nil {
		handleFlagsError(err)
		return
	}

	cfg = applyWorkdirDefault(cfg, resolveMainWorkDir)

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
	// exit 4 = waiting at a human gate; the run summary already said how to
	// answer it, so nothing more goes to stderr.
	if code := exitCodeFor(err); code != 1 {
		os.Exit(code)
	}
	// A run-execution failure already had its classified cause + remediation
	// rendered in the run summary (printFailureCause). Re-printing the full
	// wrapped error here would make the raw "pipeline execution: handler error at
	// node …" wrapper the last, most-prominent thing on screen (#492). Keep stderr
	// to a terse classified one-liner instead. interpretRunResult is the sole
	// producer of these prefixes.
	msg := err.Error()
	if strings.HasPrefix(msg, "pipeline execution:") || strings.HasPrefix(msg, "pipeline finished with status:") {
		fc := tracker.ClassifyFailure(err)
		fmt.Fprintf(os.Stderr, "error: %s\n", strings.TrimSpace(fc.Icon+" "+fc.Title))
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "error: %v\n", err)
	os.Exit(1)
}

// handleFlagsError prints usage or error and exits for flag parsing failures.
func handleFlagsError(err error) {
	if errors.Is(err, errHelpAll) {
		printUsageAll(os.Stdout)
		os.Exit(0)
	}
	if errors.Is(err, flag.ErrHelp) {
		printUsage(os.Stdout)
		os.Exit(0)
	}
	fmt.Fprintf(os.Stderr, "Error: %v\n\n", err)
	printUsage(os.Stderr)
	os.Exit(1)
}

// resolveMainWorkDir returns the current working directory, exiting on failure.
// applyWorkdirDefault records whether -w/--workdir was given (the resume
// hint repeats it only then) and fills in cwd() when it was not.
func applyWorkdirDefault(cfg runConfig, cwd func() string) runConfig {
	cfg.workdirExplicit = cfg.workdir != ""
	if !cfg.workdirExplicit {
		cfg.workdir = cwd()
	}
	return cfg
}

func resolveMainWorkDir() string {
	wd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: cannot determine working directory: %v\n", err)
		os.Exit(1)
	}
	return wd
}

// exitCodeFor maps a command error to its exit code: 2 for doctor warnings and
// for --fail-on-override (distinct codepaths that can't both fire on one
// invocation; interpretRunResult already printed the override detail), 4 for a
// run waiting at a human gate, 1 for everything else.
func exitCodeFor(err error) int {
	var doctorWarn *DoctorWarningsError
	switch {
	case errors.As(err, &doctorWarn), errors.Is(err, pipeline.ErrValidationOverridden):
		return 2
	case errors.Is(err, errPausedAtGate):
		return exitPausedAtGate
	default:
		return 1
	}
}
