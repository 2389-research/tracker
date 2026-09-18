// ABOUTME: Doctor warning for writable_paths_mode: prefer nodes on a host that
// ABOUTME: cannot enforce the Landlock jail (#648 spec C7 / C8).
package tracker

import (
	"errors"
	"strings"
	"testing"

	"github.com/2389-research/tracker/pipeline"
)

func graphWithJailNodes() *pipeline.Graph {
	g := pipeline.NewGraph("jail")
	g.AddNode(&pipeline.Node{ID: "Prefer", Shape: "box", Attrs: map[string]string{
		"writable_paths": ".git/**", pipeline.AttrWritablePathsMode: pipeline.WritablePathsModePrefer,
	}})
	g.AddNode(&pipeline.Node{ID: "Require", Shape: "box", Attrs: map[string]string{
		"writable_paths": ".git/**",
	}})
	g.AddNode(&pipeline.Node{ID: "Plain", Shape: "box", Attrs: map[string]string{}})
	// A prefer node on an out-of-process backend refuses in both modes — it
	// never degrades, so doctor must not call it a degrade candidate.
	g.AddNode(&pipeline.Node{ID: "PreferACP", Shape: "box", Attrs: map[string]string{
		"writable_paths": ".git/**", pipeline.AttrWritablePathsMode: pipeline.WritablePathsModePrefer, "backend": "acp",
	}})
	return g
}

// C7: on a host whose Landlock probe fails, doctor warns once per prefer node
// (and only for prefer nodes) that it will run UNJAILED here.
func TestDoctor_C7_PreferWarnsWithoutLandlock(t *testing.T) {
	out := CheckResult{Name: "Pipeline File", Status: CheckStatusOK, Message: "x is valid"}
	appendJailDegradeWarnings(&out, graphWithJailNodes(), func() error { return errors.New("landlock ABI v3 not available") })
	var warns []CheckDetail
	for _, d := range out.Details {
		if d.Status == CheckStatusWarn {
			warns = append(warns, d)
		}
	}
	if len(warns) != 1 {
		t.Fatalf("got %d warnings, want 1 (only the prefer node): %+v", len(warns), out.Details)
	}
	msg := warns[0].Message
	for _, want := range []string{`node "Prefer"`, "UNJAILED", "landlock ABI v3 not available", "writable_paths_mode: require", "jail_degraded"} {
		if !strings.Contains(msg, want) {
			t.Errorf("warning missing %q: %s", want, msg)
		}
	}
	if strings.Contains(msg, `"Require"`) || strings.Contains(msg, `"Plain"`) || strings.Contains(msg, `"PreferACP"`) {
		t.Errorf("warning names a non-prefer node: %s", msg)
	}
	if out.Status != CheckStatusWarn {
		t.Errorf("status = %q, want warn", out.Status)
	}
}

// C7: when the host CAN enforce the jail there is nothing to warn about.
func TestDoctor_C7_PreferSilentWithLandlock(t *testing.T) {
	out := CheckResult{Name: "Pipeline File", Status: CheckStatusOK, Message: "x is valid"}
	appendJailDegradeWarnings(&out, graphWithJailNodes(), func() error { return nil })
	if len(out.Details) != 0 || out.Status != CheckStatusOK {
		t.Fatalf("warned on a Landlock host: %+v", out)
	}
}
