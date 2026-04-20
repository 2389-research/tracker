package pipeline

import (
	"strings"
	"testing"
)

func TestExtractParamsFromGraphAttrs(t *testing.T) {
	attrs := map[string]string{
		"goal":       "build",
		"params.foo": "bar",
		"params.env": "prod",
	}
	params := ExtractParamsFromGraphAttrs(attrs)
	if len(params) != 2 {
		t.Fatalf("len(params) = %d, want 2", len(params))
	}
	if params["foo"] != "bar" || params["env"] != "prod" {
		t.Fatalf("unexpected params: %#v", params)
	}
}

func TestApplyGraphParamOverrides(t *testing.T) {
	g := NewGraph("test")
	g.Attrs["params.foo"] = "default"
	g.Attrs["params.env"] = "dev"

	err := ApplyGraphParamOverrides(g, map[string]string{"foo": "override"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := g.Attrs["params.foo"]; got != "override" {
		t.Fatalf("params.foo = %q, want override", got)
	}
	if got := g.Attrs["params.env"]; got != "dev" {
		t.Fatalf("params.env = %q, want dev", got)
	}
}

func TestApplyGraphParamOverrides_UnknownParam(t *testing.T) {
	g := NewGraph("test")
	g.Attrs["params.foo"] = "default"

	err := ApplyGraphParamOverrides(g, map[string]string{"missing": "x"})
	if err == nil {
		t.Fatal("expected error for unknown param")
	}
	if !strings.Contains(err.Error(), "unknown param") {
		t.Fatalf("error = %v, want unknown param", err)
	}
}
