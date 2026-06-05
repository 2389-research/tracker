// ABOUTME: Negative-control regression guard for issue #298 — the build-context
// ABOUTME: file wiring (seed / range-marker / append / read-first / allowlist).
package pipeline

import (
	"strings"
	"testing"
)

// Test 1 — Setup seeds .ai/build/build-context.md with a redirect (not a mention).
func TestBuildProductSetupSeedsBuildContext(t *testing.T) {
	g := loadBuildProduct(t)
	cmd := g.Nodes["Setup"].Attrs["tool_command"]
	if !strings.Contains(cmd, "build-context.md") {
		t.Error("Setup does not reference build-context.md (#298)")
	}
	if !strings.Contains(cmd, "> .ai/build/build-context.md") {
		t.Error("Setup has no redirect seeding build-context.md (#298)")
	}
}

// Test 2 — PickNextMilestone writes the range marker via the non-leaking guard.
func TestBuildProductPickNextWritesStartMarker(t *testing.T) {
	g := loadBuildProduct(t)
	cmd := g.Nodes["PickNextMilestone"].Attrs["tool_command"]
	if !strings.Contains(cmd, "> .ai/build/milestone-start-sha") {
		t.Error("PickNextMilestone has no truncating redirect writing milestone-start-sha (#298)")
	}
	// The bare `git rev-parse HEAD || echo ""` leaks the literal "HEAD" to stdout
	// on a commitless repo; --verify --quiet prints nothing and exits non-zero.
	if !strings.Contains(cmd, "rev-parse --verify --quiet") {
		t.Error("PickNextMilestone must capture START via `git rev-parse --verify --quiet` (#298)")
	}
}

// Test 3 — MarkMilestoneDone APPENDS an entry, cleans up the marker, and keeps
// the routing printf last so it survives output truncation.
func TestBuildProductMarkMilestoneDoneAppends(t *testing.T) {
	g := loadBuildProduct(t)
	cmd := g.Nodes["MarkMilestoneDone"].Attrs["tool_command"]
	if !strings.Contains(cmd, "git diff --name-only") {
		t.Error("MarkMilestoneDone does not compute the file list via git diff --name-only (#298)")
	}
	// APPEND (>>), not truncate (>): a truncating redirect would silently
	// discard prior milestones' entries.
	if !strings.Contains(cmd, ">> .ai/build/build-context.md") {
		t.Error("MarkMilestoneDone does not APPEND to build-context.md — per-milestone history would be lost (#298)")
	}
	if !strings.Contains(cmd, "rm -f .ai/build/milestone-start-sha") {
		t.Error("MarkMilestoneDone does not remove the milestone-start-sha marker after use (#298)")
	}
	// The append must precede the terminal routing marker (CLAUDE.md: marker last).
	appendIdx := strings.Index(cmd, ">> .ai/build/build-context.md")
	markerIdx := strings.LastIndex(cmd, `printf "milestone-`)
	if markerIdx == -1 {
		t.Fatal("MarkMilestoneDone lost its terminal printf routing marker (#298 regression)")
	}
	if appendIdx == -1 || appendIdx > markerIdx {
		t.Error("build-context.md append occurs after the terminal printf marker — routing token may be truncated (#298)")
	}
}

// Test 4 — the three build-loop agents read build-context.md first.
func TestBuildProductAgentPromptsReadBuildContextFirst(t *testing.T) {
	g := loadBuildProduct(t)
	for _, id := range []string{"Implement", "FixMilestone", "VerifyMilestone"} {
		if !strings.Contains(g.Nodes[id].Attrs["prompt"], "build-context.md") {
			t.Errorf("%s prompt does not instruct reading build-context.md (#298)", id)
		}
	}
}

// Test 5 — FinalSpecCheck allowlists build-context.md so it isn't flagged.
func TestBuildProductFinalSpecCheckAllowlistsBuildContext(t *testing.T) {
	g := loadBuildProduct(t)
	if !strings.Contains(g.Nodes["FinalSpecCheck"].Attrs["prompt"], "build-context.md") {
		t.Error("FinalSpecCheck does not allowlist build-context.md (#298)")
	}
}
