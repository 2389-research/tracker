package agent

import "testing"

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

func TestParseEpisodeSummariesFallback(t *testing.T) {
	got := ParseEpisodeSummaries("single summary")
	if len(got) != 1 || got[0] != "single summary" {
		t.Fatalf("unexpected fallback parse result: %#v", got)
	}
}
