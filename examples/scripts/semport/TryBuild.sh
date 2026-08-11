set -eu
swift build --target OmniAgentsSDK >/tmp/semport-build.log 2>&1 || { printf 'FAIL\n'; grep 'error:' /tmp/semport-build.log | head -40; exit 1; }
printf 'PASS'