package main

import (
	"strings"
	"testing"
)

func TestRenderHeaderContains2389Branding(t *testing.T) {
	h := renderHeader()
	if !strings.Contains(h, "2389") {
		t.Fatalf("expected '2389' in header, got:\n%s", h)
	}
}

func TestRenderStartupBannerContainsHeaderAndTagline(t *testing.T) {
	b := renderStartupBanner()
	if !strings.Contains(b, "2389") {
		t.Fatalf("expected '2389' in startup banner, got:\n%s", b)
	}
	// Should contain at least one tagline (non-empty line after header).
	lines := strings.Split(b, "\n")
	hasContent := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed != "" && !strings.Contains(trimmed, "║") && !strings.Contains(trimmed, "╔") &&
			!strings.Contains(trimmed, "╚") && !strings.Contains(trimmed, "2389") &&
			!strings.Contains(trimmed, "█") {
			hasContent = true
			break
		}
	}
	if !hasContent {
		t.Fatalf("expected tagline content in startup banner, got:\n%s", b)
	}
}

func TestTaglinesNotEmpty(t *testing.T) {
	tags := taglines()
	if len(tags) == 0 {
		t.Fatal("expected at least one tagline")
	}
	for i, tag := range tags {
		if strings.TrimSpace(tag) == "" {
			t.Fatalf("tagline[%d] is blank", i)
		}
	}
}

func TestRandomTaglineReturnsFromPool(t *testing.T) {
	tag := randomTagline()
	if tag == "" {
		t.Fatal("randomTagline returned empty string")
	}
	pool := taglines()
	found := false
	for _, candidate := range pool {
		if candidate == tag {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("randomTagline returned %q which is not in the pool", tag)
	}
}
