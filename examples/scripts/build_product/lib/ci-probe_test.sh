#!/usr/bin/env bash
# ABOUTME: Fixture tests for lib/ci-probe.sh — the shared project-CI probe Setup
# ABOUTME: installs as .ai/build/ci-probe.sh (#233 Gap 1): Makefile ci/check/lint
# ABOUTME: target parsing, make's exit collapsing to 1 (#320), and the sole
# ABOUTME: rc=2 source — Makefile present but `make` missing (escalate).
#
# Driven through verify.sh (which sources the probe at the runtime contract
# path) with PATH shims, exactly as TestMilestone exercises it; the
# language-native gate fall-through (#299) is covered by lib/verify_test.sh.
# These checks were Setup_test.sh's until the probe moved out of Setup's
# heredoc.
#   V8 Makefile ci/check/lint target parsing; make failure collapses to 1 (#320)
#   V9 Makefile present + make missing -> exit 2 (sole source of rc=2)
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
# Fixture: a fresh git repo with the two lib files installed at the runtime
# contract paths, exactly as Setup copies them (Setup_test.sh proves the copy
# is byte-identical). verify.sh sources .ai/build/ci-probe.sh itself.
G -c init.defaultBranch=main init -q
mkdir -p "$WORK/.ai/build" "$WORK/.ai/milestones"
cp "$LIB_DIR/verify.sh" "$WORK/.ai/build/verify.sh"
cp "$LIB_DIR/ci-probe.sh" "$WORK/.ai/build/ci-probe.sh"
verify() { rm -f "$STATE/calls"; VOUT="$( (cd "$WORK" && PATH="$STATE/bin:$PATH" sh .ai/build/verify.sh) 2>&1)"; VRC=$?; }
vhas() { printf '%s' "$VOUT" | grep -qF -- "$1" && echo yes || echo no; }
# A Go stack so the "no vet when make ran" / "go gates still ran first"
# checks have a language-native gate to observe.
touch "$WORK/go.mod"

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
