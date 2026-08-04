// ABOUTME: The operator-facing warning for a run that consumed usage under an
// ABOUTME: uncatalogued model while a --max-cost ceiling was configured (#518).
package pipeline

import (
	"fmt"
	"strings"

	"github.com/2389-research/tracker/internal/diag"
)

// UnpricedBudgetWarning returns the message to show an operator when a run
// attributed billable usage to a model with no catalog entry (RunTotals.Unpriced)
// while a --max-cost ceiling was configured. Empty when there is nothing to
// warn about — priced usage, or unpriced usage with no cost ceiling to bypass.
//
// DECISION (#518): unpriced usage is a SIGNAL, not a hard failure. A genuinely
// free local model (Ollama via openaicompat) legitimately costs $0 and must
// keep running, and there is no way to tell it apart from an uncatalogued paid
// model except by adding the paid model to the catalog. So the ceiling is not
// tripped and the run is not stopped; the operator is instead told that the
// ceiling could not bound this usage, so a real overspend under a misspelled or
// missing model is visible rather than silent. Returning a string (not an
// error) keeps this decision structural: there is no failure path to take.
func UnpricedBudgetWarning(totals RunTotals, costLimited bool) string {
	if !totals.Unpriced || !costLimited {
		return ""
	}
	models := "an uncatalogued model"
	if len(totals.UnpricedModels) > 0 {
		models = strings.Join(totals.UnpricedModels, ", ")
	}
	return fmt.Sprintf(
		"unpriced usage under a --max-cost ceiling: %s has no catalog entry, so its cost was estimated as $0 and the ceiling could not bound it; add it to the model catalog to price this run",
		models,
	)
}

// WarnUnpricedBudget emits UnpricedBudgetWarning to the diagnostic log when it is
// non-empty. It never returns an error and never halts the run — see the
// decision recorded on UnpricedBudgetWarning.
func WarnUnpricedBudget(totals RunTotals, costLimited bool) {
	if msg := UnpricedBudgetWarning(totals, costLimited); msg != "" {
		diag.Warnf("warning: %s", msg)
	}
}
