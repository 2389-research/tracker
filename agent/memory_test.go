package agent

import (
	"fmt"
	"strings"
	"testing"
)

func TestEpisodeLogSummaryAndSerialization(t *testing.T) {
	var log EpisodeLog
	log.Record("read", `{"path":"main.go"}`, "ok", false)
	log.Record("write", `{"path":"main.go"}`, "permission denied", true)

	summary := log.Summary()
	if summary == "" {
		t.Fatal("expected non-empty episode summary")
	}
	if got := SerializeEpisodeSummaries([]string{summary}); got == "" || got == "[]" {
		t.Fatalf("unexpected serialized summaries: %q", got)
	}
}

func TestEpisodeLogSummaryCapsArgsAndRenderedSize(t *testing.T) {
	var log EpisodeLog
	for i := 0; i < 64; i++ {
		log.Record("write", fmt.Sprintf(`{"path":"x","content":"%s"}`, strings.Repeat("c", 500)), "ok", false)
	}
	summary := log.Summary()
	if len([]rune(summary)) > maxEpisodeLogSummaryRunes {
		t.Fatalf("expected summary length <= %d runes, got %d", maxEpisodeLogSummaryRunes, len([]rune(summary)))
	}
	if strings.Contains(summary, strings.Repeat("c", 250)) {
		t.Fatalf("expected tool args to be truncated in summary: %q", summary)
	}
}

func TestParseEpisodeSummariesFallback(t *testing.T) {
	got := ParseEpisodeSummaries("single summary")
	if len(got) != 1 || got[0] != "single summary" {
		t.Fatalf("unexpected fallback parse result: %#v", got)
	}
}

func TestEpisodeSummariesNormalizationCapsCount(t *testing.T) {
	var in []string
	for i := 1; i <= 12; i++ {
		in = append(in, fmt.Sprintf("summary %d", i))
	}
	got := ParseEpisodeSummaries(SerializeEpisodeSummaries(in))
	if len(got) != maxEpisodeSummaryCount {
		t.Fatalf("expected %d summaries after cap, got %d", maxEpisodeSummaryCount, len(got))
	}
	if got[0] != "summary 5" || got[len(got)-1] != "summary 12" {
		t.Fatalf("expected to keep newest summaries, got %#v", got)
	}
}

func TestEpisodeSummariesNormalizationCapsTotalRunes(t *testing.T) {
	in := []string{
		strings.Repeat("a", maxEpisodeSummaryTotalRunes),
		strings.Repeat("b", maxEpisodeSummaryTotalRunes),
	}
	got := ParseEpisodeSummaries(SerializeEpisodeSummaries(in))
	if len(got) != 1 {
		t.Fatalf("expected oldest summary to be dropped by rune budget, got %#v", got)
	}
	if got[0] != strings.Repeat("b", maxEpisodeSummaryTotalRunes) {
		t.Fatalf("expected newest summary to remain, got len=%d", len([]rune(got[0])))
	}
}

func TestEpisodeSummariesNormalizationDropsOldestWhenOverRuneBudget(t *testing.T) {
	in := []string{
		strings.Repeat("a", 2500),
		strings.Repeat("b", 2500),
	}
	got := ParseEpisodeSummaries(SerializeEpisodeSummaries(in))
	if len(got) != 1 {
		t.Fatalf("expected only newest summary to remain, got %d", len(got))
	}
	if got[0] != strings.Repeat("b", 2500) {
		t.Fatalf("expected oldest summary to be dropped, got len=%d", len([]rune(got[0])))
	}
}

func TestEpisodeSummariesNormalizationTruncatesSingleOversizedEntry(t *testing.T) {
	in := []string{strings.Repeat("x", maxEpisodeSummaryTotalRunes+50)}
	got := ParseEpisodeSummaries(SerializeEpisodeSummaries(in))
	if len(got) != 1 {
		t.Fatalf("expected one summary, got %d", len(got))
	}
	if len([]rune(got[0])) > maxEpisodeSummaryTotalRunes {
		t.Fatalf("expected single summary to be capped at %d runes, got %d", maxEpisodeSummaryTotalRunes, len([]rune(got[0])))
	}
}
