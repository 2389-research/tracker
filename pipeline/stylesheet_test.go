// ABOUTME: Tests for the CSS-like model stylesheet parser and resolver.
// ABOUTME: Validates universal, class, and ID selectors with specificity-based override rules.
package pipeline

import "testing"

func TestParseStylesheetUniversal(t *testing.T) {
	input := `* { llm_model: claude-sonnet-4-5; }`
	ss, err := ParseStylesheet(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(ss.Rules) != 1 {
		t.Fatalf("expected 1 rule, got %d", len(ss.Rules))
	}
	if ss.Rules[0].Selector != "*" {
		t.Errorf("expected '*', got %q", ss.Rules[0].Selector)
	}
	if ss.Rules[0].Properties["llm_model"] != "claude-sonnet-4-5" {
		t.Errorf("expected 'claude-sonnet-4-5'")
	}
}

func TestParseStylesheetClass(t *testing.T) {
	input := `.code { llm_model: claude-opus-4-6; temperature: 0.2; }`
	ss, err := ParseStylesheet(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(ss.Rules) != 1 {
		t.Fatalf("expected 1 rule")
	}
	if ss.Rules[0].Selector != ".code" {
		t.Errorf("expected '.code'")
	}
	if ss.Rules[0].Properties["temperature"] != "0.2" {
		t.Errorf("expected '0.2'")
	}
}

func TestParseStylesheetID(t *testing.T) {
	input := `#review { reasoning_effort: high; }`
	ss, err := ParseStylesheet(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(ss.Rules) != 1 {
		t.Fatalf("expected 1 rule")
	}
	if ss.Rules[0].Selector != "#review" {
		t.Errorf("expected '#review'")
	}
}

func TestParseStylesheetMultipleRules(t *testing.T) {
	input := "* { llm_model: claude-sonnet-4-5; }\n.code { llm_model: claude-opus-4-6; }\n#review { reasoning_effort: high; }"
	ss, err := ParseStylesheet(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(ss.Rules) != 3 {
		t.Fatalf("expected 3 rules, got %d", len(ss.Rules))
	}
}

func TestParseStylesheetEmpty(t *testing.T) {
	ss, err := ParseStylesheet("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(ss.Rules) != 0 {
		t.Errorf("expected 0 rules")
	}
}

func TestStylesheetSpecificity(t *testing.T) {
	if specificityOf("*") >= specificityOf(".code") {
		t.Error("* should be less than .class")
	}
	if specificityOf(".code") >= specificityOf("#review") {
		t.Error(".class should be less than #id")
	}
}

func TestResolveStyleUniversalOnly(t *testing.T) {
	ss, _ := ParseStylesheet(`* { llm_model: claude-sonnet-4-5; temperature: 0.5; }`)
	node := &Node{ID: "gen", Attrs: map[string]string{}}
	resolved := ss.Resolve(node)
	if resolved["llm_model"] != "claude-sonnet-4-5" {
		t.Errorf("expected 'claude-sonnet-4-5'")
	}
	if resolved["temperature"] != "0.5" {
		t.Errorf("expected '0.5'")
	}
}

func TestResolveStyleClassOverridesUniversal(t *testing.T) {
	ss, _ := ParseStylesheet("* { llm_model: claude-sonnet-4-5; temperature: 0.5; }\n.code { llm_model: claude-opus-4-6; }")
	node := &Node{ID: "gen", Attrs: map[string]string{"class": "code"}}
	resolved := ss.Resolve(node)
	if resolved["llm_model"] != "claude-opus-4-6" {
		t.Errorf("expected class override")
	}
	if resolved["temperature"] != "0.5" {
		t.Errorf("expected universal temperature")
	}
}

func TestResolveStyleIDOverridesClass(t *testing.T) {
	ss, _ := ParseStylesheet("* { llm_model: claude-sonnet-4-5; }\n.code { llm_model: claude-opus-4-6; }\n#special { llm_model: gpt-4o; }")
	node := &Node{ID: "special", Attrs: map[string]string{"class": "code"}}
	resolved := ss.Resolve(node)
	if resolved["llm_model"] != "gpt-4o" {
		t.Errorf("expected ID override")
	}
}

func TestResolveStyleNodeAttrsOverrideAll(t *testing.T) {
	ss, _ := ParseStylesheet("* { llm_model: claude-sonnet-4-5; }\n#gen { llm_model: gpt-4o; }")
	node := &Node{ID: "gen", Attrs: map[string]string{"llm_model": "explicit-model"}}
	resolved := ss.Resolve(node)
	if resolved["llm_model"] != "explicit-model" {
		t.Errorf("expected explicit attr override")
	}
}

func TestResolveStyleNoMatchingRules(t *testing.T) {
	ss, _ := ParseStylesheet(`.code { llm_model: claude-opus-4-6; }`)
	node := &Node{ID: "gen", Attrs: map[string]string{}}
	resolved := ss.Resolve(node)
	if _, ok := resolved["llm_model"]; ok {
		t.Error("expected no llm_model")
	}
}

func TestResolveStyleMultipleClasses(t *testing.T) {
	ss, _ := ParseStylesheet(".code { llm_model: claude-opus-4-6; }\n.fast { temperature: 0.1; }")
	node := &Node{ID: "gen", Attrs: map[string]string{"class": "code fast"}}
	resolved := ss.Resolve(node)
	if resolved["llm_model"] != "claude-opus-4-6" {
		t.Errorf("expected .code model")
	}
	if resolved["temperature"] != "0.1" {
		t.Errorf("expected .fast temperature")
	}
}
