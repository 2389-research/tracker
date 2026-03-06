# StrongDM Parity Matrix

This matrix tracks strict parity against StrongDM's published specs as of March 6, 2026.

| Layer | Spec section | Requirement | Local file(s) | Test file | Status |
|-------|--------------|-------------|---------------|-----------|--------|
| pipeline | Attractor 2.6 | Node `type` overrides shape-based handler resolution | `pipeline/graph.go`, `pipeline/parser.go` | `pipeline/parity_attractor_test.go` | PASS |
| pipeline | Attractor Appendix B | `house` maps to `stack.manager_loop` | `pipeline/graph.go` | `pipeline/parity_attractor_test.go` | PASS |
| pipeline | Attractor 7 | Exit node must have no outgoing edges | `pipeline/validate.go` | `pipeline/parity_attractor_test.go` | PASS |
| pipeline | Attractor 10 | Conditions resolve `context.*` variables | `pipeline/condition.go`, `pipeline/validate_semantic.go` | `pipeline/parity_attractor_test.go` | PASS |
| pipeline | Attractor 3.4 | Goal gates are enforced at exit time, not on first failure | `pipeline/engine.go` | `pipeline/parity_attractor_test.go`, `pipeline/engine_test.go` | PASS |
| pipeline | Attractor 3.4 | Unsatisfied goal gates reroute via node or graph retry targets before failing | `pipeline/engine.go`, `pipeline/checkpoint.go` | `pipeline/parity_attractor_test.go`, `pipeline/engine_test.go` | PASS |
| pipeline | Attractor 8 | Stylesheet shape selectors work | `pipeline/stylesheet.go` | `pipeline/parity_attractor_test.go`, `pipeline/stylesheet_test.go` | PASS |
| pipeline | Attractor 8 | Node `class` is comma-separated for stylesheet targeting | `pipeline/stylesheet.go` | `pipeline/parity_attractor_test.go`, `pipeline/stylesheet_test.go` | PASS |
| pipeline | Attractor 9 | Graph attributes are mirrored into runtime context and `$goal` expands in prompts | `pipeline/engine.go`, `pipeline/transforms.go`, `pipeline/handlers/codergen.go` | `pipeline/engine_test.go`, `pipeline/handlers/codergen_test.go` | TODO |
| pipeline | Attractor Appendix C | Stage artifacts write `prompt.md`, `response.md`, and `status.json` | `pipeline/engine.go`, `pipeline/handlers/codergen.go`, `pipeline/handlers/tool.go` | `pipeline/engine_test.go`, `pipeline/handlers/codergen_test.go`, `pipeline/handlers/tool_test.go` | TODO |
| pipeline | Attractor 6 | Interviewer variants include callback and queued answers | `pipeline/handlers/human.go` | `pipeline/handlers/interviewer_test.go` | TODO |
| pipeline | Attractor Appendix B | Built-in handler surface includes `stack.manager_loop` | `pipeline/handlers/registry.go`, `pipeline/handlers/manager_loop.go` | `pipeline/handlers/integration_test.go` | TODO |
| agent | Coding Agent 2 | Session loops until natural completion, turn limit, or abort | `agent/session.go` | `agent/parity_coding_agent_test.go` | TODO |
| agent | Coding Agent 3 | Toolsets are provider-aligned rather than universal | `agent/session.go`, `agent/tools/registry.go`, provider profile wiring | `agent/parity_coding_agent_test.go` | TODO |
| agent | Coding Agent 3.8 | Unknown tools return error results, not session failures | `agent/tools/registry.go`, `agent/session.go` | `agent/parity_coding_agent_test.go` | TODO |
| agent | Coding Agent 3.8 | Tool execution errors become tool error results | `agent/tools/registry.go`, `agent/session.go` | `agent/parity_coding_agent_test.go` | TODO |
| agent | Coding Agent 3.8 | Steering is injected between tool rounds | `agent/session.go` | `agent/parity_coding_agent_test.go` | TODO |
| agent | Coding Agent 5 | Tool output truncation matches the spec contract | `agent/tools/registry.go`, truncation helpers | `agent/parity_coding_agent_test.go`, `agent/tools/*_test.go` | TODO |
| agent | Coding Agent 4 | Codergen uses the real agent loop with an execution environment and tools | `pipeline/handlers/codergen.go`, `pipeline/handlers/registry.go`, `agent/session.go` | `pipeline/handlers/codergen_test.go`, `agent/parity_coding_agent_test.go` | TODO |
| llm | Unified LLM 2 | Client resolves default providers deterministically | `llm/client.go` | `llm/parity_unified_llm_test.go` | TODO |
| llm | Unified LLM 3 | `Complete` populates provider and latency on responses | `llm/client.go` | `llm/parity_unified_llm_test.go` | TODO |
| llm | Unified LLM 3 | `Stream` emits a resolution error event when provider lookup fails | `llm/client.go`, `llm/stream.go` | `llm/parity_unified_llm_test.go` | TODO |
| llm | Unified LLM 4 | Tool call translation round-trips across provider adapters | `llm/anthropic/translate.go`, `llm/openai/translate.go`, `llm/google/translate.go` | `llm/parity_unified_llm_test.go`, provider adapter tests | TODO |
| llm | Unified LLM 4 | Finish reasons normalize to the shared contract | `llm/types.go`, provider translators | `llm/parity_unified_llm_test.go`, provider adapter tests | TODO |
| llm | Unified LLM 4 | Usage fields include totals and cache-token data where available | `llm/types.go`, provider adapters | `llm/parity_unified_llm_test.go`, provider adapter tests | TODO |
| llm | Unified LLM 6 | Retryable vs terminal provider errors map to the shared error types | `llm/errors.go`, `llm/retry.go` | `llm/parity_unified_llm_test.go`, `llm/errors_test.go`, `llm/retry_test.go` | TODO |

## Verification

- Initial matrix created before implementation changes.
- Initial verified gaps are encoded first in `pipeline/parity_attractor_test.go`.
- Update each row from `TODO` to `PASS` only after the corresponding test is green.
