This is the final compliance check. Read:
- SPEC.md (every requirement)
- docs/traceability.yaml (every entry)
- .ai/gates/final.txt (gate results, including the WAIVED test_ref lines)
- docs/traceability-waivers.txt, if present (test_ref waivers: `<ID>  <reason>`)

For EVERY requirement in the spec (FR-N, QG-N, NFR-N, EC-N):
1. Is it in the traceability file?
2. Does it have an impl_ref pointing to real code?
3. Does it have a test_ref pointing to a real test?
4. Does the acceptance note match the spec's acceptance criteria?
5. If its test_ref is waived: is the waiver reason genuinely valid (a
   requirement that cannot be unit-tested), or a dodge? A dodge is a FAIL.

Also verify Section 18 (Release Readiness Checklist) — every item.
Also verify Section 17 (Definition of Done) applies to every feature.

Write .ai/decisions/final-compliance.md with a per-requirement verdict.

If everything traces cleanly: STATUS:success
If any requirement is unlinked or unimplemented: STATUS:fail with the list.