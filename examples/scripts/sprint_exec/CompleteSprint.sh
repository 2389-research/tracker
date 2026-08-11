set -eu
target=$(cat .ai/current_sprint_id.txt)
now=$(date -u +%Y-%m-%dT%H:%M:%SZ)
awk -F '\t' -v OFS='\t' -v target="$target" -v now="$now" '
NR==1 {print; next}
{ if ($1==target) { $3="completed"; $5=now }; print }
' .ai/ledger.tsv > .ai/ledger.tsv.tmp
mv .ai/ledger.tsv.tmp .ai/ledger.tsv
printf 'completed-%s' "$target"