set -eu
# Save the Answer node's response next to the run's other state and point at
# the next step. Reads only what the engine wrote (the agent's response.md);
# no user or model text is interpolated into this command. Writes only under
# the run dir — tracked files are never touched. Routing is on the exit code
# alone: errors go to stderr and the notice is a heredoc, so no line reads as
# a routing marker.
die() { printf 'ERROR: WriteAnswer: %s\n' "$*" >&2; exit 1; }
[ -n "${TRACKER_RUN_DIR:-}" ] || die "TRACKER_RUN_DIR is not set — run explain through tracker"
SRC="$TRACKER_RUN_DIR/Answer/response.md"
[ -s "$SRC" ] || die "no answer at $SRC — the Answer node produced nothing to save"
cp "$SRC" "$TRACKER_RUN_DIR/explain.md"
cat <<EOF
answer saved to $TRACKER_RUN_DIR/explain.md
next: make a small change with
  tracker quickfix --param change="<one sentence>"
EOF
