# Complexity ratchet (#468)

`make complexity` (and CI) runs `gate.sh gate`: it scans production Go for
gocyclo (>8), gocognit (>8), and file-size (>500 line) violations and compares
them against `baseline.txt`, failing only on a violation that is **new** (not
grandfathered) or **worse** (a higher value than baselined).

`baseline.txt` is a **ceiling that may only shrink**. Format, one per line:
`metric|file|func|value` where metric ∈ {cyclo, cognitive, filesize}
(line-insensitive — no line:col — so entries survive ordinary edits).

## Burning it down
Refactor a grandfathered function/file below the limit (or lower its value),
then `make complexity-update` to regenerate `baseline.txt`. The reviewer sees
the baseline shrink in the diff. CI's `baseline-shrinks` check rejects any PR
whose `baseline.txt` grew.

## Tools
Pinned and run via `go run`: gocyclo `v0.6.0`, gocognit `v1.2.1`. No local
install required for CI; the pre-commit hook uses locally-installed copies for a
fast staged-files check (best-effort — CI is authoritative).
