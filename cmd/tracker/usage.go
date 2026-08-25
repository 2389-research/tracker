// ABOUTME: Two-tier CLI help output for the tracker run command (#463).
// ABOUTME: --help shows common flags; --help-all shows every flag grouped by concern.
package main

import (
	"fmt"
	"io"
)

// printUsageHeader writes the banner, invocation forms, and subcommand list
// shared by both the common (`--help`) and full (`--help-all`) references.
func printUsageHeader(w io.Writer) {
	fmt.Fprint(w, renderStartupBanner())
	fmt.Fprintf(w, "Usage:\n")
	fmt.Fprintf(w, "  tracker [flags] <pipeline.dip> [flags]\n")
	// The subcommand list is derived from commandTable (see command_table.go)
	// so a new command appears in help automatically — no hand-maintained row.
	renderSubcommandUsage(w)
	fmt.Fprintf(w, "\n")
}

// printUsage writes the default help: the common flags most runs need, plus a
// pointer to `--help-all` for the full reference. Progressive disclosure keeps
// the newcomer's first `tracker --help` scannable rather than a 30-flag wall (#463).
func printUsage(w io.Writer) {
	printUsageHeader(w)
	fmt.Fprintf(w, "Common flags:\n")
	fmt.Fprintf(w, "  -w, --workdir string      Working directory (default: current directory)\n")
	fmt.Fprintf(w, "  -r, --resume string       Resume a previous run by ID (e.g. 13041bbb0a38)\n")
	fmt.Fprintf(w, "  --json                    Stream events as newline-delimited JSON to stdout\n")
	fmt.Fprintf(w, "  --no-tui                  Disable TUI dashboard; use plain console output\n")
	fmt.Fprintf(w, "  --verbose                 Show raw provider stream events and extra LLM trace detail\n")
	fmt.Fprintf(w, "  --backend string          Agent backend: native (default), claude-code, or acp\n")
	fmt.Fprintf(w, "  --autopilot <persona>     Replace human gates with LLM judge (lax/mid/hard/mentor)\n")
	fmt.Fprintf(w, "  --auto-approve            Auto-approve all human gates (deterministic, no LLM)\n")
	fmt.Fprintf(w, "  --max-cost int            Halt if total cost in cents exceeds this value (0 = no limit)\n")
	fmt.Fprintf(w, "  --max-tokens int          Halt if total tokens exceed this value (0 = no limit)\n")
	fmt.Fprintf(w, "  --param key=value         Override a declared workflow param (repeatable)\n")
	fmt.Fprintf(w, "  --git policy              Git preflight policy: auto (default), off, warn, require, init\n")
	fmt.Fprintf(w, "  --version                 Show version information\n")
	fmt.Fprintf(w, "\nRun 'tracker --help-all' for the full flag reference (budget, gateway routing,\n")
	fmt.Fprintf(w, "webhook gates, artifacts, and tool-safety knobs).\n")
}

// printUsageAll writes every run flag, grouped by concern (#463). This is the
// expert reference behind `tracker --help-all`; the flat `--help` shows only
// the common subset above.
func printUsageAll(w io.Writer) {
	printUsageHeader(w)
	fmt.Fprintf(w, "Common:\n")
	fmt.Fprintf(w, "  -w, --workdir string      Working directory (default: current directory)\n")
	fmt.Fprintf(w, "  -r, --resume string       Resume a previous run by ID (e.g. 13041bbb0a38)\n")
	fmt.Fprintf(w, "  --json                    Stream events as newline-delimited JSON to stdout\n")
	fmt.Fprintf(w, "  --no-tui                  Disable TUI dashboard; use plain console output\n")
	fmt.Fprintf(w, "  --verbose                 Show raw provider stream events and extra LLM trace detail\n")
	fmt.Fprintf(w, "  --format string           Pipeline format override: dip (default) or dot (deprecated)\n")
	fmt.Fprintf(w, "  --backend string          Agent backend: native (default), claude-code, or acp\n")
	fmt.Fprintf(w, "  --param key=value         Override a declared workflow param (repeatable)\n")
	fmt.Fprintf(w, "\nBudget caps (halt between nodes):\n")
	fmt.Fprintf(w, "  --max-tokens int          Halt if total tokens exceed this value (0 = no limit)\n")
	fmt.Fprintf(w, "  --max-cost int            Halt if total cost in cents exceeds this value (0 = no limit)\n")
	fmt.Fprintf(w, "  --max-wall-time duration  Halt if pipeline wall time exceeds this duration (0 = no limit)\n")
	fmt.Fprintf(w, "  --sleep-aware-budget      Exclude detected suspend spans (e.g. a closed laptop) from wall/stall budgets\n")
	fmt.Fprintf(w, "  --fail-on-override        Exit code 2 if run terminates via validation_overridden (default: exit 0)\n")
	fmt.Fprintf(w, "\nHuman gates & automation:\n")
	fmt.Fprintf(w, "  --autopilot <persona>     Replace human gates with LLM judge (lax/mid/hard/mentor)\n")
	fmt.Fprintf(w, "  --auto-approve            Auto-approve all human gates (deterministic, no LLM)\n")
	fmt.Fprintf(w, "  --webhook-url string      POST human gate prompts to this URL and wait for callback (headless)\n")
	fmt.Fprintf(w, "  --gate-callback-addr string  Local addr for the webhook callback server (default: :8789)\n")
	fmt.Fprintf(w, "  --gate-timeout duration      Per-gate wait timeout when --webhook-url is set (default: 10m)\n")
	fmt.Fprintf(w, "  --gate-timeout-action string What to do on gate timeout: fail (default) or success\n")
	fmt.Fprintf(w, "  --webhook-auth string     Authorization header for outbound webhook requests\n")
	fmt.Fprintf(w, "\nGateway routing:\n")
	fmt.Fprintf(w, "  --gateway-url string      Cloudflare AI Gateway root URL (per-provider *_BASE_URL env vars override this)\n")
	fmt.Fprintf(w, "  --gateway-kind string     Gateway path convention: empty/cf-aig (default) or bedrock\n")
	fmt.Fprintf(w, "\nArtifacts, git & resume:\n")
	fmt.Fprintf(w, "  --git policy              Git preflight policy: auto (default), off, warn, require, init\n")
	fmt.Fprintf(w, "  --allow-init              Required for --git=init in non-interactive runs\n")
	fmt.Fprintf(w, "  --export-bundle string    Write a portable git bundle of run artifacts to this path after completion\n")
	fmt.Fprintf(w, "  --artifact-dir string     Override node state directory (default: <workdir>/.tracker/runs)\n")
	fmt.Fprintf(w, "  --force-bundle-mismatch   Allow resume even when the bundle's content-addressed identity differs from the original run\n")
	fmt.Fprintf(w, "\nTool safety:\n")
	fmt.Fprintf(w, "  --bypass-denylist         Disable the built-in tool_command denylist (SECURITY: sandboxed use only)\n")
	fmt.Fprintf(w, "  --tool-allowlist pattern  Glob pattern a tool_command must match to execute (repeatable, comma-separated)\n")
	fmt.Fprintf(w, "  --tool-denylist-add pat   Extra glob pattern(s) added to built-in denylist (repeatable, comma-separated, additive; --bypass-denylist disables built-in + added patterns)\n")
	fmt.Fprintf(w, "  --max-output-limit bytes  Hard ceiling per tool_command output stream (default: 10MB)\n")
	fmt.Fprintf(w, "\nOther:\n")
	fmt.Fprintf(w, "  --version                 Show version information\n")
}
