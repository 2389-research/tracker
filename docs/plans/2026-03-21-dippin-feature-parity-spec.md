# Task Specification: Dippin Language Feature Parity

**Date:** 2026-03-21  
**Author:** Analysis Agent  
**Status:** Ready for Implementation

---

## Objective

Close the 8% feature gap between Tracker and the Dippin language specification by:

1. Wiring the `reasoning_effort` field from `.dip` files to runtime LLM requests
2. Implementing 12 missing Dippin semantic lint rules (DIP101-DIP112)
3. Integrating lint warnings into the CLI validation flow

---

## Scope

### In Scope

**Runtime Features:**
- Wire `reasoning_effort` node attribute to `SessionConfig.ReasoningEffort`

**Validation Features:**
- Implement 12 Dippin lint rules:
  - DIP101: Node only reachable via conditional edges
  - DIP102: Routing node missing default edge  
  - DIP103: Overlapping conditions
  - DIP104: Unbounded retry loop
  - DIP105: No success path to exit
  - DIP106: Undefined variable in prompt
  - DIP107: Unused context write
  - DIP108: Unknown model/provider
  - DIP109: Namespace collision in imports
  - DIP110: Empty prompt on agent
  - DIP111: Tool without timeout
  - DIP112: Reads key not produced upstream

**CLI Features:**
- Add `tracker validate [file]` command
- Display lint warnings without blocking execution
- Return exit code 0 for warnings, 1 for errors

### Out of Scope

- DOT export (handled by dippin-lang CLI)
- Migration tool (handled by dippin-lang CLI)
- Formatter (handled by dippin-lang CLI)
- Editor integration / LSP
- Custom lint rules / plugins
- Autofix suggestions
- TUI integration (optional, can be follow-up)

---

## Expected Artifacts

1. **Code Files:**
   - `pipeline/handlers/codergen.go` (modified) — Wire reasoning_effort
   - `pipeline/lint_dippin.go` (new) — All 12 lint rules
   - `pipeline/lint_dippin_test.go` (new) — Comprehensive tests
   - `pipeline/validate_semantic.go` (modified) — Return warnings
   - `cmd/tracker/validate.go` (new or modified) — CLI command
   - `cmd/tracker/main.go` (modified) — Register validate subcommand

2. **Test Files:**
   - Unit tests for each lint rule (≥3 test cases per rule)
   - Integration test for reasoning_effort wiring
   - `examples/reasoning_effort_test.dip` (demo file)

3. **Documentation:**
   - Update README with `tracker validate` usage
   - Document which providers support reasoning_effort
   - Add lint rules reference (or link to Dippin spec)

---

## Success Criteria

### Functional Requirements

**FR1:** `reasoning_effort` specified in `.dip` files reaches LLM provider
- Test: Create `.dip` with `reasoning_effort: high`, verify OpenAI request includes parameter
- Acceptance: Integration test passes with real API call

**FR2:** All 12 Dippin lint rules detect their respective issues
- Test: Each rule has test cases for positive (warning) and negative (no warning)
- Acceptance: `go test ./pipeline/... -v` passes 100%

**FR3:** `tracker validate` command displays warnings without blocking
- Test: Run validate on file with lint warnings, verify exit code 0
- Acceptance: Warnings printed to stderr, success message to stdout

**FR4:** No regressions in existing functionality
- Test: Run `go test ./...` and existing examples
- Acceptance: All existing tests pass, examples execute successfully

### Non-Functional Requirements

**NFR1:** Test coverage ≥90% for new code
- Measure: `go test -cover ./pipeline/`

**NFR2:** Lint warnings use Dippin spec format
- Format: `warning[DIPXXX]: message`
- Example: `warning[DIP110]: empty prompt on agent node "TaskA"`

**NFR3:** Implementation follows TDD (test-first) pattern
- Each task writes failing test before implementation

---

## Constraints

### Technical Constraints

1. **Go version:** Must work with Go 1.25+
2. **No breaking changes:** Existing `.dot` and `.dip` files must still work
3. **Backward compatibility:** Warnings are non-blocking, errors are blocking
4. **Library dependency:** Use dippin-lang v0.1.0 (already imported)

### Time Constraints

- **Estimated total:** 13-15 hours
- **Breakdown:**
  - Reasoning effort wiring: 1 hour
  - DIP110, DIP111 (simple rules): 1 hour
  - DIP102, DIP104, DIP108 (medium rules): 2 hours
  - DIP101, DIP107, DIP112 (medium-complex rules): 3 hours
  - DIP105, DIP106, DIP103, DIP109 (complex rules): 5.5 hours
  - CLI integration: 1 hour
  - Testing & polish: 1.5 hours

### Resource Constraints

- Must not impact pipeline execution performance (lint is opt-in)
- Warnings should not be excessively noisy (minimize false positives)

---

## Implementation Strategy

### Approach

**Sequential, Incremental, Test-First**

1. Start with quick win (reasoning_effort) to prove value
2. Implement lint rules one at a time, simplest first
3. Each rule follows TDD: test → implement → verify → commit
4. Integrate CLI after 3-4 rules are complete
5. Continue adding remaining rules
6. Final polish and documentation

### Priority Order

**High Priority (Week 1):**
1. Task 1: Reasoning effort wiring (1 hour) — User-visible feature
2. Task 2: DIP110 (empty prompt) (30 min) — Catches common error
3. Task 3: DIP111 (tool timeout) (30 min) — Prevents hanging
4. Task 4: DIP102 (no default edge) (45 min) — Prevents stuck execution
5. Task 5: CLI integration (1 hour) — Makes warnings visible

**Medium Priority (Week 2):**
6. DIP104 (unbounded retry)
7. DIP108 (unknown model/provider)
8. DIP101 (unreachable via conditional)
9. DIP107 (unused write)
10. DIP112 (reads not produced)

**Lower Priority (Week 3):**
11. DIP105 (no success path)
12. DIP106 (undefined var in prompt)
13. DIP103 (overlapping conditions)
14. DIP109 (namespace collision)

### Testing Strategy

**Unit Tests:**
- Each lint rule: 3-5 test cases (positive, negative, edge cases)
- Reasoning effort: Mock client captures config
- Use table-driven tests where appropriate

**Integration Tests:**
- End-to-end: `.dip` file → parse → validate → lint → execute
- Real LLM API call for reasoning_effort (OpenAI)
- Verify examples/ directory pipelines still work

**Regression Tests:**
- All existing `go test ./...` must pass
- Run examples/ pipelines to ensure no breakage

---

## Risk Mitigation

### Risk 1: Lint rules too noisy (false positives)

**Mitigation:**
- Start conservative (warn only when certain)
- Test against real-world examples from examples/ directory
- Document expected warnings vs. false positives

### Risk 2: Provider compatibility for reasoning_effort

**Mitigation:**
- Document which providers support it (OpenAI ✅, Anthropic ❌)
- Graceful degradation: ignore if provider doesn't support
- Test with multiple providers

### Risk 3: Performance impact of linting

**Mitigation:**
- Make lint opt-in (requires flag or explicit call)
- Cache lint results during pipeline execution
- Measure performance: lint should add <100ms overhead

### Risk 4: Breaking changes to existing workflows

**Mitigation:**
- Warnings are non-blocking (exit code 0)
- Existing behavior unchanged (no validation by default)
- Explicit opt-in via `tracker validate` or `--lint` flag

---

## Acceptance Criteria

### Definition of Done

A task is complete when:
- [ ] All unit tests pass (`go test ./...`)
- [ ] Integration tests pass (if applicable)
- [ ] Code follows existing patterns and style
- [ ] No regressions in existing functionality
- [ ] Committed with descriptive message
- [ ] Documentation updated (if user-facing)

### Final Deliverable

The implementation is complete when:
- [ ] All 13 tasks implemented and tested
- [ ] `tracker validate` command works end-to-end
- [ ] Lint warnings display correctly
- [ ] All success criteria met (FR1-FR4, NFR1-NFR3)
- [ ] Documentation updated
- [ ] Examples directory includes `.dip` files demonstrating features
- [ ] No open bugs or failing tests

---

## Dependencies

### External Dependencies

1. **dippin-lang library** v0.1.0
   - Already imported in go.mod
   - No upgrade needed
   - Provides IR types and parser

2. **LLM Provider APIs**
   - OpenAI: reasoning_effort support ✅
   - Anthropic: graceful ignore ⚠️
   - Gemini: needs investigation ❓

### Internal Dependencies

1. **Existing Tracker code:**
   - `pipeline/validate.go` — Structural validation (DIP001-DIP009)
   - `pipeline/validate_semantic.go` — Semantic validation framework
   - `pipeline/handlers/codergen.go` — Agent session builder
   - `agent/config.go` — SessionConfig type

2. **Testing infrastructure:**
   - Existing test patterns in `*_test.go` files
   - Mock LLM client for testing
   - `examples/` directory for integration tests

---

## Verification Plan

### Manual Testing

After implementation, manually verify:

1. **Reasoning effort:**
   ```bash
   cat > test.dip <<EOF
   workflow Test
     start: A
     exit: A
     agent A
       reasoning_effort: high
       model: gpt-5.4
       provider: openai
       prompt: Think deeply.
   EOF
   
   OPENAI_API_KEY=sk-... tracker test.dip --verbose
   # Verify logs show reasoning_effort parameter
   ```

2. **Lint warnings:**
   ```bash
   cat > test_lint.dip <<EOF
   workflow Test
     start: A
     exit: B
     agent A
       # Empty prompt
     agent B
       prompt: done
     edges
       A -> B
   EOF
   
   tracker validate test_lint.dip
   # Expected: warning[DIP110]: empty prompt on agent node "A"
   ```

3. **No regressions:**
   ```bash
   tracker examples/ask_and_execute.dip --no-tui
   # Should execute successfully
   ```

### Automated Testing

CI/CD pipeline should run:

```bash
go test ./... -v -race -cover
go vet ./...
golangci-lint run
```

---

## Documentation Requirements

### Code Documentation

- All new functions have ABOUTME comments
- Lint rules documented with examples
- Edge cases documented in comments

### User Documentation

**README.md updates:**
- Add `tracker validate` to CLI reference
- Document lint warnings and how to fix them
- Link to Dippin spec for full rule reference

**Example files:**
- `examples/reasoning_effort_test.dip` — Demonstrate reasoning_effort
- Update existing examples with lint-clean versions

**Inline help:**
```bash
tracker validate --help
# Should show usage and lint rule summary
```

---

## Future Enhancements (Out of Scope)

After achieving full parity, consider:

1. **Autofix** — `tracker validate --fix` applies safe corrections
2. **LSP** — Real-time lint warnings in editors
3. **Custom rules** — User-defined lint rules
4. **Suppressions** — `# dippin-lint-ignore DIP110` comments
5. **CI/CD integration** — GitHub Action for validation
6. **TUI integration** — Display warnings in dashboard

These are explicitly **not** required for this implementation but are natural follow-ups.

---

## Summary

**What we're building:**
- Wire 1 missing runtime feature (reasoning_effort)
- Implement 12 missing lint rules (DIP101-DIP112)
- Add CLI validation command with warning display

**Why it matters:**
- Achieves 100% Dippin language feature parity
- Catches workflow design issues before execution
- Makes Tracker the reference Dippin executor

**How long it takes:**
- Estimated: 13-15 hours
- Approach: Incremental, test-first, one task at a time

**How we know it's done:**
- All tests pass
- CLI works end-to-end
- Documentation complete
- No regressions

---

## Next Steps for Implementation Agent

1. **Start with Task 1** (reasoning_effort) — Quick win, proves value
2. **Follow the implementation plan** in `2026-03-21-dippin-feature-parity-plan.md`
3. **Use TDD pattern** for all lint rules (test first, then implement)
4. **Commit incrementally** (one rule per commit)
5. **Test continuously** (run `go test` after each change)
6. **Ask for clarification** if any requirement is ambiguous

The detailed implementation plan has step-by-step instructions for each task, including exact code snippets and test cases. Follow it sequentially or cherry-pick high-priority tasks first.
