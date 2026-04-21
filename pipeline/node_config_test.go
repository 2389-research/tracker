// ABOUTME: Tests for the typed NodeConfig accessors — verifies parsing, defaults,
// ABOUTME: graph-to-node override semantics, and set/unset detection.
package pipeline

import (
	"testing"
	"time"
)

func TestAgentConfig_EmptyNode(t *testing.T) {
	n := &Node{Attrs: map[string]string{}}
	cfg := n.AgentConfig(nil)

	// Every field should be zero / empty for an unconfigured node.
	if cfg.Model != "" || cfg.Provider != "" || cfg.MaxTurns != 0 {
		t.Errorf("expected zero values, got Model=%q Provider=%q MaxTurns=%d",
			cfg.Model, cfg.Provider, cfg.MaxTurns)
	}
	if cfg.AutoStatus || cfg.ReflectOnErrorSet || cfg.VerifyAfterEditSet || cfg.PlanBeforeExecuteSet {
		t.Errorf("bool flags should all be false/unset, got %+v", cfg)
	}
	if cfg.CommandTimeout != 0 || cfg.MaxBudgetUSD != 0 {
		t.Errorf("numeric fields should be zero")
	}
}

func TestAgentConfig_NodeAttrsWinOverGraph(t *testing.T) {
	graphAttrs := map[string]string{
		"llm_model":    "claude-sonnet-4-6",
		"llm_provider": "anthropic",
	}
	n := &Node{Attrs: map[string]string{
		"llm_model": "gpt-5.4",
	}}

	cfg := n.AgentConfig(graphAttrs)
	if cfg.Model != "gpt-5.4" {
		t.Errorf("node model should override graph; got %q", cfg.Model)
	}
	if cfg.Provider != "anthropic" {
		t.Errorf("graph provider should propagate when node is silent; got %q", cfg.Provider)
	}
}

func TestAgentConfig_ReflectOnErrorThreeState(t *testing.T) {
	cases := []struct {
		name      string
		attrVal   string
		wantValue bool
		wantSet   bool
	}{
		{"unset", "", false, false},
		{"explicit false", "false", false, true},
		{"explicit true", "true", true, true},
		{"any other value counts as set+true", "yes", true, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			attrs := map[string]string{}
			if tc.name != "unset" {
				attrs["reflect_on_error"] = tc.attrVal
			}
			cfg := (&Node{Attrs: attrs}).AgentConfig(nil)
			if cfg.ReflectOnError != tc.wantValue || cfg.ReflectOnErrorSet != tc.wantSet {
				t.Errorf("ReflectOnError=%v Set=%v, want value=%v set=%v",
					cfg.ReflectOnError, cfg.ReflectOnErrorSet, tc.wantValue, tc.wantSet)
			}
		})
	}
}

func TestAgentConfig_PlanAliasOnlyWhenExplicitAbsent(t *testing.T) {
	// "plan" shorthand is honored only when "plan_before_execute" is absent.
	n := &Node{Attrs: map[string]string{
		"plan_before_execute": "true",
		"plan":                "false",
	}}
	cfg := n.AgentConfig(nil)
	if !cfg.PlanBeforeExecute || !cfg.PlanBeforeExecuteSet {
		t.Errorf("explicit plan_before_execute=true should win over plan=false")
	}

	// Alias applies when only "plan" is set.
	n2 := &Node{Attrs: map[string]string{"plan": "true"}}
	cfg2 := n2.AgentConfig(nil)
	if !cfg2.PlanBeforeExecute || !cfg2.PlanBeforeExecuteSet {
		t.Errorf("plan=true (shorthand) should enable PlanBeforeExecute")
	}
}

func TestAgentConfig_VerifyAfterEditGraphNodePrecedence(t *testing.T) {
	// graph enables, node disables → node wins.
	n := &Node{Attrs: map[string]string{"verify_after_edit": "false"}}
	cfg := n.AgentConfig(map[string]string{"verify_after_edit": "true"})
	if cfg.VerifyAfterEdit || !cfg.VerifyAfterEditSet {
		t.Errorf("node false should override graph true; got value=%v set=%v",
			cfg.VerifyAfterEdit, cfg.VerifyAfterEditSet)
	}
}

func TestAgentConfig_NumericParseFailuresAreLenient(t *testing.T) {
	// Unparseable numeric strings produce zero values, no panic.
	n := &Node{Attrs: map[string]string{
		"max_turns":                    "not-a-number",
		"command_timeout":              "forever",
		"max_budget_usd":               "$$",
		"max_verify_retries":           "-3",
		"context_compaction_threshold": "bad",
	}}
	cfg := n.AgentConfig(nil)
	if cfg.MaxTurns != 0 || cfg.CommandTimeout != 0 || cfg.MaxBudgetUSD != 0 {
		t.Errorf("bad numerics should fall back to zero; got MaxTurns=%d Timeout=%v Budget=%v",
			cfg.MaxTurns, cfg.CommandTimeout, cfg.MaxBudgetUSD)
	}
	if cfg.MaxVerifyRetries != 0 {
		t.Errorf("negative max_verify_retries should be rejected; got %d", cfg.MaxVerifyRetries)
	}
	if cfg.CompactionThreshold != 0 {
		t.Errorf("bad threshold should stay zero; got %v", cfg.CompactionThreshold)
	}
}

func TestAgentConfig_NumericParseSuccesses(t *testing.T) {
	n := &Node{Attrs: map[string]string{
		"max_turns":                    "42",
		"command_timeout":              "15m",
		"max_budget_usd":               "5.25",
		"max_verify_retries":           "3",
		"context_compaction_threshold": "0.8",
	}}
	cfg := n.AgentConfig(nil)
	if cfg.MaxTurns != 42 {
		t.Errorf("MaxTurns = %d, want 42", cfg.MaxTurns)
	}
	if cfg.CommandTimeout != 15*time.Minute {
		t.Errorf("CommandTimeout = %v, want 15m", cfg.CommandTimeout)
	}
	if cfg.MaxBudgetUSD != 5.25 {
		t.Errorf("MaxBudgetUSD = %v, want 5.25", cfg.MaxBudgetUSD)
	}
	if cfg.MaxVerifyRetries != 3 {
		t.Errorf("MaxVerifyRetries = %d, want 3", cfg.MaxVerifyRetries)
	}
	if cfg.CompactionThreshold != 0.8 {
		t.Errorf("CompactionThreshold = %v, want 0.8", cfg.CompactionThreshold)
	}
}

func TestToolConfig_Basic(t *testing.T) {
	n := &Node{Attrs: map[string]string{
		"tool_command":  "make test",
		"output_limit":  "65536",
		"working_dir":   "/tmp/build",
		"tool_pass_env": "PATH,HOME",
	}}
	cfg := n.ToolConfig()
	if cfg.Command != "make test" || cfg.OutputLimit != 65536 ||
		cfg.WorkingDir != "/tmp/build" || cfg.PassEnv != "PATH,HOME" {
		t.Errorf("tool config mismatch: %+v", cfg)
	}
}

func TestHumanConfig_TimeoutParsing(t *testing.T) {
	n := &Node{Attrs: map[string]string{
		"mode":           "interview",
		"default":        "Approve",
		"prompt":         "Confirm",
		"questions_key":  "qs",
		"answers_key":    "ans",
		"timeout":        "30m",
		"timeout_action": "fail",
	}}
	cfg := n.HumanConfig()
	if cfg.Mode != "interview" || cfg.DefaultChoice != "Approve" ||
		cfg.Timeout != 30*time.Minute || cfg.TimeoutAction != "fail" {
		t.Errorf("human config mismatch: %+v", cfg)
	}

	// Bad timeout string → zero duration, no panic.
	bad := (&Node{Attrs: map[string]string{"timeout": "not-a-duration"}}).HumanConfig()
	if bad.Timeout != 0 {
		t.Errorf("bad timeout should be zero, got %v", bad.Timeout)
	}
}

func TestParallelConfig_StraightThrough(t *testing.T) {
	n := &Node{Attrs: map[string]string{
		"parallel_targets": "A,B,C",
		"fan_in_sources":   "X,Y",
	}}
	cfg := n.ParallelConfig()
	if cfg.ParallelTargets != "A,B,C" || cfg.FanInSources != "X,Y" {
		t.Errorf("parallel config mismatch: %+v", cfg)
	}
}

func TestRetryConfig_NodeOverridesGraph(t *testing.T) {
	graph := map[string]string{
		"default_retry_policy": "patient",
		"default_max_retry":    "5",
	}
	n := &Node{Attrs: map[string]string{
		"retry_policy": "aggressive",
		"max_retries":  "2",
		"base_delay":   "500ms",
	}}
	rc := n.RetryConfig(graph)
	if rc.PolicyName != "aggressive" {
		t.Errorf("PolicyName = %q, want aggressive", rc.PolicyName)
	}
	if rc.MaxRetries != 2 || !rc.MaxRetriesSet {
		t.Errorf("MaxRetries = %d set=%v, want 2/true", rc.MaxRetries, rc.MaxRetriesSet)
	}
	if rc.BaseDelay != 500*time.Millisecond || !rc.BaseDelaySet {
		t.Errorf("BaseDelay = %v set=%v, want 500ms/true", rc.BaseDelay, rc.BaseDelaySet)
	}
}

func TestRetryConfig_GraphDefaultsWhenNodeSilent(t *testing.T) {
	graph := map[string]string{
		"default_retry_policy": "standard",
		"default_max_retry":    "4",
	}
	n := &Node{Attrs: map[string]string{}}
	rc := n.RetryConfig(graph)
	if rc.PolicyName != "standard" {
		t.Errorf("PolicyName should fall back to graph default; got %q", rc.PolicyName)
	}
	if rc.MaxRetries != 4 || !rc.MaxRetriesSet {
		t.Errorf("MaxRetries = %d set=%v, want 4/true", rc.MaxRetries, rc.MaxRetriesSet)
	}
	if rc.BaseDelaySet {
		t.Errorf("BaseDelay should be unset when neither node nor graph set it")
	}
}

func TestRetryConfig_InvalidMaxRetriesLeavesUnset(t *testing.T) {
	n := &Node{Attrs: map[string]string{"max_retries": "not-a-number"}}
	rc := n.RetryConfig(nil)
	if rc.MaxRetriesSet {
		t.Errorf("unparseable max_retries should leave MaxRetriesSet=false; got true")
	}
}

// Regression: when node max_retries is present but unparseable, fall through
// to graph default rather than dropping the whole field. Matches the
// pre-refactor cascade behavior of Engine.maxRetries. (Codex P2 on PR #148.)
func TestRetryConfig_UnparseableNodeMaxRetriesCascadesToGraph(t *testing.T) {
	graph := map[string]string{"default_max_retry": "7"}
	n := &Node{Attrs: map[string]string{"max_retries": "not-a-number"}}
	rc := n.RetryConfig(graph)
	if !rc.MaxRetriesSet || rc.MaxRetries != 7 {
		t.Errorf("bad node value should cascade to graph default 7; got value=%d set=%v",
			rc.MaxRetries, rc.MaxRetriesSet)
	}
}

// Regression: BaseDelay honors default_base_delay at graph level for contract
// consistency with PolicyName and MaxRetries. (CodeRabbit major on PR #148.)
func TestRetryConfig_BaseDelayGraphFallback(t *testing.T) {
	graph := map[string]string{"default_base_delay": "2s"}
	n := &Node{Attrs: map[string]string{}}
	rc := n.RetryConfig(graph)
	if !rc.BaseDelaySet || rc.BaseDelay != 2*time.Second {
		t.Errorf("BaseDelay should fall back to graph default; got value=%v set=%v",
			rc.BaseDelay, rc.BaseDelaySet)
	}
	// Node-level value still wins when present.
	n2 := &Node{Attrs: map[string]string{"base_delay": "500ms"}}
	rc2 := n2.RetryConfig(graph)
	if rc2.BaseDelay != 500*time.Millisecond {
		t.Errorf("node value should override graph default; got %v", rc2.BaseDelay)
	}
}
