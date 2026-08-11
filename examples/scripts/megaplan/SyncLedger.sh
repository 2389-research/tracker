set -eu
mkdir -p .ai
if [ ! -f .ai/ledger.tsv ]; then
  printf 'sprint_id\ttitle\tstatus\tcreated_at\tupdated_at\n' > .ai/ledger.tsv
fi
if [ -f .ai/current_sprint_id.txt ]; then
  sprint_id=$(cat .ai/current_sprint_id.txt)
else
  last=$(awk -F '\t' 'NR>1{print $1}' .ai/ledger.tsv | sort | tail -n1)
  if [ -z "$last" ]; then sprint_id=001; else sprint_id=$(printf '%03d' $((10#$last + 1))); fi
fi
now=$(date -u +%Y-%m-%dT%H:%M:%SZ)
if awk -F '\t' -v target="$sprint_id" 'NR>1 && $1==target {found=1} END{exit found?0:1}' .ai/ledger.tsv; then
  awk -F '\t' -v OFS='\t' -v target="$sprint_id" -v now="$now" '
NR==1 {print; next}
{ if ($1==target) { $2="Generated Sprint " target; $3="planned"; $5=now }; print }
' .ai/ledger.tsv > .ai/ledger.tsv.tmp
  mv .ai/ledger.tsv.tmp .ai/ledger.tsv
else
  printf '%s\t%s\t%s\t%s\t%s\n' "$sprint_id" "Generated Sprint $sprint_id" "planned" "$now" "$now" >> .ai/ledger.tsv
fi
printf 'synced-%s' "$sprint_id"