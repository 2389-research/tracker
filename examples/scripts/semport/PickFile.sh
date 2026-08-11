set -eu
next_row=$(awk -F '\t' 'NR>1 && $3=="new"{print; exit}' semport/ledger.tsv || true)
if [ -z "$next_row" ]; then
  printf 'ALL_PORTED'
  exit 1
fi
printf '%s\n' "$next_row" > .ai/semport/current_file.tsv
filepath=$(printf '%s' "$next_row" | cut -f1)
cp "references/openai-agents-python/src/agents/$filepath" .ai/semport/current_source.py
printf 'Picked: %s' "$filepath"