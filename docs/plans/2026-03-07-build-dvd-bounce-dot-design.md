# Build DVD Bounce DOT Design

**Goal:** Make `build_dvd_bounce.dot` run correctly under the current tracker engine.

**Root Cause:** The file defines several nodes without `shape`, which makes validation fail, and it uses a `diamond` node with a prompt as if it were an LLM validation step, but `diamond` maps to the conditional handler in this engine and does not execute prompts.

**Design:** Convert all executable stages (`design`, `implement`, `validate`, `review`) into `shape=box` codergen nodes. Remove the prompt-bearing conditional gate and route directly from `validate` and `review` using `auto_status=true` plus `STATUS:success|fail` responses, which the engine already understands.
