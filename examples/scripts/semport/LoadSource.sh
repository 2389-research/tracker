set -eu
filepath=$(cut -f1 .ai/semport/current_file.tsv)
printf '=== Python Source: %s ===\n' "$filepath"
cat .ai/semport/current_source.py
printf '\n\n=== Existing OmniAgentsSDK Swift files ===\n'
find Sources/OmniAgentsSDK -name '*.swift' -type f | sort
printf '\n\n=== OmniAICore public types ===\n'
grep -h 'public.*struct \|public.*protocol \|public.*class \|public.*enum ' Sources/OmniAICore/*.swift 2>/dev/null | head -50 || true