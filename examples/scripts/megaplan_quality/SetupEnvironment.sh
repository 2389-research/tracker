set -eu
mkdir -p .ai/drafts .ai/sprints
if [ ! -f .ai/ledger.tsv ]; then
  printf 'sprint_id\ttitle\tstatus\tcreated_at\tupdated_at\n' > .ai/ledger.tsv
fi
printf 'ready'