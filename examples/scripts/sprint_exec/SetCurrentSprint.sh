set -eu
target=$(awk -F '\t' 'NR>1 && $3!~/^completed$/ && $3!~/^skipped$/{print $1; exit}' .ai/ledger.tsv)
if [ -z "$target" ]; then
  target=$(awk -F '\t' 'NR>1 && $3!~/^completed$/{print $1; exit}' .ai/ledger.tsv)
fi
if [ -z "$target" ]; then
  target=$(awk -F '\t' 'END{print $1}' .ai/ledger.tsv)
fi
printf '%s' "$target" > .ai/current_sprint_id.txt
printf 'current-%s' "$target"