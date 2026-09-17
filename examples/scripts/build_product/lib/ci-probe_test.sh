#!/usr/bin/env bash
# ABOUTME: Fixture tests for lib/ci-probe.sh — the shared project-CI probe Setup
# ABOUTME: installs as .ai/build/ci-probe.sh (#233 Gap 1): Makefile target parsing
# ABOUTME: + language-native gates BOTH run (#640 D8), GNUmakefile precedence via
# ABOUTME: -f (D10), make-missing out-of-band marker (E8), lint hatch v1/v2 (D4/D5).
#
# Driven through verify.sh (which sources the probe at the runtime contract
# path) with PATH shims, exactly as TestMilestone exercises it; stack
# detection and the test runners are lib/verify_test.sh's.
#   V8  Makefile ci/check/lint parsing; make failure collapses to 1 (#320);
#       native gates run IN ADDITION to the make target (#640 D8)
#   V8b GNUmakefile beats Makefile, passed via -f (#640 D10)
#   V9  Makefile present + make missing → exit 1 + _TRACKER_CI_MAKE_MISSING +
#       .ai/build/ci-make-missing (#640 E8: no semantic exit number)
#   V10 known_lint_failures hatch, golangci-lint v1: one --exclude per entry,
#       a pattern with spaces is ONE argument, whitespace-only line ignored,
#       `SA1019 --fix` style entries pass intact (never split), `-…` rejected
#   V11 golangci-lint v2: temp --config with linters.exclusions.rules; with a
#       project .golangci.yml the hatch can't apply → warning + would-match list
#   V12 unparseable version → warning, no excludes, would-match list
set -uo pipefail
DIR="$(cd "$(dirname "$0")" && pwd)"
fail=0
check() { # name expected actual
  if [ "$2" = "$3" ]; then echo "ok: $1"; else echo "FAIL: $1 — want '$2' got '$3'"; fail=1; fi
}
WORK="$(mktemp -d)"
STATE="$(mktemp -d)"
trap 'rm -rf "$WORK" "$STATE"' EXIT
. "$DIR/../test_helpers.sh"
install_tool_shims
G() { git -C "$WORK" -c user.name=t -c user.email=t@t "$@"; }
G -c init.defaultBranch=main init -q
mkdir -p "$WORK/.ai/build" "$WORK/.ai/milestones"
printf '.ai/\n' > "$WORK/.gitignore"
cp "$LIB_DIR/verify.sh" "$WORK/.ai/build/verify.sh"
cp "$LIB_DIR/ci-probe.sh" "$WORK/.ai/build/ci-probe.sh"
# TEST_SH=dash runs verify.sh under dash (TestMilestone runs it via `sh`).
verify() { rm -f "$STATE/calls" "$STATE/argv"; VOUT="$( (cd "$WORK" && PATH="$STATE/bin:$PATH" "${TEST_SH:-sh}" .ai/build/verify.sh) 2>&1)"; VRC=$?; }
vhas() { printf '%s' "$VOUT" | grep -qF -- "$1" && echo yes || echo no; }
chas() { printf '%s' "$(calls)" | grep -qF -- "$1" && echo yes || echo no; }
TAB=$'\t'
# A Go stack so the language-native gate is observable next to make.
touch "$WORK/go.mod"

# V8. Makefile parsing: `build-ci:` and `ci := x` must NOT match; `check lint:`
#     matches `check`; a failing make (native exit 2) collapses to 1 (#320).
#     #640 D8: go vet / golangci-lint run whether or not a make target ran.
printf 'build-ci:\n\techo no\nci := nope\n' > "$WORK/Makefile"
verify
check "V8 no false target match"          "no" "$(chas 'make')"
check "V8 falls to language gates"        "yes" "$(vhas 'no project CI target in Makefile (looked for: ci, check, lint, test)')"
printf 'build-ci:\n\techo no\n# ci: commented\ncheck lint: deps\n\techo ok\n' > "$WORK/Makefile"
verify
check "V8 make check ran via -f"          "yes" "$(argv_has "make${TAB}-f${TAB}Makefile${TAB}check")"
check "V8 make gate message"              "yes" "$(vhas 'running make -f Makefile check (project CI gate)')"
check "V8 make ok exit 0"                 "0" "$VRC"
check "V8 D8: vet ALSO ran"               "yes" "$(chas 'go vet')"
check "V8 D8: lint ALSO ran"              "yes" "$(chas 'golangci-lint run')"
check "V8 D8: both-gates banner"          "yes" "$(vhas 'language-native gates (run in addition to any Makefile target)')"
set_rc make check 2
verify
check "V8 make rc2 collapses to 1"        "1" "$VRC"
check "V8 make red: native still ran"     "yes" "$(chas 'go vet')"
reset_rc
# #640 D8: a no-op `lint:` target must NOT neuter a red native gate.
printf 'lint:\n\t@echo ok\n' > "$WORK/Makefile"
set_rc go vet 1
verify
check "V8 no-op lint target: vet red = 1" "1" "$VRC"
reset_rc

# V8b. #640 D10: GNUmakefile wins over Makefile (GNU make's own order) and is
#      passed explicitly with -f; `ci:` beats `check:`.
printf 'check:\n\techo c\nci:\n\techo i\n' > "$WORK/GNUmakefile"
printf 'lint:\n\techo m\n' > "$WORK/Makefile"
verify
check "V8b ci from GNUmakefile via -f"    "yes" "$(argv_has "make${TAB}-f${TAB}GNUmakefile${TAB}ci")"
check "V8b Makefile's lint not run"       "no"  "$(chas 'make -f Makefile lint')"
rm -f "$WORK/GNUmakefile" "$WORK/Makefile"

# V9. Makefile present but `make` NOT installed (#640 E8): exit 1 (no
#     reserved number), the explicit marker LINE and marker FILE, and the
#     Go gates still ran first. PATH is restricted to symlinks of the tools
#     verify.sh needs, minus make.
printf 'ci:\n\techo i\n' > "$WORK/Makefile"
mkdir -p "$STATE/pbin"
for t in sh dash bash cat grep paste git awk sed sort uniq head tail tr wc ls printf mkdir rm cp mv dirname basename cut env uname mktemp date cmp find; do
  p="$(command -v "$t" 2>/dev/null)" && [ -n "$p" ] && ln -sf "$p" "$STATE/pbin/$t"
done
ln -sf "$STATE/bin/go" "$STATE/pbin/go"
rm -f "$STATE/calls" "$STATE/argv"
[ -z "${TEST_SH:-}" ] || ln -sf "$(command -v "$TEST_SH")" "$STATE/pbin/$TEST_SH"
VOUT="$( (cd "$WORK" && PATH="$STATE/pbin" "${TEST_SH:-sh}" .ai/build/verify.sh) 2>&1)"; VRC=$?
check "V9 make missing exit 1"            "1" "$VRC"
check "V9 make missing message"           "yes" "$(vhas "Makefile present but 'make' not installed — escalating")"
check "V9 marker line"                    "yes" "$(printf '%s' "$VOUT" | grep -qx '_TRACKER_CI_MAKE_MISSING' && echo yes || echo no)"
check "V9 marker file"                    "yes" "$([ -f "$WORK/.ai/build/ci-make-missing" ] && echo yes || echo no)"
check "V9 go gates still ran first"       "yes" "$(chas 'go test')"
rm -f "$WORK/Makefile"
# The marker file is cleared at the start of the next run.
verify
check "V9b marker cleared on next run"    "no" "$([ -f "$WORK/.ai/build/ci-make-missing" ] && echo yes || echo no)"

# V10. #441 hatch on golangci-lint v1 (#640 D5/E2): each entry is ONE
#      `--exclude <pat>` pair; `SA1019: .* is deprecated` stays one argument;
#      a whitespace-only line is dropped (no dangling --exclude); a leading
#      `-` is rejected with a WARNING; comments stripped.
set_out golangci-lint version 'golangci-lint has version 1.64.8 built with go1.24.1 from abc on 2025-01-01'
printf '# operator note\r\n\nSA1019: .* is deprecated\n  \nG404\n--fix\n-D\n' > "$WORK/.ai/milestones/known_lint_failures"
verify
check "V10 exit 0"                        "0" "$VRC"
check "V10 v1 --exclude pairs, one arg"   "yes" "$(argv_has "golangci-lint${TAB}run${TAB}--exclude${TAB}SA1019: .* is deprecated${TAB}--exclude${TAB}G404")"
check "V10 no dangling --exclude"         "no"  "$(chas '--exclude --exclude')"
check "V10 --fix rejected"                "yes" "$(vhas "WARNING: ignoring known_lint_failures entry '--fix'")"
check "V10 -D rejected"                   "yes" "$(vhas "WARNING: ignoring known_lint_failures entry '-D'")"
check "V10 --fix never reached argv"      "no"  "$(chas '--fix')"
check "V10 hatch banner v1"               "yes" "$(vhas 'known_lint_failures hatch: 2 pattern(s) via --exclude (golangci-lint v1)')"
# Milestone base → --new-from-rev precedes the excludes.
echo base > "$WORK/README.md"; G add -A; G commit -q -m A
G rev-parse HEAD > "$WORK/.ai/build/milestone-start-sha"
verify
check "V10 --new-from-rev + excludes"     "yes" "$(argv_has "golangci-lint${TAB}run${TAB}--new-from-rev${TAB}$(G rev-parse HEAD)${TAB}--exclude${TAB}SA1019: .* is deprecated${TAB}--exclude${TAB}G404")"
rm -f "$WORK/.ai/build/milestone-start-sha"

# V11. golangci-lint v2 (#640 D4): `--exclude` no longer exists → the entries
#      go into a temp --config (linters.exclusions.rules, path .* + text).
set_out golangci-lint version 'golangci-lint has version 2.1.6 built with go1.24.2 from abc on 2025-04-01'
verify
check "V11 exit 0"                        "0" "$VRC"
check "V11 no --exclude on v2"            "no"  "$(chas '--exclude')"
check "V11 --config passed"               "yes" "$(chas 'golangci-lint run --config ')"
check "V11 config version 2"              "yes" "$(grep -qx 'version: "2"' "$STATE/lint-config" && echo yes || echo no)"
check "V11 config exclusion rule 1"       "yes" "$(grep -qx "        text: 'SA1019: .\* is deprecated'" "$STATE/lint-config" && echo yes || echo no)"
check "V11 config exclusion rule 2"       "yes" "$(grep -qx "        text: 'G404'" "$STATE/lint-config" && echo yes || echo no)"
check "V11 config rule has path"          "yes" "$(grep -qx "      - path: '.\*'" "$STATE/lint-config" && echo yes || echo no)"
check "V11 hatch banner v2"               "yes" "$(vhas 'via --config (golangci-lint v2; --exclude was removed in v2)')"
# With a project config, v2 cannot merge: run WITHOUT the hatch, fail on the
# lint result, and list the entries that WOULD have matched the output.
echo 'version: "2"' > "$WORK/.golangci.yml"
set_out golangci-lint run 'x.go:1:1: SA1019: foo.Bar is deprecated (staticcheck)
y.go:2:2: G404: weak random (gosec)
z.go:3:3: unrelated (govet)'
set_rc golangci-lint run 1
verify
check "V11b user config: exit 1"          "1" "$VRC"
check "V11b user config: warning"         "yes" "$(vhas 'cannot merge a project .golangci.* config with the known_lint_failures hatch')"
check "V11b user config: no --config"     "no"  "$(chas '--config')"
check "V11b would-match SA1019"           "yes" "$(vhas 'would match: SA1019: .* is deprecated')"
check "V11b would-match G404"             "yes" "$(vhas 'would match: G404')"
check "V11b lint output surfaced"         "yes" "$(vhas 'z.go:3:3: unrelated')"
rm -f "$WORK/.golangci.yml"
reset_rc

# V12. Unparseable version → no hatch, loud warning, would-match report.
set_out golangci-lint version 'golangci-lint has version (devel)'
set_out golangci-lint run 'y.go:2:2: G404: weak random (gosec)'
set_rc golangci-lint run 1
verify
check "V12 unknown version: exit 1"       "1" "$VRC"
check "V12 unknown version: warning"      "yes" "$(vhas 'could not determine the golangci-lint major version')"
check "V12 unknown version: would-match"  "yes" "$(vhas 'would match: G404')"
check "V12 no flags from the hatch"       "no"  "$(chas '--exclude')"
reset_rc; rm -f "$WORK/.ai/milestones/known_lint_failures"

if [ "$fail" = 0 ]; then echo "ALL PASS"; else echo "SOME FAILED"; exit 1; fi
