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
)

type runConfig struct {
	mode         commandMode
	pipelineFile string
	format       string // "dip", "dot", or "" (auto-detect from extension)
	workdir      string
	resumeID     string // run ID to resume (resolved to checkpoint path)
	noTUI        bool
	verbose      bool
	jsonOut      bool // stream events as NDJSON to stdout
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
	run      func(pipelineFile, workdir, checkpoint, format string, verbose bool, jsonOut bool) error
	runTUI   func(pipelineFile, workdir, checkpoint, format string, verbose bool) error
}

type setupResult struct {
	values    map[string]string
	cancelled bool
}

func main() {
	cfg, err := parseFlags(os.Args)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			printUsage(os.Stdout)
			os.Exit(0)
		}
		fmt.Fprintf(os.Stderr, "Error: %v\n\n", err)
		printUsage(os.Stderr)
		os.Exit(1)
	}

	if cfg.workdir == "" {
		wd, err := os.Getwd()
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: cannot determine working directory: %v\n", err)
			os.Exit(1)
		}
		cfg.workdir = wd
	}

	err = executeCommand(cfg, commandDeps{})
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}
