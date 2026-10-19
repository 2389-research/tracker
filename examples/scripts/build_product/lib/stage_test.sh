#!/usr/bin/env bash
# ABOUTME: Fixture tests for lib/stage.sh — the per-invocation excludes the commit
# ABOUTME: nodes stage through: secrets and compiled binaries skipped, global excludes kept.
#
# stage_build_excludes is sourced by CommitIfDirty.sh and FinalCommit.sh. Both
# callers' suites keep the end-to-end evidence; this suite pins the rules.
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
# Driver: compute the excludes the way a caller does, then list what
# `git add -A` would stage (untracked, not excluded).
cat > "$STATE/drive.sh" <<DRIVER
set -eu
. "$LIB_DIR/stage.sh"
T=\$(mktemp -d)
trap 'rm -rf "\$T"' EXIT
stage_build_excludes "\$T"
echo "--- staged:"
stage_git ls-files --others --exclude-standard
DRIVER
run() { OUT="$( (cd "$WORK" && ${TEST_SH:-sh} "$STATE/drive.sh") 2>"$STATE/stderr")"; RC=$?; }
staged() { has_line "$1"; }
G() { git -C "$WORK" "$@"; }
export HOME="$STATE/home" XDG_CONFIG_HOME="$STATE/xdg"
mkdir -p "$HOME" "$XDG_CONFIG_HOME/git"
G -c init.defaultBranch=main init -q

echo 'A=1' > "$WORK/.env"
echo 'A=' > "$WORK/.env.example"
echo 'A=1' > "$WORK/.env.local"
printf -- '-----BEGIN RSA PRIVATE KEY-----\nabc\n-----END RSA PRIVATE KEY-----\n' > "$WORK/server.pem"
printf -- '-----BEGIN CERTIFICATE-----\nabc\n-----END CERTIFICATE-----\n' > "$WORK/cert.pem"
printf '\177ELF\000\001binary\000\n' > "$WORK/app"; chmod +x "$WORK/app"
printf '#!/bin/sh\necho hi\n' > "$WORK/run.sh"; chmod +x "$WORK/run.sh"
echo 'x' > "$WORK/notes.DS_Store"
echo '*.DS_Store' > "$XDG_CONFIG_HOME/git/ignore"
mkdir -p "$WORK/my dir"
echo 'A=1' > "$WORK/my dir/.env"
printf '\177ELF\000\001binary\000\n' > "$WORK/my dir/tool bin"; chmod +x "$WORK/my dir/tool bin"
echo 'src' > "$WORK/my dir/main.go"
run
check "exit 0"                            "0" "$RC"
check "driver reached the listing"        "yes" "$(has_line '--- staged:')"
check ".env skipped"                      "no" "$(staged .env)"
check ".env.local skipped"                "no" "$(staged .env.local)"
check ".env.example kept"                 "yes" "$(staged .env.example)"
check "private-key .pem skipped"          "no" "$(staged server.pem)"
check "public .pem kept"                  "yes" "$(staged cert.pem)"
check "compiled binary skipped"           "no" "$(staged app)"
check "executable script kept"            "yes" "$(staged run.sh)"
check "global excludes honored"           "no" "$(staged notes.DS_Store)"
check "secret in a spaced dir skipped"    "no" "$(staged 'my dir/.env')"
check "binary with spaces skipped"        "no" "$(staged 'my dir/tool bin')"
check "source in a spaced dir kept"       "yes" "$(staged 'my dir/main.go')"
check "secret warning printed"            "yes" "$(has 'WARNING: not staging secret-looking untracked file(s)')"
check "binary skip line printed"          "yes" "$(has 'skipping compiled binary artifact: app')"
check "never writes info/exclude"         "no" "$(contains "$(cat "$WORK/.git/info/exclude" 2>/dev/null)" '.env')"
check "never writes .gitignore"           "no" "$([ -e "$WORK/.gitignore" ] && echo yes || echo no)"
[ "$fail" -eq 0 ] || { echo "stderr:"; cat "$STATE/stderr"; }

exit "$fail"
