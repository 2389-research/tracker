// ABOUTME: Integration tests that call real LLM provider APIs.
// ABOUTME: Run with: go test ./llm/ -tags=integration -v

//go:build integration

package llm_test

import (
	"context"
	"os"
	"testing"

	"github.com/2389-research/tracker/llm"
	"github.com/2389-research/tracker/llm/anthropic"
	"github.com/2389-research/tracker/llm/google"
	"github.com/2389-research/tracker/llm/openai"
)

func setupClient(t *testing.T) *llm.Client {
	t.Helper()

	var opts []llm.ClientOption

	if key := os.Getenv("ANTHROPIC_API_KEY"); key != "" {
		opts = append(opts, llm.WithProvider(anthropic.New(key)))
	}
	if key := os.Getenv("OPENAI_API_KEY"); key != "" {
		opts = append(opts, llm.WithProvider(openai.New(key)))
	}
	if key := os.Getenv("GEMINI_API_KEY"); key != "" {
		opts = append(opts, llm.WithProvider(google.New(key)))
	}

	if len(opts) == 0 {
		t.Skip("no API keys set; set ANTHROPIC_API_KEY, OPENAI_API_KEY, or GEMINI_API_KEY")
	}

	client, err := llm.NewClient(opts...)
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func TestIntegrationAnthropicComplete(t *testing.T) {
	key := os.Getenv("ANTHROPIC_API_KEY")
	if key == "" {
		t.Skip("ANTHROPIC_API_KEY not set")
	}

	client := setupClient(t)
	resp, err := client.Complete(context.Background(), &llm.Request{
		Model:    "claude-haiku-4-5-20251001",
		Messages: []llm.Message{llm.UserMessage("Say 'hello' and nothing else.")},
	})
	if err != nil {
		t.Fatal(err)
	}

	t.Logf("Response:\n%s", resp)

	if resp.Text() == "" {
		t.Error("expected non-empty response text")
	}
	if resp.Usage.InputTokens == 0 {
		t.Error("expected non-zero input tokens")
	}
	if resp.FinishReason.Reason != "stop" {
		t.Errorf("expected finish reason 'stop', got %q", resp.FinishReason.Reason)
	}
}

func TestIntegrationOpenAIComplete(t *testing.T) {
	key := os.Getenv("OPENAI_API_KEY")
	if key == "" {
		t.Skip("OPENAI_API_KEY not set")
	}

	client := setupClient(t)
	resp, err := client.Complete(context.Background(), &llm.Request{
		Model:    "gpt-4.1-mini",
		Messages: []llm.Message{llm.UserMessage("Say 'hello' and nothing else.")},
	})
	if err != nil {
		t.Fatal(err)
	}

	t.Logf("Response:\n%s", resp)

	if resp.Text() == "" {
		t.Error("expected non-empty response text")
	}
	if resp.Usage.InputTokens == 0 {
		t.Error("expected non-zero input tokens")
	}
}

func TestIntegrationGeminiComplete(t *testing.T) {
	key := os.Getenv("GEMINI_API_KEY")
	if key == "" {
		t.Skip("GEMINI_API_KEY not set")
	}

	client := setupClient(t)
	resp, err := client.Complete(context.Background(), &llm.Request{
		Model:    "gemini-2.0-flash",
		Messages: []llm.Message{llm.UserMessage("Say 'hello' and nothing else.")},
	})
	if err != nil {
		t.Fatal(err)
	}

	t.Logf("Response:\n%s", resp)

	if resp.Text() == "" {
		t.Error("expected non-empty response text")
	}
}

func TestIntegrationAnthropicStream(t *testing.T) {
	key := os.Getenv("ANTHROPIC_API_KEY")
	if key == "" {
		t.Skip("ANTHROPIC_API_KEY not set")
	}

	client := setupClient(t)
	ch := client.Stream(context.Background(), &llm.Request{
		Model:    "claude-haiku-4-5-20251001",
		Messages: []llm.Message{llm.UserMessage("Say 'hello' and nothing else.")},
	})

	var text string
	var gotFinish bool
	for evt := range ch {
		if evt.Err != nil {
			t.Fatal(evt.Err)
		}
		text += evt.Delta
		if evt.Type == llm.EventFinish {
			gotFinish = true
		}
	}

	if text == "" {
		t.Error("expected non-empty streamed text")
	}
	if !gotFinish {
		t.Error("expected finish event")
	}
	t.Logf("Streamed text: %s", text)
}

func TestIntegrationAnthropicToolCall(t *testing.T) {
	key := os.Getenv("ANTHROPIC_API_KEY")
	if key == "" {
		t.Skip("ANTHROPIC_API_KEY not set")
	}

	client := setupClient(t)
	resp, err := client.Complete(context.Background(), &llm.Request{
		Model: "claude-haiku-4-5-20251001",
		Messages: []llm.Message{
			llm.UserMessage("What's the weather in San Francisco? Use the get_weather tool."),
		},
		Tools: []llm.ToolDefinition{
			{
				Name:        "get_weather",
				Description: "Get the current weather for a location",
				Parameters:  []byte(`{"type":"object","properties":{"location":{"type":"string","description":"City name"}},"required":["location"]}`),
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	t.Logf("Response:\n%s", resp)

	calls := resp.ToolCalls()
	if len(calls) == 0 {
		t.Error("expected at least one tool call")
	} else {
		t.Logf("Tool call: %s(%s)", calls[0].Name, string(calls[0].Arguments))
	}

	if resp.FinishReason.Reason != "tool_calls" {
		t.Errorf("expected finish reason 'tool_calls', got %q", resp.FinishReason.Reason)
	}
}

func TestIntegrationInvalidKey(t *testing.T) {
	adapter := anthropic.New("invalid-key-12345")
	client, _ := llm.NewClient(llm.WithProvider(adapter))

	_, err := client.Complete(context.Background(), &llm.Request{
		Model:    "claude-haiku-4-5-20251001",
		Messages: []llm.Message{llm.UserMessage("Hello")},
	})
	if err == nil {
		t.Fatal("expected error with invalid key")
	}

	t.Logf("Got expected error: %v", err)
}
