// ABOUTME: Tests for the Doctor library API — preflight health checks.
// ABOUTME: Verifies probe opt-in, provider detection, and pipeline validation.
package tracker

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDoctor_NoProbe_KeyPresent(t *testing.T) {
	workdir := t.TempDir()
	t.Setenv("ANTHROPIC_API_KEY", "sk-ant-test-12345678901234567890")

	r, err := Doctor(context.Background(), DoctorConfig{WorkDir: workdir, ProbeProviders: false})
	if err != nil {
		t.Fatalf("Doctor: %v", err)
	}
	var providersCheck *CheckResult
	for i := range r.Checks {
		if r.Checks[i].Name == "LLM Providers" {
			providersCheck = &r.Checks[i]
		}
	}
	if providersCheck == nil {
		t.Fatal("LLM Providers check not found")
	}
	if providersCheck.Status != "ok" && providersCheck.Status != "skip" {
		t.Errorf("LLM Providers status = %q, want ok or skip", providersCheck.Status)
	}
}

func TestDoctor_NoProviderKeys(t *testing.T) {
	workdir := t.TempDir()
	for _, k := range []string{"ANTHROPIC_API_KEY", "OPENAI_API_KEY", "GEMINI_API_KEY", "GOOGLE_API_KEY", "OPENAI_COMPAT_API_KEY"} {
		t.Setenv(k, "")
	}

	r, err := Doctor(context.Background(), DoctorConfig{WorkDir: workdir, ProbeProviders: false})
	if err != nil {
		t.Fatalf("Doctor: %v", err)
	}
	if r.OK {
		t.Error("expected OK=false when no providers configured")
	}
}

func TestDoctor_PipelineFileValidation(t *testing.T) {
	workdir := t.TempDir()
	pf := filepath.Join(workdir, "ok.dip")
	const src = `workflow X
  goal: "x"
  start: S
  exit: E
  agent S
    label: "S"
    prompt: "hi"
  agent E
    label: "E"
    prompt: "bye"
  S -> E
`
	must(t, os.WriteFile(pf, []byte(src), 0o644))

	r, err := Doctor(context.Background(), DoctorConfig{WorkDir: workdir, PipelineFile: pf, ProbeProviders: false})
	if err != nil {
		t.Fatalf("Doctor: %v", err)
	}
	var pipelineCheck *CheckResult
	for i := range r.Checks {
		if r.Checks[i].Name == "Pipeline File" {
			pipelineCheck = &r.Checks[i]
		}
	}
	if pipelineCheck == nil {
		t.Fatal("Pipeline File check missing when PipelineFile set")
	}
}

func TestSanitizeProviderError(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "anthropic key",
			in:   "auth failed: sk-ant-api03-abcdef1234567890abcdef",
			want: "auth failed: [redacted-key]",
		},
		{
			name: "openai key",
			in:   "invalid key sk-abcdef1234567890abcdef",
			want: "invalid key [redacted-key]",
		},
		{
			name: "google key",
			in:   "request failed AIzaSyAbcDef1234567890abcdef_01",
			want: "request failed [redacted-key]",
		},
		{
			name: "bearer token",
			in:   "401 Unauthorized: Bearer abc.def.ghi12345",
			want: "401 Unauthorized: Bearer [redacted]",
		},
		{
			name: "plain message",
			in:   "connection refused",
			want: "connection refused",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := sanitizeProviderError(c.in); got != c.want {
				t.Errorf("sanitizeProviderError(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

// TestSanitizeThenTrim_NoPartialKeyLeak verifies the sanitize-before-trim
// ordering in probeProvider. A key that straddles the trim boundary must
// not produce a leaked prefix after truncation. Regression guard for PR
// feedback on issue #106 follow-up.
func TestSanitizeThenTrim_NoPartialKeyLeak(t *testing.T) {
	// Construct a message where the key starts at char 50 and runs past
	// the 80-char truncation point. Trimming first would leave a 30-char
	// prefix of the key that's shorter than the regex minimum, so the
	// regex would miss it and the prefix would leak.
	key := "sk-ant-api03-" + strings.Repeat("A", 60)
	msg := strings.Repeat("x", 50) + key

	// Correct order: sanitize first, then trim.
	got := trimErrMsg(sanitizeProviderError(msg), 80)

	if strings.Contains(got, "sk-ant-") {
		t.Errorf("got = %q; leaked key prefix (must be redacted before trim)", got)
	}
	if !strings.Contains(got, "[redacted-key]") {
		t.Errorf("got = %q; want [redacted-key] substitution", got)
	}
}
