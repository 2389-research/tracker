set -eu
swift build --target OmniAgentsSDK >/tmp/semport-build.log 2>&1 || { errs=$(grep -c 'error:' /tmp/semport-build.log); printf 'STILL_FAILING (%s errors)' "$errs"; exit 1; }
printf 'BUILD_CLEAN'