// ABOUTME: refuseToStartOutcome is a routable, non-retryable OutcomeFail for every
// ABOUTME: refuse-to-start kind; the writable_paths text is unchanged.
package handlers

import (
	"errors"
	"strings"
	"testing"

	"github.com/2389-research/tracker/pipeline"
)

func TestRefuseToStartOutcome_Kinds(t *testing.T) {
	h := NewCodergenHandler(nil, t.TempDir())
	node := &pipeline.Node{ID: "Answer"}
	cases := map[string]string{
		refuseNoBackend:          "backend refuse-to-start",
		refuseBackendUnavailable: "backend refuse-to-start",
		refuseReadOnlyACP:        "tool_access read_only refuse-to-start",
		refuseWritablePaths:      "writable_paths refuse-to-start",
	}
	for kind, label := range cases {
		cause := errors.New("cause for " + kind)
		out, err := h.refuseToStartOutcome(kind, node, cause, "prompt", t.TempDir())
		if err != nil {
			t.Fatalf("%s: unexpected error %v", kind, err)
		}
		if out.Status != pipeline.OutcomeFail {
			t.Errorf("%s: status %q, want fail (routable, not retry)", kind, out.Status)
		}
		for _, want := range []string{`node "Answer"`, label, "not retryable", cause.Error()} {
			if !strings.Contains(out.FailureReason, want) {
				t.Errorf("%s: FailureReason %q missing %q", kind, out.FailureReason, want)
			}
		}
		if out.ContextUpdates[pipeline.ContextKeyLastResponse] != out.FailureReason {
			t.Errorf("%s: last_response must carry the refusal message", kind)
		}
	}
}

func TestJailRefusedOutcome_Unchanged(t *testing.T) {
	h := NewCodergenHandler(nil, t.TempDir())
	out, err := h.jailRefusedOutcome(errors.New("landlock unavailable"), &pipeline.Node{ID: "N"}, "p", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	want := `node "N": writable_paths refuse-to-start (not retryable — a host capability/config condition): landlock unavailable`
	if out.FailureReason != want {
		t.Fatalf("FailureReason = %q, want %q", out.FailureReason, want)
	}
}

func TestRefuseToStartError_Unwraps(t *testing.T) {
	cause := errors.New("no backend available")
	err := error(&refuseToStartError{Kind: refuseNoBackend, Err: cause})
	var r *refuseToStartError
	if !errors.As(err, &r) || !errors.Is(err, cause) || err.Error() != cause.Error() {
		t.Fatalf("refuseToStartError must As/Is/print its cause: %v", err)
	}
}
