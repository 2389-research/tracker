// ABOUTME: Tests that core LLM types compile and construct correctly.
// ABOUTME: Validates Message, ContentPart, Request, Response, Usage types.
package llm

import (
	"testing"
)

func TestMessageConstruction(t *testing.T) {
	msg := SystemMessage("You are helpful.")
	if msg.Role != RoleSystem {
		t.Errorf("expected RoleSystem, got %v", msg.Role)
	}
	if msg.Text() != "You are helpful." {
		t.Errorf("expected text, got %q", msg.Text())
	}
}

func TestUserMessage(t *testing.T) {
	msg := UserMessage("Hello")
	if msg.Role != RoleUser {
		t.Errorf("expected RoleUser, got %v", msg.Role)
	}
	if msg.Text() != "Hello" {
		t.Errorf("expected Hello, got %q", msg.Text())
	}
}

func TestAssistantMessage(t *testing.T) {
	msg := AssistantMessage("Hi there")
	if msg.Role != RoleAssistant {
		t.Errorf("expected RoleAssistant, got %v", msg.Role)
	}
}

func TestToolResultMessage(t *testing.T) {
	msg := ToolResultMessage("call_123", "72F and sunny", false)
	if msg.Role != RoleTool {
		t.Errorf("expected RoleTool, got %v", msg.Role)
	}
	if msg.ToolCallID != "call_123" {
		t.Errorf("expected call_123, got %q", msg.ToolCallID)
	}
}

func TestUsageAddition(t *testing.T) {
	a := Usage{InputTokens: 100, OutputTokens: 50, TotalTokens: 150}
	b := Usage{InputTokens: 200, OutputTokens: 100, TotalTokens: 300}
	c := a.Add(b)
	if c.InputTokens != 300 {
		t.Errorf("expected 300 input tokens, got %d", c.InputTokens)
	}
	if c.TotalTokens != 450 {
		t.Errorf("expected 450 total tokens, got %d", c.TotalTokens)
	}
}
