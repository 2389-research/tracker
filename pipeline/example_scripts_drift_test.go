package pipeline_test

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// exampleScriptCopySets lists example workflow scripts that are shipped as
// byte-identical copies under several workflow directories (each .dip
// references its own scripts/<workflow>/ sidecar dir, and only build_product
// is embedded). A fix landed in one copy must land in all of them — #646
// item 4 found the dotpowers-family task-id bug in seven places. The logic
// for the dotpowers family now lives once in scripts/dotpowers/lib/ (sourced
// via ${graph.workflow_dir}), so these copies are thin wrappers; this test
// keeps the wrappers (and the still-duplicated helpers) in lockstep.
var exampleScriptCopySets = []struct {
	name    string
	dirs    []string
	scripts []string
}{
	{
		name: "dotpowers family",
		dirs: []string{"dotpowers", "dotpowers-auto", "dotpowers-simple", "dotpowers-simple-auto",
			"test-kitchen", "scenario-testing", "kitchen-sink"},
		scripts: []string{"PickNextTask.sh", "MarkTaskComplete.sh", "ValidateBuild.sh", "VerifyTestsFinal.sh",
			"CheckImplementBudget.sh", "CheckReworkBudget.sh", "ValidatePlanFormat.sh",
			"VerifyBaseline.sh", "CheckExistingPlans.sh"},
	},
	{
		name:    "megaplan",
		dirs:    []string{"megaplan", "megaplan_quality"},
		scripts: []string{"DetermineSprintId.sh", "SyncLedger.sh", "PersistSprintPlaceholder.sh", "SetupEnvironment.sh"},
	},
	{
		// #656: FinalCommit.sh is the deterministic final committer, identical
		// across the two build_product variants (self-contained, not in lib/,
		// so pinned here). Fix once, copy to both.
		name:    "build_product FinalCommit",
		dirs:    []string{"build_product", "build_product_with_superspec"},
		scripts: []string{"FinalCommit.sh"},
	},
	{
		name:    "ralph-loop",
		dirs:    []string{"ralph-loop", "fix-tracker-visibility"},
		scripts: []string{"CheckCompletion.sh", "IncrementCounter.sh"},
	},
}

func TestExampleScriptCopiesStayIdentical(t *testing.T) {
	root := filepath.Join("..", "examples", "scripts")
	for _, set := range exampleScriptCopySets {
		for _, script := range set.scripts {
			refPath := filepath.Join(root, set.dirs[0], script)
			ref, err := os.ReadFile(refPath)
			if err != nil {
				t.Fatalf("%s: %v", set.name, err)
			}
			for _, dir := range set.dirs[1:] {
				path := filepath.Join(root, dir, script)
				got, err := os.ReadFile(path)
				if err != nil {
					t.Errorf("%s: %v", set.name, err)
					continue
				}
				if !bytes.Equal(ref, got) {
					t.Errorf("%s: %s drifted from %s — the copies must stay byte-identical (fix once, copy to every dir)", set.name, path, refPath)
				}
			}
		}
	}
}
