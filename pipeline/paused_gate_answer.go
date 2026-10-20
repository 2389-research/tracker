// ABOUTME: The `tracker answer` command line for a paused run's waiting gates —
// ABOUTME: one source for the gate_paused event and the CLI summary.
package pipeline

import (
	"regexp"
	"strings"
)

// GateName is a waiting gate's name for `tracker answer`: its node ID,
// prefixed by its scope path when it has one ("B1/Review").
func GateName(g PausedGate) string {
	return strings.Join(append(append([]string(nil), g.ScopePath...), g.NodeID), "/")
}

// AnswerCommand is the `tracker answer` command that answers gates, the
// waiting gates of run runID; flags (e.g. "-w /x") go before the run ID. One
// gate gets its default (or first) answer with the alternatives as a shell
// comment; several gates get one --answer <gate>=<answer> each, since they
// must be answered together.
func AnswerCommand(runID, flags string, gates []PausedGate) string {
	parts := []string{"tracker answer"}
	if flags != "" {
		parts = append(parts, flags)
	}
	parts = append(parts, runID)
	if len(gates) == 1 {
		return strings.Join(parts, " ") + singleAnswerSuffix(gates[0])
	}
	for _, g := range gates {
		parts = append(parts, "--answer", shellArg(GateName(g)+"="+suggestedAnswer(g)))
	}
	return strings.Join(parts, " ")
}

// suggestedAnswer is the answer the command proposes for g.
func suggestedAnswer(g PausedGate) string {
	switch {
	case g.Default != "":
		return g.Default
	case len(g.Choices) > 0:
		return g.Choices[0]
	default:
		return "your answer"
	}
}

func singleAnswerSuffix(g PausedGate) string {
	switch {
	case g.Mode == "interview":
		return ` --answers-file answers.json   # {"answers": {"q1": "..."}}`
	case len(g.Choices) == 0:
		return ` --text "your answer"`
	}
	first := suggestedAnswer(g)
	var others []string
	for _, c := range g.Choices {
		if c != first {
			others = append(others, c)
		}
	}
	if g.Mode == "freeform" {
		others = append(others, `--text "free text"`)
	}
	if len(others) == 0 {
		return " " + shellArg(first)
	}
	return " " + shellArg(first) + "   # or: " + strings.Join(others, " / ")
}

var shellSafeArg = regexp.MustCompile(`^[A-Za-z0-9_./:=@%+,-]+$`)

// shellArg single-quotes s for a POSIX shell unless it is safe as is.
func shellArg(s string) string {
	if shellSafeArg.MatchString(s) {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
