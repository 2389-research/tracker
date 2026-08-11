set -eu
fpath=$(cut -f1 .ai/semport/current_file.tsv)
awk -F '\t' -v OFS='\t' -v fp="$fpath" '
NR==1 {print; next}
{ if ($1==fp && $3=="new") { $3="ported" } ; print }
' semport/ledger.tsv > semport/ledger.tsv.tmp
mv semport/ledger.tsv.tmp semport/ledger.tsv
remaining=$(awk -F '\t' 'NR>1 && $3=="new"' semport/ledger.tsv | wc -l | tr -d ' ')
printf 'ported %s (%s remaining)' "$fpath" "$remaining"