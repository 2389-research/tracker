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

// subcommandMap maps CLI arg strings to command modes. "list" is an alias for audit.
var subcommandMap = map[string]commandMode{
	"version":             modeVersion,
	"--version":           modeVersion,
	"list":                modeAudit,
	string(modeDiagnose):  modeDiagnose,
	string(modeDoctor):    modeDoctor,
	string(modeSetup):     modeSetup,
	string(modeValidate):  modeValidate,
	string(modeSimulate):  modeSimulate,
	string(modeAudit):     modeAudit,
	string(modeWorkflows): modeWorkflows,
	string(modeInit):      modeInit,
	string(modeUpdate):    modeUpdate,
}

// parseSubcommand checks if the second argument is a known subcommand and
// sets the config mode. Returns the mode and true if matched.
func parseSubcommand(arg string, cfg *runConfig) (commandMode, bool) {
	if arg == "--help" || arg == "-h" || arg == "help" {
		return "", false // signal ErrHelp below
	}
	if mode, ok := subcommandMap[arg]; ok {
		cfg.mode = mode
		return mode, true
	}
	return "", false
}

// parseFlagsForMode handles flag parsing for non-run subcommands.
func parseFlagsForMode(mode commandMode, args []string, cfg *runConfig) (runConfig, error) {
	switch mode {
	case modeVersion, modeSetup, modeDoctor, modeWorkflows, modeUpdate:
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
	if isHelpRequest(args) {
		return cfg, flag.ErrHelp
	}

	fs := newRunFlagSet(args[0], &cfg)
	positional, err := parseArgsMultiPass(fs, args[1:])
	if err != nil {
		return cfg, err
	}

	if len(positional) < 1 {
		return cfg, errUsage
	}
	cfg.pipelineFile = positional[0]

	if err := validateBackend(cfg.backend); err != nil {
		return cfg, err
	}
	return cfg, nil
}

// isHelpRequest returns true when the second argument is a help flag.
func isHelpRequest(args []string) bool {
	if len(args) <= 1 {
		return false
	}
	switch args[1] {
	case "--help", "-h", "help":
		return true
	}
	return false
}

// newRunFlagSet creates and configures the FlagSet for run command flags.
func newRunFlagSet(progName string, cfg *runConfig) *flag.FlagSet {
	fs := flag.NewFlagSet(progName, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&cfg.workdir, "w", "", "Working directory (default: current directory)")
	fs.StringVar(&cfg.workdir, "workdir", "", "Working directory (default: current directory)")
	fs.StringVar(&cfg.resumeID, "r", "", "Resume a previous run by ID (e.g. 13041bbb0a38)")
	fs.StringVar(&cfg.resumeID, "resume", "", "Resume a previous run by ID (e.g. 13041bbb0a38)")
	fs.BoolVar(&cfg.noTUI, "no-tui", false, "Disable TUI dashboard; use plain console output")
	fs.BoolVar(&cfg.verbose, "verbose", false, "Show raw provider stream events and extra LLM trace detail")
	fs.BoolVar(&cfg.jsonOut, "json", false, "Stream events as newline-delimited JSON to stdout")
	fs.StringVar(&cfg.format, "format", "", "Pipeline format override: dip (default) or dot")
	fs.StringVar(&cfg.backend, "backend", "", "Agent backend: native (default), claude-code, or acp")
	fs.StringVar(&cfg.autopilot, "autopilot", "", "Replace human gates with LLM judge (lax/mid/hard/mentor)")
	fs.BoolVar(&cfg.autoApprove, "auto-approve", false, "Auto-approve all human gates (no LLM, deterministic)")
	return fs
}

// parseArgsMultiPass runs multiple flag parse passes to collect positional
// args even when flags appear after positional arguments.
func parseArgsMultiPass(fs *flag.FlagSet, remaining []string) ([]string, error) {
	var positional []string
	for len(remaining) > 0 {
		if err := fs.Parse(remaining); err != nil {
			return nil, err
		}
		positional = append(positional, fs.Args()...)
		if fs.NArg() == 0 {
			break
		}
		remaining = fs.Args()[1:]
		positional = positional[:len(positional)-fs.NArg()]
		positional = append(positional, fs.Args()[0])
	}
	return positional, nil
}

// validateBackend returns an error if the backend name is not recognized.
func validateBackend(backend string) error {
	if backend != "" && backend != "native" && backend != "claude-code" && backend != "acp" {
		return fmt.Errorf("invalid backend %q: must be one of: native, claude-code, acp", backend)
	}
	return nil
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
	fmt.Fprintf(w, "  tracker update                Update tracker to latest version\n")
	fmt.Fprintf(w, "  tracker version               Show version information\n\n")
	fmt.Fprintf(w, "Flags:\n")
	fmt.Fprintf(w, "  -w, --workdir string      Working directory (default: current directory)\n")
	fmt.Fprintf(w, "  -r, --resume string       Resume a previous run by ID (e.g. 13041bbb0a38)\n")
	fmt.Fprintf(w, "  --format string           Pipeline format override: dip (default) or dot (deprecated)\n")
	fmt.Fprintf(w, "  --json                    Stream events as newline-delimited JSON to stdout\n")
	fmt.Fprintf(w, "  --no-tui                  Disable TUI dashboard; use plain console output\n")
	fmt.Fprintf(w, "  --verbose                 Show raw provider stream events and extra LLM trace detail\n")
	fmt.Fprintf(w, "  --backend string          Agent backend: native (default), claude-code, or acp\n")
	fmt.Fprintf(w, "  --autopilot <persona>     Replace human gates with LLM judge (lax/mid/hard/mentor)\n")
	fmt.Fprintf(w, "  --auto-approve            Auto-approve all human gates (deterministic, no LLM)\n")
	fmt.Fprintf(w, "  --version                 Show version information\n")
}
