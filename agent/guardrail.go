// ABOUTME: Pre-execution tool-call guardrail hook for the agent tool-dispatch loop.
// ABOUTME: A GuardrailPolicy inspects (tool, args, context) BEFORE execution; deny returns the reason to the model.
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/2389-research/tracker/llm"
)

// GuardrailRequest carries the tool call plus available session context to a
// GuardrailPolicy for a pre-execution decision. Risk is f(tool, args, context),
// not f(tool) — the args and role fields let a policy make an in-context ruling
// rather than a static presence check.
type GuardrailRequest struct {
	ToolName   string
	ToolInput  json.RawMessage
	NodeID     string
	Role       string
	IsSubagent bool
}

// GuardrailDecision is the result of a policy check. Allow gates execution;
// ReasonCodes are machine-readable denial reasons surfaced to the model as the
// tool result so it can adapt; PolicyID identifies the deciding policy.
type GuardrailDecision struct {
	Allow       bool
	ReasonCodes []string
	PolicyID    string
}

// GuardrailPolicy evaluates a tool call before it executes. A non-nil error is
// treated by the session as a FAIL-CLOSED denial (never allow-on-error).
type GuardrailPolicy interface {
	Check(ctx context.Context, req GuardrailRequest) (GuardrailDecision, error)
}

// GuardrailContext is the static per-session context merged into every
// GuardrailRequest. It is carried on SessionConfig next to the tool-safety
// seam so a caller (e.g. tracker-runner) can attribute decisions to a node,
// role, and subagent flag without threading them through each call site.
type GuardrailContext struct {
	NodeID     string
	Role       string
	IsSubagent bool
}

// builtinDenylistPatterns mirrors the CLAUDE.md tool-node safety denylist
// (eval, pipe-to-shell, curl|sh). Within ListGuardrailPolicy these are DEFENSE
// IN DEPTH: the denylist is enforced BEFORE consulting the allowlist so a
// permissive (nil) allowlist can never soften it. Scope note (finding 3): this
// agent-layer denylist is a property of the DEFAULT list policy — a caller that
// supplies a fully custom GuardrailPolicy replaces it and takes responsibility
// for its own deny decisions. That is not a security regression: the
// AUTHORITATIVE shell-command denylist is still enforced downstream by the
// pipeline tool-command handler regardless of the agent-layer policy.
var builtinDenylistPatterns = []*regexp.Regexp{
	regexp.MustCompile(`\beval\b`),                                 // eval of dynamic content
	regexp.MustCompile(`\|\s*(sh|bash|zsh)\b`),                     // pipe-to-shell
	regexp.MustCompile(`\b(curl|wget)\b[^|]*\|\s*(sh|bash|zsh)\b`), // curl|sh / wget|sh
}

// commandBearingTools maps command-executing tool names to the JSON field that
// carries the shell command. The built-in denylist scans ONLY that field for
// these tools. A blanket scan of every tool's raw input false-positive-denies
// coding agents: a write/edit whose file CONTENT contains "eval(" or a
// "curl | sh" string trips the patterns though nothing executes. Detection for
// genuine command execution (bash) is unchanged — the command field is scanned
// in full, including a raw-payload fallback when the input is not valid JSON.
var commandBearingTools = map[string]string{
	"bash": "command",
}

// ListGuardrailPolicy is a concrete GuardrailPolicy driven by a tool-name
// allowlist, with a non-overridable built-in denylist applied first.
//
// None-vs-empty semantics (deliberate, pinned by tests):
//   - Allowlist == nil        → allow-all (the ONLY allow-all; explicit named absence)
//   - Allowlist == []string{} → deny-all  (empty non-nil is a real, restrictive value)
//
// Go zero-value truthiness would collapse these two; the nil check is kept
// explicit so an empty non-nil slice denies rather than fails open.
type ListGuardrailPolicy struct {
	// Allowlist of permitted tool names. nil = allow-all; non-nil = only the
	// listed names pass (empty non-nil = deny-all).
	Allowlist []string
	// PolicyID labels decisions from this policy. Defaults to "list-guardrail".
	PolicyID string
}

// Check implements GuardrailPolicy. The built-in denylist runs first and is
// never softened by the allowlist; then the None-vs-empty allowlist rule.
func (p ListGuardrailPolicy) Check(_ context.Context, req GuardrailRequest) (GuardrailDecision, error) {
	id := p.PolicyID
	if id == "" {
		id = "list-guardrail"
	}
	if matchesBuiltinDenylist(req.ToolName, req.ToolInput) {
		return GuardrailDecision{Allow: false, ReasonCodes: []string{"builtin_denylist"}, PolicyID: id}, nil
	}
	// nil allowlist is the only allow-all. An empty non-nil slice matches
	// nothing below and therefore denies.
	if p.Allowlist == nil {
		return GuardrailDecision{Allow: true, PolicyID: id}, nil
	}
	for _, name := range p.Allowlist {
		if name == req.ToolName {
			return GuardrailDecision{Allow: true, PolicyID: id}, nil
		}
	}
	return GuardrailDecision{Allow: false, ReasonCodes: []string{"not_in_allowlist"}, PolicyID: id}, nil
}

// matchesBuiltinDenylist reports whether a command-executing tool's command
// carries a pattern from the built-in denylist. Non-command tools (write, edit,
// read, …) are never scanned, so file-content payloads cannot false-positive.
func matchesBuiltinDenylist(toolName string, input json.RawMessage) bool {
	field, isCommandTool := commandBearingTools[toolName]
	if !isCommandTool {
		return false
	}
	cmd := commandField(input, field)
	for _, re := range builtinDenylistPatterns {
		if re.MatchString(cmd) {
			return true
		}
	}
	return false
}

// commandField extracts the named command field from a command tool's JSON
// input. If the input is not valid JSON, the whole raw payload is returned so
// detection fails safe (toward scanning) for a command tool with a malformed
// argument object.
func commandField(input json.RawMessage, field string) string {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(input, &obj); err != nil {
		return string(input)
	}
	raw, ok := obj[field]
	if !ok {
		return ""
	}
	var cmd string
	if err := json.Unmarshal(raw, &cmd); err != nil {
		return string(raw)
	}
	return cmd
}

// safeGuardrailCheck invokes a policy's Check, converting a panic into an error
// so a panicking (or malformed) policy denies exactly one call — the SAME
// fail-closed path as an errored policy — instead of unwinding the session and
// crashing a host that embeds Session.Run directly. The recovered value is
// surfaced as the error cause so the block is diagnosable.
func safeGuardrailCheck(ctx context.Context, policy GuardrailPolicy, req GuardrailRequest) (dec GuardrailDecision, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("guardrail policy panicked: %v", r)
		}
	}()
	return policy.Check(ctx, req)
}

// checkGuardrail consults the configured GuardrailPolicy before a tool executes.
// It returns (denialResult, true) when the call must NOT run — the caller returns
// that result to the model as the tool result (IsError=true so the model treats
// it as a failed call and adapts) instead of executing the tool. It returns
// (_, false) when execution may proceed (no policy configured, or the policy
// allowed the call). A policy error is FAIL-CLOSED: the call is denied.
func (s *Session) checkGuardrail(ctx context.Context, call llm.ToolCallData) (llm.ToolResultData, bool) {
	if s.config.Guardrail == nil {
		return llm.ToolResultData{}, false
	}
	req := GuardrailRequest{
		ToolName:   call.Name,
		ToolInput:  call.Arguments,
		NodeID:     s.config.GuardrailContext.NodeID,
		Role:       s.config.GuardrailContext.Role,
		IsSubagent: s.config.GuardrailContext.IsSubagent,
	}
	decision, err := safeGuardrailCheck(ctx, s.config.Guardrail, req)
	if err != nil {
		return s.guardrailDenial(call, GuardrailDecision{PolicyID: decision.PolicyID, ReasonCodes: []string{"guardrail_error"}}, err), true
	}
	if decision.Allow {
		return llm.ToolResultData{}, false
	}
	return s.guardrailDenial(call, decision, nil), true
}

// guardrailDenial builds the model-visible tool result for a denied call and
// emits an error event so the audit trail records the block. The tool's Execute
// is never reached, so its side effect cannot happen.
func (s *Session) guardrailDenial(call llm.ToolCallData, decision GuardrailDecision, cause error) llm.ToolResultData {
	reasons := strings.Join(decision.ReasonCodes, ", ")
	if reasons == "" {
		reasons = "denied"
	}
	msg := fmt.Sprintf("Tool call to %q denied by guardrail policy %q (reasons: %s). The tool was NOT executed. Adjust your approach and try a different, permitted action.",
		call.Name, decision.PolicyID, reasons)
	if cause != nil {
		msg += fmt.Sprintf(" Policy evaluation error: %s", cause.Error())
	}
	s.emit(Event{
		Type:      EventError,
		SessionID: s.id,
		ToolName:  call.Name,
		Err:       fmt.Errorf("guardrail denied tool %q (policy %q, reasons: %s)", call.Name, decision.PolicyID, reasons),
	})
	return llm.ToolResultData{
		ToolCallID: call.ID,
		Name:       call.Name,
		Content:    msg,
		IsError:    true,
	}
}
