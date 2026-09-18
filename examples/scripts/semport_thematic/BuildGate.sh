set -eu
[ -n "${target_name:-}" ] || { echo "ERROR: target_name is not set — export target_name=<SwiftTargetName> before running semport_thematic" >&2; exit 1; }
mkdir -p ".ai/semport/$target_name"
LOG=".ai/semport/$target_name/build.log"   # per-workdir, not a shared /tmp path (#646 item 11)
swift build --target "$target_name" >"$LOG" 2>&1 || {
  printf 'BUILD_FAIL\n'
  grep 'error:' "$LOG" | head -80 || true
  exit 1
}
printf 'BUILD_PASS'
