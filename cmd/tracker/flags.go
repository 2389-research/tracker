// ABOUTME: CLI flag parsing and usage output for the tracker command.
// ABOUTME: Handles subcommand detection and flag extraction for all modes.
package main

import (
	"flag"
	"fmt"
	"io"
)

func parseFlags(args []string) (runConfig, error) {
	cfg := runConfig{mode: modeRun}

	if len(args) > 1 {
		if mode, ok := parseSubcommand(args[1], &cfg); ok {
			return parseFlagsForMode(mode, args, &cfg)
		}
	}

	return parseRunFlags(args, cfg)
}

// parseSubcommand checks if the second argument is a known subcommand and
// sets the config mode. Returns the mode and true if matched.
func parseSubcommand(arg string, cfg *runConfig) (commandMode, bool) {
	switch arg {
	case "version", "--version":
		cfg.mode = modeVersion
		return modeVersion, true
	case "--help", "-h", "help":
		return "", false // signal ErrHelp below
	case "list":
		cfg.mode = modeAudit
		return modeAudit, true
	case string(modeDiagnose):
		cfg.mode = modeDiagnose
		return modeDiagnose, true
	case string(modeDoctor):
		cfg.mode = modeDoctor
		return modeDoctor, true
	case string(modeSetup):
		cfg.mode = modeSetup
		return modeSetup, true
	case string(modeValidate):
		cfg.mode = modeValidate
		return modeValidate, true
	case string(modeSimulate):
		cfg.mode = modeSimulate
		return modeSimulate, true
	case string(modeAudit):
		cfg.mode = modeAudit
		return modeAudit, true
	case string(modeWorkflows):
		cfg.mode = modeWorkflows
		return modeWorkflows, true
	case string(modeInit):
		cfg.mode = modeInit
		return modeInit, true
	}
	return "", false
}

// parseFlagsForMode handles flag parsing for non-run subcommands.
func parseFlagsForMode(mode commandMode, args []string, cfg *runConfig) (runConfig, error) {
	switch mode {
	case modeVersion, modeSetup, modeDoctor, modeWorkflows:
		return *cfg, nil
	case modeInit:
		if len(args) > 2 {
			cfg.pipelineFile = args[2]
		}
		return *cfg, nil
	case modeValidate:
		if len(args) > 2 {
			cfg.pipelineFile = args[2]
		}
		return *cfg, nil
	case modeSimulate:
		if len(args) > 2 {
			cfg.pipelineFile = args[2]
		}
		return *cfg, nil
	case modeAudit, modeDiagnose:
		return parseAuditFlags(args, cfg)
	default:
		return *cfg, nil
	}
}

// parseAuditFlags handles audit-specific flag parsing.
func parseAuditFlags(args []string, cfg *runConfig) (runConfig, error) {
	afs := flag.NewFlagSet("audit", flag.ContinueOnError)
	afs.SetOutput(io.Discard)
	afs.StringVar(&cfg.workdir, "w", "", "Working directory")
	afs.StringVar(&cfg.workdir, "workdir", "", "Working directory")
	if err := afs.Parse(args[2:]); err != nil {
		return *cfg, fmt.Errorf("audit: %w", err)
	}
	if afs.NArg() > 0 {
		cfg.resumeID = afs.Arg(0)
	}
	return *cfg, nil
}

// parseRunFlags parses flags for the default "run" mode, supporting flags
// in any order relative to the pipeline file argument.
func parseRunFlags(args []string, cfg runConfig) (runConfig, error) {
	// Handle --help / -h that wasn't caught as subcommand.
	if len(args) > 1 {
		switch args[1] {
		case "--help", "-h", "help":
			return cfg, flag.ErrHelp
		}
	}

	fs := flag.NewFlagSet(args[0], flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&cfg.workdir, "w", "", "Working directory (default: current directory)")
	fs.StringVar(&cfg.workdir, "workdir", "", "Working directory (default: current directory)")
	fs.StringVar(&cfg.resumeID, "r", "", "Resume a previous run by ID (e.g. 13041bbb0a38)")
	fs.StringVar(&cfg.resumeID, "resume", "", "Resume a previous run by ID (e.g. 13041bbb0a38)")
	fs.BoolVar(&cfg.noTUI, "no-tui", false, "Disable TUI dashboard; use plain console output")
	fs.BoolVar(&cfg.verbose, "verbose", false, "Show raw provider stream events and extra LLM trace detail")
	fs.BoolVar(&cfg.jsonOut, "json", false, "Stream events as newline-delimited JSON to stdout")
	fs.StringVar(&cfg.format, "format", "", "Pipeline format override: dip (default) or dot")
	fs.StringVar(&cfg.backend, "backend", "", "Agent backend: native (default) or claude-code")
	fs.StringVar(&cfg.autopilot, "autopilot", "", "Replace human gates with LLM judge (lax/mid/hard/mentor)")
	fs.BoolVar(&cfg.autoApprove, "auto-approve", false, "Auto-approve all human gates (no LLM, deterministic)")

	// Go's flag package stops parsing at the first non-flag argument.
	// To support flags in any order (e.g. "tracker pipeline.dot -c cp.json"),
	// we gather all non-flag arguments across multiple parse passes.
	remaining := args[1:]
	var positional []string
	for len(remaining) > 0 {
		if err := fs.Parse(remaining); err != nil {
			return cfg, err
		}
		positional = append(positional, fs.Args()...)
		// If Parse consumed everything or stopped at a non-flag, we need
		// to skip past the first positional arg and try parsing the rest.
		if fs.NArg() == 0 {
			break
		}
		// Skip the first positional arg and continue parsing the rest.
		remaining = fs.Args()[1:]
		positional = positional[:len(positional)-fs.NArg()]
		positional = append(positional, fs.Args()[0])
	}

	if len(positional) < 1 {
		return cfg, errUsage
	}

	cfg.pipelineFile = positional[0]

	if cfg.backend != "" && cfg.backend != "native" && cfg.backend != "claude-code" {
		return cfg, fmt.Errorf("invalid backend %q: must be one of: native, claude-code", cfg.backend)
	}

	return cfg, nil
}

func printUsage(w io.Writer) {
	fmt.Fprint(w, renderStartupBanner())
	fmt.Fprintf(w, "Usage:\n")
	fmt.Fprintf(w, "  tracker [flags] <pipeline.dip> [flags]\n")
	fmt.Fprintf(w, "  tracker setup\n")
	fmt.Fprintf(w, "  tracker validate <pipeline.dip>\n")
	fmt.Fprintf(w, "  tracker simulate <pipeline.dip>\n")
	fmt.Fprintf(w, "  tracker audit [runID]\n")
	fmt.Fprintf(w, "  tracker diagnose [runID]       Analyze failures in a run\n")
	fmt.Fprintf(w, "  tracker doctor                Preflight health check\n")
	fmt.Fprintf(w, "  tracker workflows             List built-in workflows\n")
	fmt.Fprintf(w, "  tracker init <workflow>        Copy a built-in workflow to current directory\n")
	fmt.Fprintf(w, "  tracker list                  List recent pipeline runs\n")
	fmt.Fprintf(w, "  tracker version               Show version information\n\n")
	fmt.Fprintf(w, "Flags:\n")
	fmt.Fprintf(w, "  -w, --workdir string      Working directory (default: current directory)\n")
	fmt.Fprintf(w, "  -r, --resume string       Resume a previous run by ID (e.g. 13041bbb0a38)\n")
	fmt.Fprintf(w, "  --format string           Pipeline format override: dip (default) or dot (deprecated)\n")
	fmt.Fprintf(w, "  --json                    Stream events as newline-delimited JSON to stdout\n")
	fmt.Fprintf(w, "  --no-tui                  Disable TUI dashboard; use plain console output\n")
	fmt.Fprintf(w, "  --verbose                 Show raw provider stream events and extra LLM trace detail\n")
	fmt.Fprintf(w, "  --backend string          Agent backend: native (default) or claude-code\n")
	fmt.Fprintf(w, "  --autopilot <persona>     Replace human gates with LLM judge (lax/mid/hard/mentor)\n")
	fmt.Fprintf(w, "  --auto-approve            Auto-approve all human gates (deterministic, no LLM)\n")
	fmt.Fprintf(w, "  --version                 Show version information\n")
}
