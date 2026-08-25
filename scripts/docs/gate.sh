#!/usr/bin/env bash
# ABOUTME: Doc-drift gate — mechanically enforces that structural changes are
# ABOUTME: reflected in the docs/website (CLI commands documented, release on site).
set -euo pipefail

# The two things we can check without false positives:
#   cli-coverage           every CLI command mode is documented on the website
#   release-version <tag>  a release's version is in CHANGELOG.md AND the website
# Prose completeness (does architecture.html explain a feature) is NOT
# mechanizable — that stays a review responsibility. See CLAUDE.md.

MAIN_GO="${MAIN_GO:-cmd/tracker/main.go}"
CLI_HTML="${CLI_HTML:-site/content/cli.html}"
CHANGELOG="${CHANGELOG:-CHANGELOG.md}"
CHANGELOG_HTML="${CHANGELOG_HTML:-site/content/changelog.html}"

# cli-coverage: enumerate the user-facing command strings from the commandMode
# constants (the single source of truth for what subcommands exist) and assert
# each appears in the website CLI reference. Adding a `tracker <cmd>` without
# documenting it fails here — exactly the drift that let `verify-tests` and
# `status` ship undocumented.
cli_coverage() {
  local missing=0 cmd
  local cmds
  cmds=$(grep -oE 'commandMode = "[a-z][a-z-]*"' "$MAIN_GO" | sed -E 's/.*"([a-z-]+)".*/\1/' | sort -u)
  [ -n "$cmds" ] || { echo "FATAL: found no commandMode constants in $MAIN_GO — refusing to pass" >&2; exit 1; }
  for cmd in $cmds; do
    if ! grep -qF "tracker $cmd" "$CLI_HTML"; then
      echo "  MISSING  tracker $cmd  — not documented in $CLI_HTML"
      missing=1
    fi
  done
  if [ "$missing" -ne 0 ]; then
    echo "FAIL: a CLI command is undocumented on the website. Add a block to $CLI_HTML (see a sibling command)." >&2
    exit 1
  fi
  echo "docs gate OK: all $(echo "$cmds" | wc -w | tr -d ' ') CLI commands documented in $CLI_HTML"
}

# release-version <vX.Y.Z|X.Y.Z>: the version being released must be present in
# both the source changelog and the website changelog. Wired into release.yml so
# a stale-website release cannot publish.
release_version() {
  local tag="${1:?usage: gate.sh release-version <vX.Y.Z>}"
  local ver="${tag#v}" # strip a leading v if present
  local bad=0
  if ! grep -qE "^## \[${ver//./\\.}\]" "$CHANGELOG"; then
    echo "  MISSING  ## [$ver] in $CHANGELOG"; bad=1
  fi
  if ! grep -qF "v$ver" "$CHANGELOG_HTML"; then
    echo "  MISSING  v$ver in $CHANGELOG_HTML (website changelog is stale for this release)"; bad=1
  fi
  if [ "$bad" -ne 0 ]; then
    echo "FAIL: release $tag is not fully reflected in the changelog + website. Cut the CHANGELOG version and add the website entry before tagging." >&2
    exit 1
  fi
  echo "docs gate OK: release $tag present in $CHANGELOG and $CHANGELOG_HTML"
}

# models: the website Models & Providers table is generated from llm/catalog.go
# (the source of truth) by scripts/gen/models. Regenerate to a temp file and diff
# against the committed partial — any drift (a catalog change not reflected on the
# site) fails here, so the model list can never silently rot.
MODELS_HTML="${MODELS_HTML:-site/layouts/partials/models-table.html}"
models_check() {
  local tmp
  tmp="$(mktemp)"
  GEN_MODELS_OUT="$tmp" go run ./scripts/gen/models >/dev/null
  if ! diff -q "$tmp" "$MODELS_HTML" >/dev/null 2>&1; then
    echo "FAIL: $MODELS_HTML is out of sync with dippin-lang/pricing. Run 'make gen-models' and commit the result." >&2
    diff "$MODELS_HTML" "$tmp" | head -30 >&2 || true
    rm -f "$tmp"
    exit 1
  fi
  rm -f "$tmp"
  echo "docs gate OK: models table in sync with dippin-lang/pricing"
}

# activity-schema: the activity-log reader sources (activityRawLine, ActivityEntry,
# and the toEntry copier) are generated from scripts/gen/activitylog/schema.go
# (the single field authority) by scripts/gen/activitylog. Regenerate to a temp
# dir and diff against the committed files — any drift (schema.go edited without
# regenerating, or a generated file hand-edited) fails here, so the three
# parity-locked reader shapes can never silently diverge.
ACTIVITY_RAW="${ACTIVITY_RAW:-tracker_activity_raw_gen.go}"
ACTIVITY_ENTRY="${ACTIVITY_ENTRY:-tracker_activity_entry_gen.go}"
activity_schema_check() {
  local tmp
  tmp="$(mktemp -d)"
  GEN_ACTIVITY_OUT="$tmp" go run ./scripts/gen/activitylog >/dev/null
  local bad=0 f
  for f in "$ACTIVITY_RAW" "$ACTIVITY_ENTRY"; do
    if ! diff -q "$tmp/$f" "$f" >/dev/null 2>&1; then
      echo "FAIL: $f is out of sync with scripts/gen/activitylog/schema.go. Run 'make gen-activity-schema' and commit the result." >&2
      diff "$f" "$tmp/$f" | head -30 >&2 || true
      bad=1
    fi
  done
  rm -rf "$tmp"
  [ "$bad" -eq 0 ] || exit 1
  echo "docs gate OK: activity-log reader schema in sync with scripts/gen/activitylog"
}

cmd="${1:-}"
case "$cmd" in
  cli-coverage)     cli_coverage ;;
  release-version)  release_version "${2:-}" ;;
  models)           models_check ;;
  activity-schema)  activity_schema_check ;;
  *) echo "usage: gate.sh [cli-coverage | release-version <vX.Y.Z> | models | activity-schema]" >&2; exit 2 ;;
esac
