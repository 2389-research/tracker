set -eu
mkdir -p .ai .ai/drafts .ai/sprints
if [ ! -f .ai/ledger.tsv ]; then
  now=$(date -u +%Y-%m-%dT%H:%M:%SZ)
  printf 'sprint_id\ttitle\tstatus\tcreated_at\tupdated_at\n001\tBootstrap sprint\tplanned\t%s\t%s\n' "$now" "$now" > .ai/ledger.tsv
fi
printf 'ledger-ready'