package agent

import (
	"encoding/json"
	"fmt"
	"strings"
)

const (
	maxEpisodeSummaryLen        = 160
	maxEpisodeSummaryCount      = 8
	maxEpisodeSummaryTotalRunes = 4000
)

// EpisodeEntry captures one tool attempt inside a session.
type EpisodeEntry struct {
	Tool    string `json:"tool"`
	Args    string `json:"args"`
	Success bool   `json:"success"`
	Summary string `json:"summary"`
}

// EpisodeLog stores structured tool-attempt records for one session.
type EpisodeLog struct {
	Entries []EpisodeEntry `json:"entries"`
}

// Record appends a tool-call episode.
func (l *EpisodeLog) Record(tool, args, output string, isError bool) {
	statusSummary := summarizeEpisodeOutput(output, isError)
	l.Entries = append(l.Entries, EpisodeEntry{
		Tool:    tool,
		Args:    compactJSON(args),
		Success: !isError,
		Summary: statusSummary,
	})
}

// Summary renders a compact multiline summary for injection into future sessions.
func (l EpisodeLog) Summary() string {
	if len(l.Entries) == 0 {
		return ""
	}
	var b strings.Builder
	for i, e := range l.Entries {
		status := "success"
		if !e.Success {
			status = "fail"
		}
		fmt.Fprintf(&b, "%d. %s args=%s outcome=%s summary=%s", i+1, e.Tool, e.Args, status, e.Summary)
		if i < len(l.Entries)-1 {
			b.WriteByte('\n')
		}
	}
	return b.String()
}

// SerializeEpisodeSummaries encodes episode summaries as a JSON array.
func SerializeEpisodeSummaries(summaries []string) string {
	summaries = normalizeEpisodeSummaries(summaries)
	if len(summaries) == 0 {
		return "[]"
	}
	b, err := json.Marshal(summaries)
	if err != nil {
		return "[]"
	}
	return string(b)
}

// ParseEpisodeSummaries decodes a JSON-array string into episode summaries.
func ParseEpisodeSummaries(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	var summaries []string
	if err := json.Unmarshal([]byte(raw), &summaries); err == nil {
		return normalizeEpisodeSummaries(summaries)
	}
	// Backward/defensive fallback: treat non-JSON value as a single summary.
	return normalizeEpisodeSummaries([]string{raw})
}

func summarizeEpisodeOutput(output string, isError bool) string {
	text := strings.TrimSpace(output)
	if text == "" {
		if isError {
			return "tool returned an error with empty output"
		}
		return "tool completed with empty output"
	}
	text = strings.ReplaceAll(text, "\n", " ")
	text = strings.Join(strings.Fields(text), " ")
	r := []rune(text)
	if len(r) > maxEpisodeSummaryLen {
		return string(r[:maxEpisodeSummaryLen]) + "…"
	}
	return text
}

func normalizeEpisodeSummaries(in []string) []string {
	out := make([]string, 0, len(in))
	runeLens := make([]int, 0, len(in))
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s != "" {
			out = append(out, s)
			runeLens = append(runeLens, len([]rune(s)))
		}
	}
	if len(out) > maxEpisodeSummaryCount {
		trimmed := len(out) - maxEpisodeSummaryCount
		out = out[len(out)-maxEpisodeSummaryCount:]
		runeLens = runeLens[trimmed:]
	}
	totalRunes := 0
	for _, n := range runeLens {
		totalRunes += n
	}
	for len(out) > 1 && totalRunes > maxEpisodeSummaryTotalRunes {
		totalRunes -= runeLens[0]
		out = out[1:]
		runeLens = runeLens[1:]
	}
	return out
}
