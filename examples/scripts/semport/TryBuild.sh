set -eu
mkdir -p .ai/semport
LOG=.ai/semport/build.log   # per-workdir; read by LoadErrors and VerifyBuild (#646 item 11)
swift build --target OmniAgentsSDK >"$LOG" 2>&1 || { printf 'FAIL\n'; grep 'error:' "$LOG" | head -40 || true; exit 1; }
printf 'PASS'
