# Compatible streaming idle deadline review

Scope: the upstream `llm/openaicompat` streaming client change for w828, including the new idle option and stream extraction. This review does not authorize a downstream dependency update or deployment. The reviewer added only `adapter_stream_review_test.go` and this record.

## Attack attempted

- Keep a real HTTP response active beyond the supplied client's total timeout, including progress bytes that do not yet form a complete SSE line. The stream must complete only after `[DONE]`; the original client's timeout must still terminate nonstreaming requests.
- Stall before response headers and after headers; distinguish the adapter's retryable `ErrStreamIdle` from caller cancellation. Keep sending bytes past a caller deadline, and disable the idle guard with zero and negative durations, without losing caller cancellation.
- Send `[DONE]` without closing the HTTP connection, omit `[DONE]`, and exceed the existing 1 MiB SSE line limit. Completion must close the response body, missing completion must fail, and oversized lines must remain bounded.
- Attempt to change redirect policy, HTTP 429/Retry-After classification, or an SSE authentication refusal through the streaming client copy. The traced client with retry middleware must make exactly one request for an authentication refusal.
- Restore an absolute streaming timeout through a Go source overlay without changing the working tree, to check that the active-stream regression test detects the original failure mechanism.

## Deterministic queries

Read `CLAUDE.md`, the adapter diff, `adapter_stream.go`, `adapter_idle_test.go`, the shared `llm.StreamIdleGuard`, and the traced completion/retry paths. The shared guard and status/error mapping are unchanged. The streaming client is a shallow copy with only its total timeout cleared; transport, redirect policy, and caller context remain in effect. The guard starts before the HTTP request and resets on raw response bytes.

Independent validation, using Go 1.25.13:

```sh
go test -race ./llm/openaicompat -count=1 -timeout=90s
git diff --check
```

The package race run passed in 2.356 seconds; output is in `/tmp/dip-compatible-stream-review.log`. It includes the author's HTTP progress, silence, partial-line, nonstreaming deadline, and missing-DONE tests, plus all seven independent adversarial tests above. The final rerun after checking the test client's cleanup error also passed in 2.342 seconds, and `git diff --check` passed.

Regression kill: an isolated copy of `adapter_stream.go` changed only `client.Timeout = 0` to `client.Timeout = 50 * time.Millisecond`. A Go overlay mapped that copy onto the source for this command:

```sh
go test -overlay /tmp/compatible-timeout-regression-aax7vrtd/overlay.json ./llm/openaicompat -run '^TestStreamActiveBeyondClientTotalTimeout$' -count=1 -timeout=30s
```

The command failed as intended at approximately 50 ms with `finish=false` and the missing-DONE/truncated-response error. Output is in `/tmp/dip-compatible-stream-regression-kill.log`. The positive test exercises approximately 200 ms of real HTTP activity against a 50 ms client timeout; it does not require waiting five minutes. No production source was changed for this probe.

The implementation owner separately reported successful full `go build ./...`, `go test ./... -short`, `make complexity`, `make docs-check`, and the three required dippin doctor checks. Those broader gates are owner evidence, not additional independent runs.

## Verdict

**KEEP.** No blocking correctness or security defect was reproduced in this bounded change. Active streams can exceed the previous total timeout while silence remains bounded by the configured idle deadline. Caller cancellation, completion framing, line bounds, nonstreaming deadlines, redirect policy, and provider error classification retain their intended behavior.

The idle deadline deliberately does not impose a total duration limit: continuous progress can keep a stream alive, and nonpositive idle settings disable this guard. Callers remain responsible for any total request budget. Existing channel-consumer/backpressure behavior and aggregate stream accumulation are outside this change; this verdict does not claim new guarantees for consumers that abandon a stream without cancellation. Dependency uptake and live verification require their own review.
