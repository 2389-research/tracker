// ABOUTME: Pins the release build-identity contract (SIFT-SUB-15-01, #593).
// ABOUTME: The goreleaser `tracker` build must stamp main.version/commit/date.
package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// requiredTrackerLdflags are the identity injections a published `tracker`
// archive must carry so it does not report `dev`/`unknown`/`unknown` (false
// provenance that also disables `tracker update` and the update-check hint).
var requiredTrackerLdflags = []string{
	"-X main.version={{ .Version }}",
	"-X main.commit={{ .ShortCommit }}",
	"-X main.date={{ .Date }}",
}

// TestGoreleaserStampsTrackerIdentity guards against a regression where the
// goreleaser `tracker` build drops its version/commit/date ldflags and ships a
// binary whose `tracker version` reports the `dev` defaults from main.go.
func TestGoreleaserStampsTrackerIdentity(t *testing.T) {
	block := trackerBuildBlock(t)
	for _, want := range requiredTrackerLdflags {
		if !strings.Contains(block, want) {
			t.Errorf("goreleaser `tracker` build is missing ldflag %q\nblock:\n%s", want, block)
		}
	}
}

// TestGoreleaserHomebrewTestAssertsIdentity guards the smoke test: the Homebrew
// formula test must assert the reported version, not merely exit zero.
func TestGoreleaserHomebrewTestAssertsIdentity(t *testing.T) {
	data, err := os.ReadFile(goreleaserPath(t))
	if err != nil {
		t.Fatalf("read .goreleaser.yml: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, `assert_match "tracker #{version}"`) {
		t.Error("Homebrew test must assert the binary reports the formula version, not just exit zero")
	}
	if !strings.Contains(content, `refute_match "commit: unknown"`) {
		t.Error("Homebrew test must refute an unknown commit in the shipped binary")
	}
}

// trackerBuildBlock returns the YAML lines of the `- id: tracker` build entry,
// stopping at the next build entry so assertions can't leak in from a sibling
// build (e.g. tracker-conformance).
func trackerBuildBlock(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(goreleaserPath(t))
	if err != nil {
		t.Fatalf("read .goreleaser.yml: %v", err)
	}
	lines := strings.Split(string(data), "\n")
	var block []string
	inBlock := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "- id:") {
			if inBlock {
				break // reached the next build entry
			}
			inBlock = trimmed == "- id: tracker"
		}
		if inBlock {
			block = append(block, line)
		}
	}
	if len(block) == 0 {
		t.Fatal("could not find `- id: tracker` build block in .goreleaser.yml")
	}
	return strings.Join(block, "\n")
}

func goreleaserPath(t *testing.T) string {
	t.Helper()
	// Test runs from the package dir (cmd/tracker); the config is at repo root.
	return filepath.Join("..", "..", ".goreleaser.yml")
}
