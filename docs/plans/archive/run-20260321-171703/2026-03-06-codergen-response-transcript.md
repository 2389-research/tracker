# Codergen Response Transcript Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Preserve a readable session transcript in `response.md` even when a codergen run produces only tool activity and no final text.

**Architecture:** Extend the codergen event collector from a text-only buffer into an ordered transcript builder over agent events. Keep `status.json` and `last_response` semantics stable by deriving the plain response text from text-delta events while writing the richer transcript to artifacts.

**Tech Stack:** Go, `agent.Event` session hooks, codergen handler tests

---

### Task 1: Add a failing tool-only transcript test

**Files:**
- Modify: `pipeline/handlers/codergen_test.go`

**Step 1: Write the failing test**

Add a handler test that:
- creates a temp workdir
- configures a codergen handler with a local exec environment
- uses a scripted completer that first returns a `write` tool call and then stops without text
- asserts `response.md` contains the tool call and tool result instead of being empty

**Step 2: Run test to verify it fails**

Run: `go test ./pipeline/handlers -run TestCodergenHandlerWritesTranscriptForToolOnlyRun`

Expected: FAIL because `response.md` is empty or missing the tool activity

### Task 2: Implement transcript capture

**Files:**
- Modify: `pipeline/handlers/codergen.go`

**Step 1: Write minimal implementation**

Replace the text-only collector with a transcript collector that:
- records `turn_start`
- records `tool_call_start`
- records `tool_call_end`
- records `text_delta`
- records `error`
- preserves event order
- still exposes plain concatenated text for `last_response` and `auto_status`

**Step 2: Run targeted tests**

Run: `go test ./pipeline/handlers -run TestCodergenHandler`

Expected: PASS

### Task 3: Verify broader impact

**Files:**
- No additional files

**Step 1: Run focused package tests**

Run: `go test ./pipeline/... ./agent/...`

Expected: PASS
