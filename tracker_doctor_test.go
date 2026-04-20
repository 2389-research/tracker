// ABOUTME: Tests for the Doctor library API — preflight health checks.
// ABOUTME: Verifies probe opt-in, provider detection, and pipeline validation.
package tracker

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/2389-research/tracker/llm"
)

func TestDoctor_NoProbe_KeyPresent(t *testing.T) {
	workdir := t.TempDir()
	t.Setenv("ANTHROPIC_API_KEY", "sk-ant-test-12345678901234567890")

	r, err := Doctor(DoctorConfig{WorkDir: workdir, ProbeProviders: false})
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

	r, err := Doctor(DoctorConfig{WorkDir: workdir, ProbeProviders: false})
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

	r, err := Doctor(DoctorConfig{WorkDir: workdir, PipelineFile: pf, ProbeProviders: false})
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

type doctorProbeTestAdapter struct {
	completeErr error
}

func (a *doctorProbeTestAdapter) Name() string { return "test" }

func (a *doctorProbeTestAdapter) Complete(_ context.Context, _ *llm.Request) (*llm.Response, error) {
	return nil, a.completeErr
}

func (a *doctorProbeTestAdapter) Stream(_ context.Context, _ *llm.Request) <-chan llm.StreamEvent {
	ch := make(chan llm.StreamEvent)
	close(ch)
	return ch
}

func (a *doctorProbeTestAdapter) Close() error { return nil }

func TestSanitizeProviderError_RedactsSensitiveTokens(t *testing.T) {
	in := "request failed: Authorization: Bearer verySecretToken request-id=req_abc123 key=sk-ant-supersecret AIzaSyA1234567890123456789012345"
	got := sanitizeProviderError(in)

	for _, secret := range []string{
		"verySecretToken",
		"req_abc123",
		"sk-ant-supersecret",
		"AIzaSyA1234567890123456789012345",
	} {
		if strings.Contains(got, secret) {
			t.Fatalf("sanitized message still contains secret %q: %q", secret, got)
		}
	}
}

func TestProbeProvider_SanitizesNonAuthError(t *testing.T) {
	secret := "sk-1234567890SECRET"
	ok, msg := probeProvider(providerDef{
		name:         "OpenAI",
		defaultModel: "gpt-4.1-mini",
		buildAdapter: func(_ string) (llm.ProviderAdapter, error) {
			return &doctorProbeTestAdapter{
				completeErr: &llm.ProviderError{
					SDKError: llm.SDKError{Msg: "boom Bearer topsecret " + secret + " req_abc123"},
					Provider: "openai",
				},
			}, nil
		},
	}, "test-key")

	if ok {
		t.Fatal("expected auth probe to fail")
	}
	if strings.Contains(msg, "topsecret") || strings.Contains(msg, secret) || strings.Contains(msg, "req_abc123") {
		t.Fatalf("probe message not sanitized: %q", msg)
	}
}
