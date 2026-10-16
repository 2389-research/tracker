// ABOUTME: Opt-in live smoke test against the real ChatGPT Codex backend, using the operator's own
// ABOUTME: ChatGPT-subscription sign-in; skipped by default, never run as part of the normal suite.
package openai

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/2389-research/tracker/internal/chatgptauth"
	"github.com/2389-research/tracker/llm"
)

// chatgptLiveTimeout bounds one live call to the ChatGPT backend — generous,
// since a subscription-mode reply reasons before it streams.
const chatgptLiveTimeout = 2 * time.Minute

// chatgptLivePrompt is sent to the real backend in every call; asking the
// model to echo it verbatim makes a reply easy to recognize.
const chatgptLivePrompt = "Reply with exactly: pong"

// TestChatGPTSubscriptionLive sends real requests to the ChatGPT Codex
// backend (chatgpt.com/backend-api/codex) using the operator's own
// ChatGPT-subscription sign-in: the token file `tracker setup` writes when
// you choose OpenAI → ChatGPT subscription. It skips before touching the
// network unless both TRACKER_E2E_CHATGPT=1 and TRACKER_E2E_CHATGPT_MODEL
// are set, so it never runs as part of the normal test suite or CI.
//
// Three calls run so one pass settles what the design's open items need:
// (a) Complete with a system message and a user message, (b) Stream of the
// same, and (c) Complete with only a user message. Comparing (c) against
// (a) and (b) shows whether the backend requires an `instructions` field.
//
// A fourth, separately-gated subtest sends a model the signed-in plan does
// not serve, when TRACKER_E2E_CHATGPT_UNSERVED_MODEL also names one — it
// records the backend's refusal (design doc open item 3) without requiring
// every other run to know an unserved model name.
func TestChatGPTSubscriptionLive(t *testing.T) {
	enabled := os.Getenv("TRACKER_E2E_CHATGPT") == "1"
	model := os.Getenv("TRACKER_E2E_CHATGPT_MODEL")
	if !enabled || model == "" {
		t.Skip("set TRACKER_E2E_CHATGPT=1 and TRACKER_E2E_CHATGPT_MODEL=<model> to run; this test sends real requests to the ChatGPT Codex backend using your real ChatGPT sign-in (tracker setup)")
	}

	path, err := chatgptauth.DefaultPath()
	if err != nil {
		t.Fatalf("resolve the ChatGPT token file path: %v", err)
	}
	adapter := New("", WithChatGPTAuth(chatgptauth.NewTokenSource(path, chatgptauth.NewAuthClient()), chatgptauth.UserAgent()))

	t.Run("Complete with system and user message", func(t *testing.T) {
		resp, err := adapter.Complete(liveCtx(t), &llm.Request{
			Model: model,
			Messages: []llm.Message{
				llm.SystemMessage("You are a terse test assistant."),
				llm.UserMessage(chatgptLivePrompt),
			},
		})
		if err != nil {
			t.Errorf("Complete: %v", err)
			return
		}
		assertPongReply(t, resp)
	})

	t.Run("Stream with system and user message", func(t *testing.T) {
		events, err := collect(adapter.Stream(liveCtx(t), &llm.Request{
			Model: model,
			Messages: []llm.Message{
				llm.SystemMessage("You are a terse test assistant."),
				llm.UserMessage(chatgptLivePrompt),
			},
		}))
		if err != nil {
			t.Errorf("Stream: %v", err)
			return
		}
		acc := llm.NewStreamAccumulator()
		for _, evt := range events {
			acc.Process(evt)
		}
		resp := acc.Response()
		assertPongReply(t, &resp)
	})

	t.Run("Complete with user message only, no instructions", func(t *testing.T) {
		resp, err := adapter.Complete(liveCtx(t), &llm.Request{
			Model:    model,
			Messages: []llm.Message{llm.UserMessage(chatgptLivePrompt)},
		})
		if err != nil {
			t.Errorf("Complete (no system message): %v", err)
			return
		}
		assertPongReply(t, resp)
	})

	t.Run("Complete with a model the plan does not serve", func(t *testing.T) {
		unservedModel := os.Getenv("TRACKER_E2E_CHATGPT_UNSERVED_MODEL")
		if unservedModel == "" {
			t.Skip("set TRACKER_E2E_CHATGPT_UNSERVED_MODEL=<model> to run; it sends one real Complete call with a model your ChatGPT plan does not serve, to record the backend's refusal")
		}
		_, err := adapter.Complete(liveCtx(t), &llm.Request{
			Model:    unservedModel,
			Messages: []llm.Message{llm.UserMessage(chatgptLivePrompt)},
		})
		if err == nil {
			t.Fatal("expected Complete to fail for a model the plan does not serve, got nil error")
		}
		var provErr llm.ProviderErrorInterface
		if !errors.As(err, &provErr) {
			t.Fatalf("err = %T, want llm.ProviderErrorInterface: %v", err, err)
		}
		if provErr.Retryable() {
			t.Errorf("Retryable() = true, want false: %v", err)
		}
		t.Logf("status=%d message=%q", provErr.GetStatusCode(), provErr.Error())
	})
}

// liveCtx bounds one live subtest call against the real backend.
func liveCtx(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), chatgptLiveTimeout)
	t.Cleanup(cancel)
	return ctx
}

// assertPongReply fails the test when resp's text does not contain "pong"
// (case-insensitive) or it reports no input tokens.
func assertPongReply(t *testing.T, resp *llm.Response) {
	t.Helper()
	if text := resp.Text(); !strings.Contains(strings.ToLower(text), "pong") {
		t.Errorf("reply does not contain %q: %q", "pong", text)
	}
	if resp.Usage.InputTokens <= 0 {
		t.Errorf("expected positive input tokens, got %d", resp.Usage.InputTokens)
	}
}
