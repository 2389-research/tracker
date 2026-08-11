set -eu
RUN_DIR=$(ls -dt .ai/attractor-runs/semport-* | head -1)
response="$RUN_DIR/FixErrors/response.md"
if [ ! -f "$response" ]; then
  printf 'no response'
  exit 1
fi
python3 -c "
import re, sys, os
text = open('$response').read()
blocks = re.findall(r'\x60\x60\x60swift:([^\n]+)\n(.*?)\x60\x60\x60', text, re.DOTALL)
if not blocks:
    print('no swift blocks found', file=sys.stderr)
    sys.exit(1)
for path, code in blocks:
    path = path.strip()
    os.makedirs(os.path.dirname(path), exist_ok=True)
    with open(path, 'w') as f:
        f.write(code)
    print(f'Wrote {path} ({len(code)} bytes)')
"