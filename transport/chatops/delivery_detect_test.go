// ABOUTME: Tests deliverable detection (URL surfacing) and the results-line format.
package chatops

import (
	"strings"
	"testing"
	"time"

	tracker "github.com/2389-research/tracker"
)

func TestDetectDeliverable(t *testing.T) {
	// An explicit deploy_url wins over a URL buried elsewhere.
	d := detectDeliverable(&tracker.Result{Context: map[string]string{
		"deploy_url": "https://app.example.com", "last_response": "see https://other.example",
	}})
	if d.URL != "https://app.example.com" {
		t.Fatalf("explicit key should win, got %q", d.URL)
	}

	// No explicit key → scan any value for a URL.
	d = detectDeliverable(&tracker.Result{Context: map[string]string{
		"last_response": "deployed to https://preview.example.com/abc — enjoy!",
	}})
	if d.URL != "https://preview.example.com/abc" {
		t.Fatalf("scanned URL = %q", d.URL)
	}

	// A delivery summary, no URL.
	d = detectDeliverable(&tracker.Result{Context: map[string]string{"delivery": "shipped 3 files"}})
	if d.Summary != "shipped 3 files" || d.URL != "" {
		t.Fatalf("summary case: %+v", d)
	}

	// Nothing notable.
	d = detectDeliverable(&tracker.Result{Context: map[string]string{"foo": "bar"}})
	if d.URL != "" || d.Summary != "" {
		t.Fatalf("nothing case: %+v", d)
	}
}

func TestDeliverSuccess_SurfacesURLAndCost(t *testing.T) {
	ui := newFakeUI()
	deliverSuccess(ui, &tracker.Result{
		Status:  "success",
		Cost:    &tracker.CostReport{TotalUSD: 1.87},
		Context: map[string]string{"deploy_url": "https://app.example.com"},
	})
	got := strings.Join(ui.posts, "\n")
	for _, want := range []string{"✅ done", "$1.87", "https://app.example.com", "iterate"} {
		if !strings.Contains(got, want) {
			t.Errorf("results message missing %q:\n%s", want, got)
		}
	}
}

func TestShortDur(t *testing.T) {
	if got := shortDur(42 * time.Second); got != "42s" {
		t.Fatalf("42s = %q", got)
	}
	if got := shortDur(125 * time.Second); got != "2m05s" {
		t.Fatalf("125s = %q", got)
	}
}
