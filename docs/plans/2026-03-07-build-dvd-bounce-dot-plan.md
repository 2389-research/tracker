# Build DVD Bounce DOT Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Repair `build_dvd_bounce.dot` so it parses and runs correctly with the current tracker engine.

**Architecture:** Keep the existing design/implement/validate/review workflow, but express every executable step as a `box` node and make validation/review set `outcome` through `auto_status`. Route retries using edge conditions on that outcome instead of a prompt-bearing diamond node.

**Tech Stack:** DOT pipeline syntax, tracker CLI, codergen handler auto-status parsing

---

### Task 1: Reproduce the current failure

**Files:**
- Modify: `/Users/harper/workspace/2389/tracker-test/build_dvd_bounce.dot`

**Step 1: Run the existing pipeline**

Run: `cd /Users/harper/workspace/2389/tracker-test && /Users/harper/Public/src/2389/mammoth-lite/bin/tracker -w /Users/harper/workspace/2389/tracker-test build_dvd_bounce.dot`

Expected: FAIL during validation because several nodes have no supported shape.

### Task 2: Rewrite unsupported nodes and routing

**Files:**
- Modify: `/Users/harper/workspace/2389/tracker-test/build_dvd_bounce.dot`

**Step 1: Replace unsupported executable nodes**

- Add `shape=box` to `design`, `implement`, `validate`, and `review`
- Remove the prompt-bearing `diamond` gate node
- Add `auto_status=true` to `validate` and `review`

**Step 2: Make prompts match engine behavior**

- `design` writes a design file via tools
- `implement` creates `index.html` via tools
- `validate` returns `STATUS:success` or `STATUS:fail`
- `review` returns `STATUS:success` or `STATUS:fail`

### Task 3: Verify the repaired pipeline

**Files:**
- Modify: `/Users/harper/workspace/2389/tracker-test/build_dvd_bounce.dot`

**Step 1: Run the repaired pipeline**

Run: `cd /Users/harper/workspace/2389/tracker-test && /Users/harper/Public/src/2389/mammoth-lite/bin/tracker -w /Users/harper/workspace/2389/tracker-test build_dvd_bounce.dot`

Expected: PASS through to `Done`, producing `index.html` and normal `.tracker/runs/<run-id>/` artifacts.
