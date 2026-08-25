// ABOUTME: Regression tests pinning commandTable as the single CLI-metadata authority (#612).
// ABOUTME: Guards that every dispatchable command is parsed and rendered in help.
package main

import (
	"bytes"
	"strings"
	"testing"
)

// TestCommandTable_RunJSONRoutedAndDocumented pins the exact drift the finding
// cited: run-json was parsed/dispatched and documented on the website but
// absent from local `--help`. It must resolve as a subcommand AND appear in the
// rendered help.
func TestCommandTable_RunJSONRoutedAndDocumented(t *testing.T) {
	mode, ok := subcommandMap["run-json"]
	if !ok || mode != modeRunJSON {
		t.Fatalf("run-json must map to modeRunJSON, got %q ok=%v", mode, ok)
	}

	var buf bytes.Buffer
	printUsageHeader(&buf)
	if !strings.Contains(buf.String(), "tracker run-json") {
		t.Errorf("rendered help must list `tracker run-json`; got:\n%s", buf.String())
	}
}

// TestCommandTable_EveryDispatchableModeInHelp is the completeness guard: every
// mode reachable via subcommandMap must be surfaced by at least one non-hidden
// commandTable row in the rendered help. This is the invariant that would have
// caught run-json (dispatchable but missing from help).
func TestCommandTable_EveryDispatchableModeInHelp(t *testing.T) {
	var buf bytes.Buffer
	printUsageHeader(&buf)
	help := buf.String()

	// Collect the visible (non-hidden) invocation name per mode.
	visibleName := map[commandMode]string{}
	for _, s := range commandTable {
		if !s.hidden {
			if _, seen := visibleName[s.mode]; !seen {
				visibleName[s.mode] = s.name
			}
		}
	}

	for name, mode := range subcommandMap {
		vis, ok := visibleName[mode]
		if !ok {
			t.Errorf("mode %q (reachable via %q) has no non-hidden help row", mode, name)
			continue
		}
		if !strings.Contains(help, "tracker "+vis) {
			t.Errorf("mode %q not rendered in help (expected `tracker %s`)", mode, vis)
		}
	}
}

// TestCommandTable_ParserClassMatchesTable pins each command's parser grouping
// so parseFlagsForMode can't route a command through the wrong flag parser.
func TestCommandTable_ParserClassMatchesTable(t *testing.T) {
	want := map[commandMode]parserClass{
		modeSetup:       parserNone,
		modeWorkflows:   parserNone,
		modeUpdate:      parserNone,
		modeVersion:     parserNone,
		modeDoctor:      parserDoctor,
		modeValidate:    parserPositionalFile,
		modeSimulate:    parserPositionalFile,
		modeEstimate:    parserPositionalFile,
		modeInit:        parserPositionalFile,
		modeVerifyTests: parserVerifyTests,
		modeAudit:       parserAudit,
		modeDiagnose:    parserAudit,
		modeStatus:      parserAudit,
		modeRunJSON:     parserAudit,
	}
	for mode, class := range want {
		if got := parserClassFor(mode); got != class {
			t.Errorf("parserClassFor(%q) = %d, want %d", mode, got, class)
		}
	}
}

// TestCommandTable_AliasesResolve pins the alias arg strings the old literal
// subcommandMap guaranteed.
func TestCommandTable_AliasesResolve(t *testing.T) {
	cases := map[string]commandMode{
		"list":      modeAudit,
		"--version": modeVersion,
		"version":   modeVersion,
	}
	for arg, want := range cases {
		if got, ok := subcommandMap[arg]; !ok || got != want {
			t.Errorf("subcommandMap[%q] = %q ok=%v, want %q", arg, got, ok, want)
		}
	}
}

// TestCommandTable_SpecsAgreeOnParserPerMode ensures every spec sharing a mode
// declares the same parser class, so the derived parserByMode map is
// unambiguous regardless of table order.
func TestCommandTable_SpecsAgreeOnParserPerMode(t *testing.T) {
	seen := map[commandMode]parserClass{}
	for _, s := range commandTable {
		if prev, ok := seen[s.mode]; ok && prev != s.parser {
			t.Errorf("mode %q has conflicting parser classes %d and %d", s.mode, prev, s.parser)
		}
		seen[s.mode] = s.parser
	}
}
