set -eu
mkdir -p .ai
# next_sprint_id — print the zero-padded id after the highest one in the
# ledger (001 when the ledger is empty/absent). #646 item 6: `$((10#$last))`
# is a bashism (dash: `arithmetic expression: expecting EOF`, rc 2 on every
# sprint after 001) and a bare `$((008))` is invalid octal on every shell, so
# awk parses the ids as decimal instead. A non-numeric id anywhere in the
# ledger fails loud instead of silently restarting at 001.
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
next=$(next_sprint_id)
printf '%s' "$next" > .ai/current_sprint_id.txt
printf 'sprint-%s' "$next"
