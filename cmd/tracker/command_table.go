// ABOUTME: Single source of truth for public CLI subcommand metadata (#612).
// ABOUTME: subcommandMap, flag-parser grouping, and the usage list all derive here.
package main

import (
	"io"
	"strings"
)

// parserClass selects which flag-parsing routine a subcommand uses. Grouping
// commands by class here (rather than by an ad-hoc switch on the mode) keeps the
// parser assignment beside the rest of a command's metadata, so a command can't
// silently end up on the wrong parser.
type parserClass int

const (
	// parserNone: no subcommand flags (version/setup/workflows/update).
	parserNone parserClass = iota
	// parserDoctor: parseDoctorFlags.
	parserDoctor
	// parserPositionalFile: a single positional pipeline/workflow file at
	// args[2] (init/validate/simulate/estimate).
	parserPositionalFile
	// parserVerifyTests: parseVerifyTestsFlags.
	parserVerifyTests
	// parserAudit: parseAuditFlags (audit/diagnose/status/run-json).
	parserAudit
)

// commandSpec is one public CLI invocation the user can type. It carries the
// canonical/alias name, the mode it dispatches to, its flag-parser class, and
// its help synopsis — the metadata that previously lived scattered across
// main.go (modes), flags.go (subcommandMap + parser switch), and usage.go
// (help rows). Dispatch and the specialized flag parsers stay explicit; only
// the metadata is centralized.
type commandSpec struct {
	// name is the exact arg string the user types (e.g. "run-json", "list",
	// "--version"). For canonical commands this equals string(mode); aliases
	// use a literal.
	name string
	// mode is the command mode this invocation dispatches to.
	mode commandMode
	// parser is the flag-parser class for this command's mode.
	parser parserClass
	// args is the argument hint rendered after the name in help
	// (e.g. "<pipeline.dip>", "[runID]"), or "" for none.
	args string
	// desc is the help synopsis, one entry per rendered line (the first inline
	// with the invocation, the rest as continuation lines). Empty = a terse
	// row with no description.
	desc []string
	// hidden keeps a valid invocation out of the rendered help list (used for
	// the "--version" alias, which is surfaced under the flags section).
	hidden bool
}

// commandTable is the ordered, single authority for public subcommand metadata.
// Rendered top-to-bottom in the usage help. modeRun (the default, no-subcommand
// form) is intentionally absent — it is the "tracker [flags] <pipeline.dip>"
// header form, not a subcommand.
var commandTable = []commandSpec{
	{name: string(modeSetup), mode: modeSetup, parser: parserNone},
	{name: string(modeValidate), mode: modeValidate, parser: parserPositionalFile, args: "<pipeline.dip>"},
	{name: string(modeSimulate), mode: modeSimulate, parser: parserPositionalFile, args: "<pipeline.dip>"},
	{name: string(modeEstimate), mode: modeEstimate, parser: parserPositionalFile, args: "<pipeline.dip>", desc: []string{"Rough pre-run cost & scale estimate"}},
	{name: string(modeAudit), mode: modeAudit, parser: parserAudit, args: "[runID]"},
	{name: string(modeDiagnose), mode: modeDiagnose, parser: parserAudit, args: "[runID]", desc: []string{"Analyze failures in a run"}},
	{name: string(modeVerifyTests), mode: modeVerifyTests, parser: parserVerifyTests, args: "[dir]", desc: []string{
		"Flag duplicate/near-duplicate Go test bodies (exit 1 if any)",
		"--race also runs `go test -race ./...` (exit 1 on a data race)",
		"--coverage adds the advisory unreached-path/re-implemented-logic pass (never fails)",
	}},
	{name: string(modeStatus), mode: modeStatus, parser: parserAudit, args: "[runID]", desc: []string{"Agent-authored high-level timeline of a run (#494)"}},
	{name: string(modeRunJSON), mode: modeRunJSON, parser: parserAudit, args: "[runID]", desc: []string{"Assemble the run.json manifest for a run directory (post-hoc; survives SIGKILL)"}},
	{name: string(modeDoctor), mode: modeDoctor, parser: parserDoctor, args: "[--probe=false] [pipeline.dip]", desc: []string{"Preflight health check (exit 0=pass 1=fail 2=warn)"}},
	{name: string(modeWorkflows), mode: modeWorkflows, parser: parserNone, desc: []string{"List built-in workflows"}},
	{name: string(modeInit), mode: modeInit, parser: parserPositionalFile, args: "<workflow>", desc: []string{"Copy a built-in workflow to current directory"}},
	{name: "list", mode: modeAudit, parser: parserAudit, desc: []string{"List recent pipeline runs"}},
	{name: string(modeUpdate), mode: modeUpdate, parser: parserNone, desc: []string{"Update tracker to latest version"}},
	{name: string(modeVersion), mode: modeVersion, parser: parserNone, desc: []string{"Show version information"}},
	{name: "--version", mode: modeVersion, parser: parserNone, hidden: true},
}

// subcommandMap maps CLI arg strings to command modes, derived from
// commandTable. "list" is an alias for audit; "--version" for version.
var subcommandMap = buildSubcommandMap()

func buildSubcommandMap() map[string]commandMode {
	m := make(map[string]commandMode, len(commandTable))
	for _, s := range commandTable {
		m[s.name] = s.mode
	}
	return m
}

// parserByMode maps each command mode to its flag-parser class, derived from
// commandTable. All specs sharing a mode declare the same class.
var parserByMode = buildParserByMode()

func buildParserByMode() map[commandMode]parserClass {
	m := make(map[commandMode]parserClass, len(commandTable))
	for _, s := range commandTable {
		m[s.mode] = s.parser
	}
	return m
}

// parserClassFor returns the flag-parser class for a command mode.
func parserClassFor(mode commandMode) parserClass {
	return parserByMode[mode]
}

// usageDescCol is the column (from the start of the line) at which subcommand
// descriptions begin in the rendered help.
const usageDescCol = 35

// renderSubcommandUsage writes the derived subcommand list to w, one row per
// non-hidden commandTable entry, in table order.
func renderSubcommandUsage(w io.Writer) {
	for _, s := range commandTable {
		if !s.hidden {
			io.WriteString(w, s.usageRows())
		}
	}
}

// usageRows renders the help line(s) for a single command: the invocation with
// its inline description, plus any continuation lines aligned to usageDescCol.
func (s commandSpec) usageRows() string {
	prefix := "  tracker " + s.name
	if s.args != "" {
		prefix += " " + s.args
	}
	if len(s.desc) == 0 {
		return prefix + "\n"
	}
	pad := usageDescCol - len(prefix)
	if pad < 2 {
		pad = 2
	}
	out := prefix + strings.Repeat(" ", pad) + s.desc[0] + "\n"
	for _, cont := range s.desc[1:] {
		out += strings.Repeat(" ", usageDescCol) + cont + "\n"
	}
	return out
}
