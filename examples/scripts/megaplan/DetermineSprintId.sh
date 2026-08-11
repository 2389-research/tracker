set -eu
mkdir -p .ai
last=$(awk -F '\t' 'NR>1{print $1}' .ai/ledger.tsv | sort | tail -n1)
if [ -z "$last" ]; then
  next=001
else
  next=$(printf '%03d' $((10#$last + 1)))
fi
printf '%s' "$next" > .ai/current_sprint_id.txt
printf 'sprint-%s' "$next"