set -eu
mkdir -p .ai
if [ ! -f .ai/ledger.tsv ]; then
  printf 'sprint_id\ttitle\tstatus\tcreated_at\tupdated_at\n' > .ai/ledger.tsv
fi
# Same portable next-id derivation as DetermineSprintId.sh (#646 item 6):
# awk parses the ids as decimal (no `10#`), a non-numeric ledger id fails loud.
next_sprint_id() {
  # awk does the decimal parse ($1+0 reads 008 as 8) and the max, skips blank
  # rows (a trailing newline is not an id), and flags the first non-numeric
  # id — decided in END, since `exit` in a rule still runs END; the shell
  # only pads.
  last=$(awk -F '\t' 'NR>1 && NF==0 { next }
                     NR>1 && $1 !~ /^[0-9]+$/ { bad = $1; exit }
                     NR>1 && $1+0 > max { max = $1+0 }
                     END { if (bad != "") print "BAD " bad; else if (max) print max }' .ai/ledger.tsv 2>/dev/null || true)
  case "$last" in
    '')    printf '001' ;;
    BAD\ *) echo "ERROR: .ai/ledger.tsv sprint_id '${last#BAD }' is not numeric — fix the ledger" >&2; exit 1 ;;
    *)     printf '%03d' $((last + 1)) ;;
  esac
}
if [ -f .ai/current_sprint_id.txt ]; then
  sprint_id=$(cat .ai/current_sprint_id.txt)
else
  sprint_id=$(next_sprint_id)
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
