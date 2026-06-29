// ABOUTME: CLI flag parsing and usage output for the tracker command.
// ABOUTME: Handles subcommand detection and flag extraction for all modes.
package main

import (
	"flag"
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"
	"time"

	tracker "github.com/2389-research/tracker"
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
	case modeVersion, modeSetup, modeWorkflows, modeUpdate:
		return *cfg, nil
	case modeDoctor:
		return parseDoctorFlags(args, cfg)
	case modeInit, modeValidate, modeSimulate:
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

// parseDoctorFlags handles doctor-specific flag parsing.
// Supports: --probe (live auth check), -w/--workdir, --backend, and an optional positional pipeline file.
//
// Uses parseArgsMultiPass so flags work in any order relative to the
// positional pipeline argument, matching run-mode's UX. Pre-fix
// `tracker doctor wf.dip --git=warn` silently left cfg.git at the
// default because flag.Parse stops at the first positional, and the
// downstream `--git=warn` override never reached the Git Requires
// check.
func parseDoctorFlags(args []string, cfg *runConfig) (runConfig, error) {
	dfs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	dfs.SetOutput(io.Discard)
	dfs.BoolVar(&cfg.probe, "probe", true, "Perform live API auth check with a 1-token request (set to false to skip)")
	dfs.StringVar(&cfg.workdir, "w", "", "Working directory (default: current directory)")
	dfs.StringVar(&cfg.workdir, "workdir", "", "Working directory (default: current directory)")
	dfs.StringVar(&cfg.backend, "backend", "", "Agent backend: native (default), claude-code, or acp")
	dfs.StringVar(&cfg.git, "git", "", "Git preflight policy (auto/off/warn/require/init) to evaluate")
	dfs.BoolVar(&cfg.allowInit, "allow-init", false, "Required latch for --git=init")
	positional, err := parseArgsMultiPass(dfs, args[2:])
	if err != nil {
		return *cfg, fmt.Errorf("doctor: %w", err)
	}
	if len(positional) > 1 {
		return *cfg, fmt.Errorf("doctor: unexpected extra arguments after pipeline file: %v", positional[1:])
	}
	if len(positional) == 1 {
		cfg.pipelineFile = positional[0]
	}
	if err := validateBackend(cfg.backend); err != nil {
		return *cfg, fmt.Errorf("doctor: %w", err)
	}
	if err := validateGitFlag(*cfg); err != nil {
		return *cfg, fmt.Errorf("doctor: %w", err)
	}
	if cfg.git == "auto" {
		cfg.git = ""
	}
	return *cfg, nil
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

	if err := validateRunConfig(cfg); err != nil {
		return cfg, err
	}
	// Normalize the "auto" alias to empty so downstream comparisons stay simple.
	if cfg.git == "auto" {
		cfg.git = ""
	}
	return cfg, nil
}

// validateRunConfig runs the run-mode flag validators in order, returning the
// first error. Extracted from parseRunFlags to keep its complexity bounded.
func validateRunConfig(cfg runConfig) error {
	if err := validateBackend(cfg.backend); err != nil {
		return err
	}
	if err := validateBudgetLimits(cfg); err != nil {
		return err
	}
	if err := validateWebhookFlags(cfg); err != nil {
		return err
	}
	if err := validateToolSafetyFlags(cfg); err != nil {
		return err
	}
	if err := validateGitFlag(cfg); err != nil {
		return err
	}
	return validateGatewayKind(cfg.gatewayKind)
}

// validateBudgetLimits returns an error if any budget limit is negative.
func validateBudgetLimits(cfg runConfig) error {
	if cfg.maxTokens < 0 || cfg.maxCostCents < 0 || cfg.maxWallTime < 0 {
		return fmt.Errorf("budget limits must be non-negative")
	}
	return nil
}

// validateToolSafetyFlags rejects nonsensical values for the tool safety flags.
// A negative --max-output-limit is a user error (probably a typo); reject loudly
// rather than silently coercing to default, to avoid surprise.
func validateToolSafetyFlags(cfg runConfig) error {
	if cfg.maxOutputLimit < 0 {
		return fmt.Errorf("--max-output-limit must be non-negative, got %d", cfg.maxOutputLimit)
	}
	return nil
}

// validateWebhookFlags returns an error if webhook flags are used incorrectly.
// Mutual exclusion with --autopilot and --auto-approve is enforced at parse time.
func validateWebhookFlags(cfg runConfig) error {
	if cfg.webhookURL == "" {
		return nil
	}
	if cfg.autopilot != "" {
		return fmt.Errorf("--webhook-url and --autopilot are mutually exclusive: choose one gate automation strategy")
	}
	if cfg.autoApprove {
		return fmt.Errorf("--webhook-url and --auto-approve are mutually exclusive: choose one gate automation strategy")
	}
	if cfg.gateTimeout < 0 {
		return fmt.Errorf("--gate-timeout must be non-negative, got %v", cfg.gateTimeout)
	}
	if cfg.gateTimeoutAction != "fail" && cfg.gateTimeoutAction != "success" {
		return fmt.Errorf("--gate-timeout-action must be %q or %q, got %q", "fail", "success", cfg.gateTimeoutAction)
	}
	return nil
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
	fs.IntVar(&cfg.maxTokens, "max-tokens", 0, "Halt if total tokens across the run exceed this value (0 = no limit)")
	fs.IntVar(&cfg.maxCostCents, "max-cost", 0, "Halt if total cost in cents exceeds this value (0 = no limit)")
	fs.DurationVar(&cfg.maxWallTime, "max-wall-time", 0, "Halt if pipeline wall time exceeds this duration (0 = no limit)")
	fs.BoolVar(&cfg.sleepAware, "sleep-aware-budget", false, "Exclude detected suspend spans (e.g. a closed laptop) from wall-time and stall budgets")
	fs.BoolVar(&cfg.failOnOverride, "fail-on-override", false, "Exit code 2 if the run terminates via validation_overridden (default: exit 0)")
	fs.Var(paramMapFlag{target: &cfg.params}, "param", "Override workflow param (repeatable): key=value")
	fs.StringVar(&cfg.gatewayURL, "gateway-url", "", "Cloudflare AI Gateway root URL (per-provider *_BASE_URL env vars override this)")
	fs.StringVar(&cfg.gatewayKind, "gateway-kind", "", "Gateway path convention: empty/cf-aig (default) or bedrock (2389 bedrock-gateway Worker)")
	fs.StringVar(&cfg.webhookURL, "webhook-url", "", "POST human gate prompts to this URL and wait for callback (headless execution)")
	fs.StringVar(&cfg.gateCallbackAddr, "gate-callback-addr", ":8789", "Local addr for the callback server when --webhook-url is set")
	fs.DurationVar(&cfg.gateTimeout, "gate-timeout", 10*time.Minute, "Per-gate wait timeout when --webhook-url is set")
	fs.StringVar(&cfg.gateTimeoutAction, "gate-timeout-action", "fail", "What to do on gate timeout: fail or success")
	fs.StringVar(&cfg.webhookAuthHeader, "webhook-auth", "", "Authorization header value sent on outbound webhook requests (e.g. 'Bearer sk_live_...')")
	fs.StringVar(&cfg.exportBundle, "export-bundle", "", "After the run completes, write a git bundle of run artifacts to this path")
	fs.StringVar(&cfg.artifactDir, "artifact-dir", "", "Override node state directory (default: <workdir>/.tracker/runs)")
	fs.BoolVar(&cfg.bypassDenylist, "bypass-denylist", false, "Disable the built-in tool_command denylist (SECURITY: use only in sandboxed environments)")
	fs.Var(stringSliceFlag{target: &cfg.toolAllowlist}, "tool-allowlist", "Glob pattern(s) a tool_command must match to execute (repeatable or comma-separated)")
	fs.Var(stringSliceFlag{target: &cfg.toolDenylistAdd}, "tool-denylist-add", "Extra glob pattern(s) added to the built-in tool_command denylist (defense in depth; additive, cannot remove built-ins; --bypass-denylist disables these too)")
	fs.IntVar(&cfg.maxOutputLimit, "max-output-limit", 0, "Hard ceiling in bytes on tool_command output per stream (0 = default 10MB)")
	fs.BoolVar(&cfg.forceBundleMismatch, "force-bundle-mismatch", false, "allow resume even when the bundle's content-addressed identity differs from the original run")
	fs.StringVar(&cfg.git, "git", "", "Git preflight policy: auto (default, respects workflow `requires:`) | off | warn | require | init")
	fs.BoolVar(&cfg.allowInit, "allow-init", false, "Required latch for --git=init in non-interactive runs")
	return fs
}

// validateGitFlag rejects invalid --git values up front so the user gets a
// clear error at flag-parse time rather than deep inside preflight.
// Accepts both "" (resolves to auto downstream) and "auto" as aliases.
func validateGitFlag(cfg runConfig) error {
	switch cfg.git {
	case "", "auto", "off", "warn", "require", "init":
		return nil
	}
	return fmt.Errorf("invalid --git=%q: must be one of: auto, off, warn, require, init", cfg.git)
}

// validateGatewayKind rejects unsupported --gateway-kind values at
// flag-parse time. Without this, a typo like --gateway-kind=bdrock would
// be propagated into TRACKER_GATEWAY_KIND and surface much later as an
// adapter-construction error (the strict resolver refuses to route on
// unknown kinds — see tracker.ErrGatewayRouteRefused). Catching at parse
// time gives the user a clear CLI message right where they made the typo.
// Accepted values must stay in sync with the tracker.GatewayKind constants.
func validateGatewayKind(kind string) error {
	switch kind {
	case "", string(tracker.GatewayKindCFAIG), string(tracker.GatewayKindBedrock):
		return nil
	}
	return fmt.Errorf("invalid --gateway-kind=%q: must be one of: %q, %q", kind, tracker.GatewayKindCFAIG, tracker.GatewayKindBedrock)
}

type paramMapFlag struct {
	target *map[string]string
}

func (p paramMapFlag) String() string {
	if p.target == nil || *p.target == nil {
		return ""
	}
	var pairs []string
	for _, key := range slices.Sorted(maps.Keys(*p.target)) {
		pairs = append(pairs, fmt.Sprintf("%s=%s", key, (*p.target)[key]))
	}
	return strings.Join(pairs, ",")
}

func (p paramMapFlag) Set(value string) error {
	key, val, ok := strings.Cut(value, "=")
	if !ok {
		return fmt.Errorf("invalid --param %q: expected key=value", value)
	}
	key = strings.TrimSpace(key)
	if key == "" {
		return fmt.Errorf("invalid --param %q: key cannot be empty", value)
	}
	if p.target == nil {
		return fmt.Errorf("invalid --param target")
	}
	if *p.target == nil {
		*p.target = make(map[string]string)
	}
	(*p.target)[key] = val
	return nil
}

// stringSliceFlag is a repeatable flag that appends each value to a target slice.
// Each invocation may also provide a comma-separated list of values, which are
// split and appended individually. Empty values (after TrimSpace) are ignored.
type stringSliceFlag struct {
	target *[]string
}

func (s stringSliceFlag) String() string {
	if s.target == nil || *s.target == nil {
		return ""
	}
	return strings.Join(*s.target, ",")
}

func (s stringSliceFlag) Set(value string) error {
	if s.target == nil {
		return fmt.Errorf("invalid stringSliceFlag target")
	}
	for _, part := range strings.Split(value, ",") {
		trimmed := strings.TrimSpace(part)
		if trimmed == "" {
			continue
		}
		*s.target = append(*s.target, trimmed)
	}
	return nil
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
	fmt.Fprintf(w, "  tracker doctor [--probe=false] [pipeline.dip]  Preflight health check (exit 0=pass 1=fail 2=warn)\n")
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
	fmt.Fprintf(w, "  --max-tokens int          Halt if total tokens exceed this value (0 = no limit)\n")
	fmt.Fprintf(w, "  --max-cost int            Halt if total cost in cents exceeds this value (0 = no limit)\n")
	fmt.Fprintf(w, "  --max-wall-time duration  Halt if pipeline wall time exceeds this duration (0 = no limit)\n")
	fmt.Fprintf(w, "  --fail-on-override        Exit code 2 if run terminates via validation_overridden (default: exit 0)\n")
	fmt.Fprintf(w, "  --param key=value         Override a declared workflow param (repeatable)\n")
	fmt.Fprintf(w, "  --gateway-url string      Cloudflare AI Gateway root URL (per-provider *_BASE_URL env vars override this)\n")
	fmt.Fprintf(w, "  --gateway-kind string     Gateway path convention: empty/cf-aig (default) or bedrock\n")
	fmt.Fprintf(w, "  --webhook-url string      POST human gate prompts to this URL and wait for callback (headless)\n")
	fmt.Fprintf(w, "  --gate-callback-addr string  Local addr for the webhook callback server (default: :8789)\n")
	fmt.Fprintf(w, "  --gate-timeout duration      Per-gate wait timeout when --webhook-url is set (default: 10m)\n")
	fmt.Fprintf(w, "  --gate-timeout-action string What to do on gate timeout: fail (default) or success\n")
	fmt.Fprintf(w, "  --webhook-auth string     Authorization header for outbound webhook requests\n")
	fmt.Fprintf(w, "  --export-bundle string    Write a portable git bundle of run artifacts to this path after completion\n")
	fmt.Fprintf(w, "  --artifact-dir string     Override node state directory (default: <workdir>/.tracker/runs)\n")
	fmt.Fprintf(w, "  --bypass-denylist         Disable the built-in tool_command denylist (SECURITY: sandboxed use only)\n")
	fmt.Fprintf(w, "  --tool-allowlist pattern  Glob pattern a tool_command must match to execute (repeatable, comma-separated)\n")
	fmt.Fprintf(w, "  --tool-denylist-add pat   Extra glob pattern(s) added to built-in denylist (repeatable, comma-separated, additive; --bypass-denylist disables built-in + added patterns)\n")
	fmt.Fprintf(w, "  --max-output-limit bytes  Hard ceiling per tool_command output stream (default: 10MB)\n")
	fmt.Fprintf(w, "  --force-bundle-mismatch   Allow resume even when the bundle's content-addressed identity differs from the original run\n")
	fmt.Fprintf(w, "  --git policy              Git preflight policy: auto (default), off, warn, require, init\n")
	fmt.Fprintf(w, "  --allow-init              Required for --git=init in non-interactive runs\n")
	fmt.Fprintf(w, "  --version                 Show version information\n")
}
