# SPEC: Review Envelope Service

A deliberately-broken spec fixture for the SpecLint coherence preflight
(issue #301). Each defect below maps to a SpecLint rule; see README.md
for the manual verification procedure. Do NOT fix these defects.

## Overview

Build a service that wraps code-review results in signed envelopes and
submits them to the upstream API. The milestone breakdown lives in
PLAN.md, and the field layouts are defined in the Shared Contracts
section below.

<!-- Rule (a) violations: PLAN.md does not exist, and there is no
     "Shared Contracts" section anywhere in this file. -->

## Retry policy

Submission uses max 2 retries with exponential backoff starting at
250ms.

If the upstream returns 429, the client backs off and tries again, up
to a maximum of 2 attempts total.

<!-- Rule (b) violation: "max 2 retries" (3 calls) vs "maximum of 2
     attempts total" (2 calls) — the same constant stated two ways. -->

## Idempotency

Every submission MUST carry an `Idempotency-Key` header derived from
`review_id + commit_sha`.

```go
// Declared interface
type Submitter interface {
    Review(ctx context.Context, model string, envelope Envelope) error
}
```

<!-- Rule (c) violation: the contract derives the key from review_id +
     commit_sha, but Review's signature receives neither. -->

## Envelope construction

```go
// Example usage — BuildEnvelope is declared in the API section below as:
//   func BuildEnvelope(payload []byte) (string, error)
key := BuildEnvelope(payload)
submit(key)
```

<!-- Rule (e) violation (warn): example calls BuildEnvelope as if it
     returned a single string; the declared signature is (string, error). -->

## API

```go
func BuildEnvelope(payload []byte) (string, error)
```

## Quality requirements

The service must be fast and robust under load.

<!-- Rule (d) violation (warn): no checkable threshold — can never
     become an assertion or a named test. -->

## Mandated tests

- A synthetic-429 test MUST verify the retry/backoff path.
- A cancellation test MUST verify the client aborts within 100ms of
  context cancellation.

<!-- Rule (f): these two tests must be enumerated in the "Mandated
     tests" section of .ai/decisions/spec-quality.md so the Decompose
     requirement-coverage table (issue #300) can own them. -->
