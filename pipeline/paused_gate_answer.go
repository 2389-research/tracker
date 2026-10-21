// ABOUTME: The `tracker answer` command line for a paused run's waiting gates —
// ABOUTME: one source for the gate_paused event and the CLI summary.
package pipeline

import (
	"path/filepath"
	"regexp"
	"strings"
)

// GateName is a waiting gate's name for `tracker answer`: its node ID,
// prefixed by its scope path when it has one ("B1/Review").
func GateName(g PausedGate) string {
	return strings.Join(append(append([]string(nil), g.ScopePath...), g.NodeID), "/")
}

// AnswerCommand is the `tracker answer` command that answers gates, the
// waiting gates of run runID; flags (e.g. "-w /x") go before the run ID. It is
// a plain command line (no shell comment), so an agent can run it as is. One
// gate gets its default (or first) answer — AnswerAlternatives lists the
// others; several gates get one --answer <gate>=<answer> each, since they
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
		return " --answers-file answers.json"
	case len(g.Choices) == 0:
		return ` --text "your answer"`
	}
	return " " + shellArg(suggestedAnswer(g))
}

// AnswerAlternatives lists the other answers a single waiting gate takes,
// for a hint next to AnswerCommand ("approve / --text \"free text\""); ""
// when there are none or several gates wait.
func AnswerAlternatives(gates []PausedGate) string {
	if len(gates) != 1 {
		return ""
	}
	g := gates[0]
	if g.Mode == "interview" {
		return `answers.json is {"answers": {"<question id>": "<answer>"}}`
	}
	first := suggestedAnswer(g)
	var others []string
	for _, c := range g.Choices {
		if c != first {
			others = append(others, c)
		}
	}
	if g.Mode == "freeform" && len(g.Choices) > 0 {
		others = append(others, `--text "free text"`)
	}
	return strings.Join(others, " / ")
}

var shellSafeArg = regexp.MustCompile(`^[A-Za-z0-9_./:=@%+,-]+$`)

// shellArg single-quotes s for a POSIX shell unless it is safe as is.
func shellArg(s string) string {
	if shellSafeArg.MatchString(s) {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// AnswerDirFlags returns the -w / --artifact-dir flags for AnswerCommand that
// locate a run started in workDir with artifactDir (--artifact-dir only when it
// is not the default <workDir>/.tracker/runs), absolute and shell-quoted.
func AnswerDirFlags(workDir, artifactDir string) string {
	if workDir == "" {
		return ""
	}
	work := absPath(workDir)
	flags := "-w " + shellArg(work)
	if artifactDir != "" && absPath(artifactDir) != filepath.Join(work, ".tracker", "runs") {
		flags += " --artifact-dir " + shellArg(absPath(artifactDir))
	}
	return flags
}

func absPath(p string) string {
	if abs, err := filepath.Abs(p); err == nil {
		return abs
	}
	return p
}
