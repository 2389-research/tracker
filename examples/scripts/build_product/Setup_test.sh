#!/usr/bin/env bash
# ABOUTME: Fixture tests for Setup.sh — workspace scaffolding, #553 spec-input
# ABOUTME: adoption, #351 .tracker/ exclusion, #264 forge-loop hygiene, #418
# ABOUTME: run-base-sha, and the GENERATED .ai/build/verify.sh + ci-probe.sh
# ABOUTME: green-gate logic (#406/#233/#299/#320/#392/#436/#441) via PATH shims.
#
# Setup.sh branch map (line refs are to Setup.sh):
#   A  scaffold .ai/{build,decisions,milestones}; append + sort -u .gitignore
#      (`.ai/` + the #405 static artifact patterns) — idempotent
#   B  #351 in a git repo: `.tracker/` -> .git/info/exclude (once); untrack a
#      pre-#351 committed .tracker/ (index only, worktree untouched)
#   C  #318 rm -rf .tracker/turn_overrides (fresh run only; Setup is skipped on
#      checkpoint resume, so a resumed run keeps its state)
#   D  #553 adopt .tracker/inputs/spec -> SPEC.md (overwrites)
#   E  no SPEC.md -> exit 1 with the `tracker init build_product` hint
#   F  write ci-probe.sh / verify.sh / iface-reachability-rubric.md
#   G  best-effort build-context.md (#298) — can never fail Setup
#   H  #418 run-base-sha: HEAD, or empty on a commitless repo / non-repo
#   I  #264 rm spec_forge_attempts, SPEC.original.md, spec-forge-log.md
#   J  `setup-ready` marker last
# Generated verify.sh (#406 single green-gate) and ci-probe.sh:
#   V1 no build system -> tests skipped, no toolchain -> exit 0
#   V2 go: build -> milestone-scoped test (#392) -> vet (#299); ./... fallback
#   V3 #436 LINT_NEW_FROM_REV only with a real base; #441 known_lint_failures
#   V4 known_failures -> `go test -skip A|B` (comments/blanks stripped)
#   V5 every runner exit collapses to 1 (a runner's 2 is NOT make-missing)
#   V6 go build failure aborts (exit 1); V7 sticky multi-stack sweep (#305)
#   V8 Makefile ci/check/lint target parsing; make failure collapses to 1 (#320)
#   V9 Makefile present + make missing -> exit 2 (sole source of rc=2)
set -uo pipefail
DIR="$(cd "$(dirname "$0")" && pwd)"
SCRIPT="$DIR/Setup.sh"
fail=0
check() { # name expected actual
  if [ "$2" = "$3" ]; then echo "ok: $1"; else echo "FAIL: $1 — want '$2' got '$3'"; fail=1; fi
}
WORK="$(mktemp -d)"
STATE="$(mktemp -d)"
trap 'rm -rf "$WORK" "$STATE"' EXIT
run() { OUT="$( (cd "$WORK" && sh "$SCRIPT") 2>"$STATE/stderr")"; RC=$?; }
last() { printf '%s' "$OUT" | tail -1; }
has() { printf '%s' "$OUT" | grep -qF -- "$1" && echo yes || echo no; }
exists() { [ -e "$WORK/$1" ] && echo present || echo gone; }
G() { git -C "$WORK" -c user.name=t -c user.email=t@t "$@"; }
reset() { rm -rf "$WORK"; mkdir -p "$WORK"; }

# ── E. Missing SPEC.md: fail loud with the actionable hint; scaffolding
#      before the check still happened; no marker.
run
check "no SPEC exit 1"                    "1" "$RC"
check "no SPEC error line"                "yes" "$(has 'ERROR: SPEC.md not found in repo root.')"
check "no SPEC init hint"                 "yes" "$(has 'tracker init build_product')"
check "no SPEC no marker"                 "no"  "$(has 'setup-ready')"
check "dirs scaffolded before check"      "present" "$(exists .ai/milestones)"

# ── A/F/H/I/J. Fresh non-git workdir with a SPEC.md.
reset
printf 'line1\nline2\nline3\n' > "$WORK/SPEC.md"
mkdir -p "$WORK/.tracker/turn_overrides" "$WORK/.ai/decisions" "$WORK/.ai/build"
echo 90 > "$WORK/.tracker/turn_overrides/Implement"
echo 3 > "$WORK/.ai/build/spec_forge_attempts"
echo old > "$WORK/.ai/decisions/SPEC.original.md"
echo old > "$WORK/.ai/decisions/spec-forge-log.md"
echo keep > "$WORK/.ai/decisions/milestones.md"
run
check "fresh exit 0"                      "0" "$RC"
check "marker last"                       "setup-ready" "$(last)"
check "line count reported"               "yes" "$(has '3 lines')"
for f in .ai/build/ci-probe.sh .ai/build/verify.sh .ai/build/iface-reachability-rubric.md .ai/build/build-context.md .ai/build/run-base-sha; do
  check "wrote $f" "present" "$(exists "$f")"
done
check "run-base-sha empty (no repo)"      "" "$(cat "$WORK/.ai/build/run-base-sha")"
check "gitignore has .ai/"                "1" "$(grep -cx '.ai/' "$WORK/.gitignore")"
check "gitignore has #405 patterns"       "yes" "$(grep -qxF 'node_modules/' "$WORK/.gitignore" && grep -qxF '*.test' "$WORK/.gitignore" && echo yes || echo no)"
check "#318 turn_overrides cleared"       "gone" "$(exists .tracker/turn_overrides)"
check "#264 forge counter cleared"        "gone" "$(exists .ai/build/spec_forge_attempts)"
check "#264 SPEC.original cleared"        "gone" "$(exists .ai/decisions/SPEC.original.md)"
check "#264 forge log cleared"            "gone" "$(exists .ai/decisions/spec-forge-log.md)"
check "other decisions kept"              "keep" "$(cat "$WORK/.ai/decisions/milestones.md")"
check "build-context header"              "# Build Context (machine-written — do not edit by hand)" "$(head -1 "$WORK/.ai/build/build-context.md")"
check "build-context no-commits label"    "yes" "$(grep -q 'as of Setup (no commits)' "$WORK/.ai/build/build-context.md" && echo yes || echo no)"
check "build-context ends at Milestones"  "## Milestones landed" "$(tail -1 "$WORK/.ai/build/build-context.md")"
# Idempotent: a second run leaves .gitignore deduped and sorted.
LINES_BEFORE="$(wc -l < "$WORK/.gitignore" | tr -d ' ')"
run
check "rerun exit 0"                      "0" "$RC"
check "gitignore deduped on rerun"        "$LINES_BEFORE" "$(wc -l < "$WORK/.gitignore" | tr -d ' ')"
check "gitignore sorted"                  "yes" "$(sort -u "$WORK/.gitignore" | cmp -s - "$WORK/.gitignore" && echo yes || echo no)"
# A user's own .gitignore entries survive the append+sort.
echo 'my-secret.env' >> "$WORK/.gitignore"
run
check "user gitignore entry kept"         "1" "$(grep -cx 'my-secret.env' "$WORK/.gitignore")"

# ── D. #553 staged input adoption overrides a repo SPEC.md; and creates one
#      when absent.
mkdir -p "$WORK/.tracker/inputs"; printf 'staged spec\n' > "$WORK/.tracker/inputs/spec"
run
check "staged spec adopted (overwrite)"   "staged spec" "$(cat "$WORK/SPEC.md")"
check "staged copy exit 0"                "0" "$RC"
rm -f "$WORK/SPEC.md"
run
check "staged spec adopted (absent)"      "staged spec" "$(cat "$WORK/SPEC.md")"
check "staged file itself untouched"      "staged spec" "$(cat "$WORK/.tracker/inputs/spec")"

# ── B/H. Git repo: .tracker/ exclusion, pre-#351 untracking, run-base-sha.
reset
printf 'spec\n' > "$WORK/SPEC.md"
G -c init.defaultBranch=main init -q
mkdir -p "$WORK/.tracker/runs/r1"; echo meta > "$WORK/.tracker/runs/r1/checkpoint.json"
echo base > "$WORK/README.md"
G add -A; G commit -q -m base           # pre-#351 polluted history: .tracker/ committed
HEAD_SHA="$(G rev-parse HEAD)"
run
check "git exit 0"                        "0" "$RC"
check "run-base-sha = HEAD"               "$HEAD_SHA" "$(cat "$WORK/.ai/build/run-base-sha")"
check "exclude has .tracker/"             "1" "$(grep -cx '.tracker/' "$WORK/.git/info/exclude")"
check ".tracker untracked from index"     "" "$(G ls-files -- .tracker)"
check ".tracker kept on disk"             "meta" "$(cat "$WORK/.tracker/runs/r1/checkpoint.json")"
check "deletion staged for next commit"   "yes" "$(G status --porcelain | grep -q '^D  .tracker/runs/r1/checkpoint.json' && echo yes || echo no)"
check "build-context has short sha"       "yes" "$(grep -q "as of Setup ($(G rev-parse --short HEAD))" "$WORK/.ai/build/build-context.md" && echo yes || echo no)"
run
check "exclude not duplicated"            "1" "$(grep -cx '.tracker/' "$WORK/.git/info/exclude")"
check "rerun with nothing to untrack ok"  "0" "$RC"
# Commitless repo: run-base-sha is genuinely empty (never the literal HEAD).
reset
printf 'spec\n' > "$WORK/SPEC.md"
G -c init.defaultBranch=main init -q
run
check "commitless exit 0"                 "0" "$RC"
check "commitless run-base-sha empty"     "" "$(cat "$WORK/.ai/build/run-base-sha")"

# ════════════════════════════════════════════════════════════════════
# Generated verify.sh / ci-probe.sh — run in a fresh git repo with PATH
# shims. Each shim logs its argv to $STATE/calls and exits with the code in
# $STATE/rc-<tool>-<sub> (default 0).
# ════════════════════════════════════════════════════════════════════
mkdir -p "$STATE/bin"
for tool in go npm uv cargo make golangci-lint; do
  cat > "$STATE/bin/$tool" <<SHIM
#!/bin/sh
echo "$tool \$*" >> "$STATE/calls"
rc="$STATE/rc-$tool-\${1:-none}"
[ -f "\$rc" ] && exit "\$(cat "\$rc")"
exit 0
SHIM
  chmod +x "$STATE/bin/$tool"
done
set_rc() { echo "$3" > "$STATE/rc-$1-$2"; }
reset_rc() { rm -f "$STATE"/rc-*; }
verify() { rm -f "$STATE/calls"; VOUT="$( (cd "$WORK" && PATH="$STATE/bin:$PATH" sh .ai/build/verify.sh) 2>&1)"; VRC=$?; }
calls() { [ -f "$STATE/calls" ] && paste -sd';' "$STATE/calls" || echo ""; }
vhas() { printf '%s' "$VOUT" | grep -qF -- "$1" && echo yes || echo no; }

reset
printf 'spec\n' > "$WORK/SPEC.md"
G -c init.defaultBranch=main init -q
run
check "verify fixture setup ok"           "0" "$RC"

# V1. No build system, no Makefile.
verify
check "V1 exit 0"                         "0" "$VRC"
check "V1 tests skipped"                  "yes" "$(vhas 'no known build system — skipping tests')"
check "V1 no Makefile info"               "yes" "$(vhas 'no Makefile present — running language-native gates')"
check "V1 no toolchain info"              "yes" "$(vhas 'no recognized toolchain')"
check "V1 no calls"                       "" "$(calls)"

# V2. Go stack, no commits: build, test ./..., vet; golangci-lint absent
#     warning is emitted only when the tool is really missing — with the
#     shim present it runs WITHOUT --new-from-rev (empty-tree base).
touch "$WORK/go.mod"
verify
check "V2 exit 0"                         "0" "$VRC"
check "V2 call order"                     "go build ./...;go test ./...;go vet ./...;golangci-lint run" "$(calls)"
check "V2 ./... fallback message"         "yes" "$(vhas 'no changed Go packages in milestone range — testing ./...')"
check "V2 no lint scoping w/o base"       "no" "$(vhas '--new-from-rev')"

# V3. #392 milestone scoping: commit A (base), then commit B touching
#     pkg/a/a.go and root main.go → targets `. ./pkg/a`; #436 lint scoped to
#     the base; #441 known_lint_failures → --exclude (comments stripped).
#
#     KNOWN-BUG (portability): verify.sh derives the package list with
#       awk -F/ '... sub(/\/[^/]*$/, "") ...'
#     The unescaped `/` inside the bracket expression of a regex LITERAL is
#     accepted by gawk (and mawk >= 1.3.4, Ubuntu's default), but BSD/macOS
#     awk terminates the literal at it ("nonterminated character class") —
#     so on macOS the scoping SILENTLY degrades to `go test ./...` with an
#     awk error interleaved in the node output, and #392's "only the changed
#     packages" contract does not hold. `[^\/]` would be portable. The
#     suite probes the local awk and asserts whichever behaviour applies so
#     the run is deterministic on both platforms; when the script is fixed
#     the BSD branch becomes unreachable and can be deleted.
if printf 'a/b.go\n' | awk -F/ 'NF>1 { sub(/\/[^/]*$/, ""); print "./" $0 }' 2>/dev/null | grep -qx './a'; then
  AWK_OK=1; SCOPED_TARGET=". ./pkg/a"; echo "info: awk accepts [^/] in a regex literal — asserting #392 scoping"
else
  AWK_OK=0; SCOPED_TARGET="./..."; echo "info: BSD awk — asserting the KNOWN-BUG ./... fallback"
fi
mkdir -p "$WORK/pkg/a"; echo base > "$WORK/README.md"; G add -A; G commit -q -m A
printf '%s\n' "$(G rev-parse HEAD)" > "$WORK/.ai/build/milestone-start-sha"
echo 'package a' > "$WORK/pkg/a/a.go"; echo 'package main' > "$WORK/main.go"
G add -A; G commit -q -m B
BASE_SHA="$(cat "$WORK/.ai/build/milestone-start-sha")"
printf '# operator note\n\nSA1019\nG404\n' > "$WORK/.ai/milestones/known_lint_failures"
verify
check "V3 exit 0"                         "0" "$VRC"
check "V3 go test target"                 "yes" "$(printf '%s' "$(calls)" | grep -qF "go test $SCOPED_TARGET" && echo yes || echo no)"
if [ "$AWK_OK" = 1 ]; then
  check "V3 scoped message"               "yes" "$(vhas 'milestone-scoped go test: . ./pkg/a')"
  check "V3 no awk error"                 "no"  "$(vhas 'nonterminated character class')"
else
  check "V3 KNOWN-BUG awk error surfaced" "yes" "$(vhas 'nonterminated character class')"
  check "V3 KNOWN-BUG fell back to ./..." "yes" "$(vhas 'no changed Go packages in milestone range')"
fi
check "V3 lint --new-from-rev base"       "yes" "$(printf '%s' "$(calls)" | grep -q "golangci-lint run --new-from-rev $BASE_SHA --exclude SA1019 --exclude G404" && echo yes || echo no)"
# KNOWN-BUG (#441 hatch): a WHITESPACE-ONLY line in known_lint_failures is
# not stripped — the `case ''|\#*` guard only catches truly empty and comment
# lines — so it becomes a dangling `--exclude` whose VALUE is the next
# `--exclude` flag, and the real pattern after it (G404) is demoted to a
# positional path argument. CLAUDE.md's "strip comments AND blank lines"
# rule intends `[[:space:]]*` blanks too. When fixed, expect no
# `--exclude --exclude` and G404 excluded.
printf 'SA1019\n  \nG404\n' > "$WORK/.ai/milestones/known_lint_failures"
verify
check "V3c KNOWN-BUG dangling --exclude (want no)" "yes" "$(printf '%s' "$(calls)" | grep -qF -- '--exclude SA1019 --exclude --exclude G404' && echo yes || echo no)"
# Unreachable start SHA → empty-tree base → all packages, no lint scoping.
echo deadbeefdeadbeefdeadbeefdeadbeefdeadbeef > "$WORK/.ai/build/milestone-start-sha"
rm -f "$WORK/.ai/milestones/known_lint_failures"
verify
check "V3b unreachable base exit 0"       "0" "$VRC"
check "V3b no --new-from-rev"             "no" "$(printf '%s' "$(calls)" | grep -q -- '--new-from-rev' && echo yes || echo no)"
check "V3b all pkgs from empty tree"      "yes" "$(printf '%s' "$(calls)" | grep -qF "go test $SCOPED_TARGET" && echo yes || echo no)"
rm -f "$WORK/.ai/build/milestone-start-sha"

# V4. #known_failures → -skip regex with comments and blank lines stripped.
printf '# expected to fail until m3\nTestStream\n\nTestFlaky\n' > "$WORK/.ai/milestones/known_failures"
verify
check "V4 -skip pattern"                  "yes" "$(printf '%s' "$(calls)" | grep -qF "go test $SCOPED_TARGET -skip TestStream|TestFlaky" && echo yes || echo no)"
check "V4 skip message"                   "yes" "$(vhas 'skipping known failures: TestStream|TestFlaky')"
rm -f "$WORK/.ai/milestones/known_failures"

# V5. A test runner exiting 2 collapses to exit 1 (never the make-missing 2);
#     later gates still run.
set_rc go test 2
verify
check "V5 runner rc2 -> 1"                "1" "$VRC"
check "V5 vet still ran"                  "yes" "$(printf '%s' "$(calls)" | grep -q 'go vet' && echo yes || echo no)"
reset_rc
# go vet failure (language-native gate) also collapses to 1.
set_rc go vet 3
verify
check "V5b vet fail -> 1"                 "1" "$VRC"
reset_rc

# V6. go build failure aborts immediately (unguarded): no test, no vet.
set_rc go build 1
verify
check "V6 build fail exit 1"              "1" "$VRC"
check "V6 nothing after build"            "go build ./..." "$(calls)"
reset_rc

# V7. #305 sticky sweep: go test fails, npm passes → exit 1, both ran.
touch "$WORK/package.json"
set_rc go test 1
verify
check "V7 exit 1"                         "1" "$VRC"
check "V7 npm still ran"                  "yes" "$(printf '%s' "$(calls)" | grep -q 'npm test' && echo yes || echo no)"
check "V7 plain-JS: tsc skipped"          "yes" "$(vhas 'no tsconfig.json — skipping tsc')"
check "V7 plain-JS: eslint skipped"       "yes" "$(vhas 'no eslint config — skipping eslint')"
reset_rc; rm -f "$WORK/package.json"

# V8. Makefile parsing: `build-ci:` and `ci := x` must NOT match; `check lint:`
#     matches `check`; a failing make (native exit 2) collapses to 1 (#320).
printf 'build-ci:\n\techo no\nci := nope\n' > "$WORK/Makefile"
verify
check "V8 no false target match"          "no" "$(printf '%s' "$(calls)" | grep -q 'make' && echo yes || echo no)"
check "V8 falls to language gates"        "yes" "$(vhas 'no project CI target in Makefile (looked for: ci, check, lint)')"
printf 'build-ci:\n\techo no\n# ci: commented\ncheck lint: deps\n\techo ok\n' > "$WORK/Makefile"
verify
check "V8 make check ran"                 "yes" "$(printf '%s' "$(calls)" | grep -q 'make check' && echo yes || echo no)"
check "V8 make gate message"              "yes" "$(vhas 'running make check (project CI gate from Makefile)')"
check "V8 make ok exit 0"                 "0" "$VRC"
check "V8 no vet when make ran"           "no" "$(printf '%s' "$(calls)" | grep -q 'go vet' && echo yes || echo no)"
set_rc make check 2
verify
check "V8 make rc2 collapses to 1"        "1" "$VRC"
reset_rc
# `ci:` wins over `check:` (search order ci, check, lint); GNUmakefile honored.
rm -f "$WORK/Makefile"; printf 'check:\n\techo c\nci:\n\techo i\n' > "$WORK/GNUmakefile"
verify
check "V8 ci preferred, GNUmakefile"      "yes" "$(printf '%s' "$(calls)" | grep -q 'make ci' && echo yes || echo no)"
rm -f "$WORK/GNUmakefile"

# V9. Makefile present but `make` NOT installed → exit 2 (escalate). PATH is
#     restricted to a dir of symlinks for the tools verify.sh needs, minus make.
printf 'ci:\n\techo i\n' > "$WORK/Makefile"
mkdir -p "$STATE/pbin"
for t in sh dash bash cat grep paste git awk sed sort uniq head tail tr wc ls printf mkdir rm cp dirname basename cut env uname; do
  p="$(command -v "$t" 2>/dev/null)" && [ -n "$p" ] && ln -sf "$p" "$STATE/pbin/$t"
done
ln -sf "$STATE/bin/go" "$STATE/pbin/go"
rm -f "$STATE/calls"
VOUT="$( (cd "$WORK" && PATH="$STATE/pbin" sh .ai/build/verify.sh) 2>&1)"; VRC=$?
check "V9 make missing exit 2"            "2" "$VRC"
check "V9 make missing message"           "yes" "$(vhas "Makefile present but 'make' not installed — escalating")"
check "V9 go gates still ran first"       "yes" "$(printf '%s' "$(calls)" | grep -q 'go test' && echo yes || echo no)"
rm -f "$WORK/Makefile"

if [ "$fail" = 0 ]; then echo "ALL PASS"; else echo "SOME FAILED"; exit 1; fi
