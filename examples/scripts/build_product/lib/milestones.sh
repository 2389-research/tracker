# ABOUTME: Shared milestone-plan bookkeeping for build_product.
# ABOUTME: Sourced (never executed) via ${graph.workflow_dir}/scripts/build_product/lib.

# count_done_milestones DONE_DIR — number of completion markers
# PickNextMilestone/MarkMilestoneDone keep in DONE_DIR (one file per finished
# milestone). Prints the count; 0 when the dir is absent.
count_done_milestones() {
  ls "$1" 2>/dev/null | wc -l | tr -d ' '
}
