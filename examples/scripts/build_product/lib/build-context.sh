# ABOUTME: Seeds the per-node build-context orientation file (issue #298).
# ABOUTME: Sourced (never executed) via ${graph.workflow_dir}/scripts/build_product/lib.

# seed_build_context — write .ai/build/build-context.md. Advisory artifact:
# the whole group is best-effort (|| true) so it can never dead-stop Setup.
# Each producer pipes to `head`, and each of those lines carries its own
# `|| true`: `head` exits once it has its N lines, so a longer producer gets
# SIGPIPE (141) — harmless when pipefail is off (the status is `head`'s 0),
# but under a caller that enables pipefail the 141 would otherwise abort the
# group at the first long listing (#658). A no-match `git grep` likewise must
# not trip set -e. The map is frozen at Setup and labelled so later-milestone
# agents down-weight it vs the authoritative milestone log / code. Caps keep
# the file SHORT (#298 §4). MarkMilestoneDone appends one "## Milestones
# landed" entry per milestone.
seed_build_context() {
  {
    echo "# Build Context (machine-written — do not edit by hand)"
    echo
    echo "_Architecture map as of Setup ($(git rev-parse --short --verify --quiet HEAD 2>/dev/null || echo 'no commits')). Packages may change as milestones land; the milestone log below is authoritative for what moved._"
    echo
    echo "## Top-level layout"
    git ls-files 2>/dev/null | awk -F/ 'NF>1{print $1"/"} NF==1{print}' | sort -u | head -40 || true
    echo
    echo "## Languages"
    git ls-files 2>/dev/null | awk -F. 'NF>1{print $NF}' | sort | uniq -c | sort -rn | head -15 || true
    echo
    echo "## Entry points"
    git ls-files 2>/dev/null | grep -E '(^|/)(main\.go|index\.[jt]s|main\.py|__main__\.py|main\.rs|Main\.java)$|(^|/)cmd/' | head -20 || true
    echo
    echo "## Key interfaces (best-effort: Go / TS / Rust)"
    # Rust traits are almost always `pub trait` / `pub(crate) trait` — the
    # visibility prefix is optional here so the Rust arm actually fires.
    git grep -nE 'type [[:alnum:]_]+ +interface[[:space:]{]|^(export )?(abstract )?(pub(\([^)]*\))? )?(interface|trait) ' -- '*.go' '*.ts' '*.tsx' '*.rs' 2>/dev/null | head -20 || true
    echo
    echo "## Milestones landed"
  } > .ai/build/build-context.md 2>/dev/null || true
}
