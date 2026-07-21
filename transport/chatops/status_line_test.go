// ABOUTME: Tests the status-command progress digest and the live-card accessor.
package chatops

import (
	"strings"
	"testing"
)

func TestStatusLine(t *testing.T) {
	cases := []struct {
		name string
		card StatusCard
		want []string // substrings that must appear
		bare bool      // want == "" (empty)
	}{
		{name: "full", card: StatusCard{DoneCount: 3, TotalCount: 8, CostUSD: 1.5, CurrentNode: "Implement"},
			want: []string{"3/8 steps", "$1.50", "Implement"}},
		{name: "no-cost", card: StatusCard{DoneCount: 1, TotalCount: 4},
			want: []string{"1/4 steps"}},
		{name: "empty", card: StatusCard{}, bare: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := statusLine(tc.card)
			if tc.bare {
				if got != "" {
					t.Fatalf("want empty, got %q", got)
				}
				return
			}
			for _, w := range tc.want {
				if !strings.Contains(got, w) {
					t.Errorf("statusLine=%q missing %q", got, w)
				}
			}
		})
	}
}

func TestStatusTrackerCard(t *testing.T) {
	st := newStatusTracker(nopRenderer{}, "build_product", 5.0)
	st.mu.Lock()
	st.card.DoneCount = 2
	st.card.TotalCount = 6
	st.card.CostUSD = 0.42
	st.mu.Unlock()
	c := st.Card()
	if c.DoneCount != 2 || c.TotalCount != 6 || c.CostUSD != 0.42 {
		t.Fatalf("Card() did not reflect state: %+v", c)
	}
}

type nopRenderer struct{}

func (nopRenderer) UpsertStatus(StatusCard) error { return nil }
