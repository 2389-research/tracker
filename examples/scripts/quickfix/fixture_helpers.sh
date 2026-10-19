# ABOUTME: Shared fixture for the quickfix suites: a git repo under a path with a space,
# ABOUTME: the engine's run env (TRACKER_RUN_ID/DIR), and staged copies of the scripts.
#
# Sourced by examples/scripts/quickfix/*_test.sh after they set DIR. Provides
# WORK (the repo), STATE (scratch), RUN_ID / RUN_DIR, WT / BR (the paths
# Preflight creates), check, G (git in WORK) and qf_run SCRIPT (runs a staged
# script from WORK the way a tool node does: OUT=stdout, ERR=stderr, RC).
fail=0
check() { # name expected actual
  if [ "$2" = "$3" ]; then echo "ok: $1"; else echo "FAIL: $1 — want '$2' got '$3'"; fail=1; fi
}
ROOT="$(mktemp -d)"
STATE="$ROOT/state"
WORK="$ROOT/my repo"
mkdir -p "$STATE" "$WORK"
trap 'chmod -R u+w "$ROOT" 2>/dev/null; rm -rf "$ROOT"' EXIT
. "$DIR/../build_product/test_helpers.sh"
export HOME="$ROOT/home" XDG_CONFIG_HOME="$ROOT/xdg" GIT_CONFIG_NOSYSTEM=1
mkdir -p "$HOME" "$XDG_CONFIG_HOME"
RUN_ID="run-123"
RUN_DIR="$WORK/.tracker/runs/$RUN_ID"
WT="$WORK/.tracker/quickfix/worktree"
BR="tracker/quickfix/$RUN_ID"
G() { git -C "$WORK" "$@"; }

# init_repo — git init WORK on main with a repo-local identity (the harness's
# hermetic env gives no global git config to lean on).
init_repo() {
  G init -q -b main
  G config user.name tester
  G config user.email tester@example.com
}

# new_repo — a fresh repo in WORK with one commit (README.md) and the run dir.
new_repo() {
  rm -rf "$WORK"; mkdir -p "$WORK" "$RUN_DIR"
  init_repo
  echo base > "$WORK/README.md"
  G add README.md; G commit -q -m base
}

# qf_run SCRIPT [ENV=VAL…] — run a staged script from WORK as a tool node does.
qf_run() {
  local script="$1"; shift
  OUT="$( (cd "$WORK" && env TRACKER_RUN_ID="$RUN_ID" TRACKER_RUN_DIR="$RUN_DIR" "$@" ${TEST_SH:-sh} "$script") 2>"$STATE/stderr")"; RC=$?
  ERR="$(cat "$STATE/stderr")"
}

# exists PATH — yes/no.
exists() { [ -e "$1" ] && echo yes || echo no; }
