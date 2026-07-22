// ABOUTME: Golden-trace conformance test. Regenerates each fixture's normalized
// ABOUTME: trace and diffs it against the committed .golden.json; -update-golden
// ABOUTME: rewrites the goldens. A mismatch means an engine event/handler/usage
// ABOUTME: contract drifted — the exact signal downstream ports need to catch.
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"testing"
)

var updateGolden = flag.Bool("update-golden", false, "regenerate golden-trace .golden.json fixtures")

// encodeGolden serializes a goldenTrace with the same settings as writeJSON so a
// committed .golden.json is byte-identical to `tracker-conformance golden <fixture>`
// stdout — downstream can diff the binary's output against the file directly.
func encodeGolden(t *testing.T, gt *goldenTrace) []byte {
	t.Helper()
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	if err := enc.Encode(gt); err != nil {
		t.Fatalf("encode golden: %v", err)
	}
	return buf.Bytes()
}

func TestGoldenTraces(t *testing.T) {
	for _, name := range []string{"agent_linear", "control_flow", "tool_failure"} {
		t.Run(name, func(t *testing.T) {
			dip := filepath.Join("testdata", "golden", name+".dip")
			goldenPath := filepath.Join("testdata", "golden", name+".golden.json")

			gt, err := generateGoldenTrace(dip)
			if err != nil {
				t.Fatalf("generateGoldenTrace(%s): %v", dip, err)
			}
			got := encodeGolden(t, gt)

			if *updateGolden {
				if err := os.WriteFile(goldenPath, got, 0o644); err != nil {
					t.Fatalf("write golden: %v", err)
				}
				t.Logf("updated %s", goldenPath)
				return
			}

			want, err := os.ReadFile(goldenPath)
			if err != nil {
				t.Fatalf("read golden (regenerate with: go test ./cmd/tracker-conformance -run TestGoldenTraces -update-golden): %v", err)
			}
			if !bytes.Equal(got, want) {
				t.Errorf("golden mismatch for %s — an engine event/handler/usage contract drifted, or the golden is stale.\n"+
					"Regenerate with: go test ./cmd/tracker-conformance -run TestGoldenTraces -update-golden\n\n--- got ---\n%s\n--- want ---\n%s",
					name, got, want)
			}
		})
	}
}
