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
)

type runConfig struct {
	mode              commandMode
	pipelineFile      string
	format            string // "dip", "dot", or "" (auto-detect from extension)
	workdir           string
	resumeID          string // run ID to resume (resolved to checkpoint path)
	noTUI             bool
	verbose           bool
	jsonOut           bool          // stream events as NDJSON to stdout
	backend           string        // agent execution backend: "" (default), "native", or "claude-code"
	autopilot         string        // persona name (lax/mid/hard/mentor) or empty
	autoApprove       bool          // deterministic auto-approve, no LLM
	probe             bool          // doctor: perform live auth validation (network call per provider)
	maxTokens         int           // halt if total tokens exceed this value (0 = no limit)
	maxCostCents      int           // halt if total cost in cents exceeds this value (0 = no limit)
	maxWallTime       time.Duration // halt if wall time exceeds this duration (0 = no limit)
	params            map[string]string
	gatewayURL        string        // TRACKER_GATEWAY_URL override — synthesizes per-provider base URLs
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
	cfg, err := parseFlags(os.Args)
	if err != nil {
		handleFlagsError(err)
		return
	}

	if cfg.workdir == "" {
		cfg.workdir = resolveMainWorkDir()
	}

	if cfg.mode == modeRun {
		maybeCheckForUpdate()
	}

	err = executeCommand(cfg, commandDeps{})

	if cfg.mode == modeRun {
		printUpdateHint()
	}

	if err != nil {
		var doctorWarn *DoctorWarningsError
		if errors.As(err, &doctorWarn) {
			// exit 2 = doctor finished with warnings but no failures
			os.Exit(2)
		}
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
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
