// ABOUTME: Assembles run.json — a single self-describing record of one run, derived from the activity log and checkpoint.
// ABOUTME: Runnable after the fact so a run that died without finalizing still gets one, and archived runs can be backfilled.
package pipeline

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// RunManifestFile is the manifest's name inside the run directory.
const RunManifestFile = "run.json"

// RunManifestSchemaVersion is bumped when the manifest's shape changes in a way
// a reader must notice. Readers should check it before trusting field
// semantics.
const RunManifestSchemaVersion = 1

// RunManifest is one run, described in a single readable document.
//
// Everything here is derivable from the run directory, which is the point: the
// assembler can run after the fact, so a run killed before it could finalize
// still gets a manifest, and runs archived before this existed can be
// backfilled. The alternative — deriving these facts at read time — means every
// consumer re-parses an activity log that can reach millions of lines to answer
// questions as basic as "did this finish".
type RunManifest struct {
	SchemaVersion int    `json:"schema_version"`
	RunID         string `json:"run_id"`

	// Goal is the workflow's stated intent, read from the checkpoint's
	// context ("graph.goal"). Declared by the spec, so it needs no inference.
	Goal string `json:"goal,omitempty"`

	// Spec records the specification that executed. Populated from the
	// manifest WriteSpecArtifacts returns, or read back from disk on backfill.
	Spec SpecManifest `json:"spec,omitempty"`

	StartedAt  string `json:"started_at,omitempty"`
	FinishedAt string `json:"finished_at,omitempty"`

	// TerminalStatus is how the run ended: "success",
	// "validation_overridden", "fail", "budget_exceeded", or empty when the
	// log carries no terminal event at all — which means the process died
	// before it could emit one, and is itself worth recording rather than
	// papering over.
	TerminalStatus string `json:"terminal_status,omitempty"`
	// ReachedNode is the last node the checkpoint recorded as current. Read
	// alongside the spec's configured exit node, this is what distinguishes a
	// run that finished from one that stopped early.
	ReachedNode string `json:"reached_node,omitempty"`
	// CompletedNodes is the checkpoint's completed set.
	CompletedNodes []string `json:"completed_nodes,omitempty"`
	RestartCount   int      `json:"restart_count,omitempty"`

	// Nodes is one entry per node the run actually started, keyed off
	// stage_started events rather than off which artifact directories exist —
	// directories are sparse in large runs, so enumerating by filesystem
	// silently drops nodes.
	Nodes []NodeSummary `json:"nodes,omitempty"`

	Totals    RunTotals      `json:"totals"`
	Human     HumanSummary   `json:"human"`
	ToolCalls map[string]int `json:"tool_calls,omitempty"`

	// EventCounts is every event type seen, with its count — a cheap shape
	// summary that answers "was there a retry storm" without a second pass.
	EventCounts map[string]int `json:"event_counts,omitempty"`

	// BundleIdentity is the .dipx identity, empty for a plain .dip run.
	BundleIdentity string `json:"bundle_identity,omitempty"`
}

// NodeSummary is what the log says about one node.
type NodeSummary struct {
	ID   string `json:"id"`
	Kind string `json:"kind,omitempty"`
	// Attempts is the highest attempt number observed, so a node that
	// succeeded first try reads 1.
	Attempts int    `json:"attempts"`
	Outcome  string `json:"outcome,omitempty"`
	// Turns counts turn_start events attributed to this node; 0 for tool and
	// human nodes, which run no agent turns.
	Turns int `json:"turns,omitempty"`
	// Failed is true when any stage_failed event named this node — recorded
	// separately from Outcome because a node can fail an attempt and then
	// succeed on retry.
	Failed bool `json:"failed,omitempty"`
}

// RunTotals is the run's aggregate economics.
//
// Derived by summing per-call usage grouped by call_id rather than by reading
// the cost_updated snapshots: those snapshots turned out to be absent from the
// overwhelming majority of archived runs, and both log paths record the same
// LLM call, so an ungrouped sum double-counts.
type RunTotals struct {
	InputTokens      int     `json:"input_tokens,omitempty"`
	OutputTokens     int     `json:"output_tokens,omitempty"`
	CacheReadTokens  int     `json:"cache_read_tokens,omitempty"`
	CacheWriteTokens int     `json:"cache_write_tokens,omitempty"`
	ReasoningTokens  int     `json:"reasoning_tokens,omitempty"`
	CostUSD          float64 `json:"cost_usd,omitempty"`
	// LLMCalls is the number of distinct call_ids seen.
	LLMCalls int `json:"llm_calls,omitempty"`
	// Estimated is true when any contributing figure was heuristic-derived
	// rather than metered (the ACP backend estimates from rune counts), so a
	// reader knows not to treat the cost as authoritative.
	Estimated bool `json:"estimated,omitempty"`
}

// HumanSummary counts the points where a person had to intervene. A run that
// needed steering did not run autonomously, which is worth knowing separately
// from whether it succeeded.
type HumanSummary struct {
	Gates    int `json:"gates,omitempty"`
	Steering int `json:"steering,omitempty"`
}

// AssembleRunManifest builds a manifest for the run in runDir by reading
// activity.jsonl, checkpoint.json, and any spec artifacts present.
//
// Tolerant by design: a missing checkpoint, an unparseable line, or a truncated
// log yields a partial manifest rather than an error, because the runs most
// worth describing are the ones that ended badly. A missing activity log is the
// one hard failure — without it there is nothing to describe.
func AssembleRunManifest(runDir, runID string) (RunManifest, error) {
	m := RunManifest{
		SchemaVersion: RunManifestSchemaVersion,
		RunID:         runID,
		ToolCalls:     map[string]int{},
		EventCounts:   map[string]int{},
	}

	entries, err := readActivityEntries(activityLogForRun(runDir, runID))
	if err != nil {
		return m, err
	}
	acc := newManifestAccumulator()
	for _, e := range entries {
		acc.add(e)
	}
	acc.finish(&m)

	applyCheckpointToManifest(&m, runDir)
	applySpecToManifest(&m, runDir)
	return m, nil
}

// WriteRunManifest assembles and writes run.json into runDir.
func WriteRunManifest(runDir, runID string) (RunManifest, error) {
	m, err := AssembleRunManifest(runDir, runID)
	if err != nil {
		return m, err
	}
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return m, fmt.Errorf("marshal manifest: %w", err)
	}
	path := filepath.Join(runDir, RunManifestFile)
	if err := os.WriteFile(path, append(data, '\n'), 0o644); err != nil {
		return m, fmt.Errorf("write %s: %w", RunManifestFile, err)
	}
	return m, nil
}

// activityLogForRun picks which copy of the log to read.
//
// The run directory holds a sentinel-stripped snapshot, but it is written at
// Close and is documented as best-effort — so it can be absent while the run is
// still open, or if the mirror failed. The secure path is the live log and the
// authoritative copy, so fall back to it. Both are handled by the reader, which
// strips the sentinel when present.
func activityLogForRun(runDir, runID string) string {
	snapshot := filepath.Join(runDir, "activity.jsonl")
	if _, err := os.Stat(snapshot); err == nil {
		return snapshot
	}
	if secure, err := SecureActivityLogPath(runID); err == nil {
		if _, err := os.Stat(secure); err == nil {
			return secure
		}
	}
	// Neither exists: return the snapshot path so the error names the location
	// a reader would expect to find it.
	return snapshot
}

// readActivityEntries decodes every parseable line of an activity log.
//
// Unparseable lines are skipped rather than fatal: a run killed mid-write
// leaves a partial final line, and that run is precisely the one a manifest is
// most useful for. Lines carry ActivityLogSentinel in the secure log and not in
// the artifact-dir snapshot, so the prefix is stripped when present.
func readActivityEntries(path string) ([]jsonlLogEntry, error) {
	f, err := os.Open(path) //nolint:gosec // path is composed from a validated run dir
	if err != nil {
		return nil, fmt.Errorf("open activity log: %w", err)
	}
	defer f.Close()

	var out []jsonlLogEntry
	sc := bufio.NewScanner(f)
	// Tool output and raw provider payloads make individual lines large; the
	// default 64KB token limit would truncate them into parse failures.
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for sc.Scan() {
		line := strings.TrimPrefix(sc.Text(), ActivityLogSentinel)
		if line == "" {
			continue
		}
		var entry jsonlLogEntry
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			continue
		}
		out = append(out, entry)
	}
	// A scan error (an over-long line) still leaves everything read so far
	// usable, so it is not surfaced as a failure.
	return out, nil
}

// manifestAccumulator folds activity entries into manifest state.
type manifestAccumulator struct {
	nodes       map[string]*NodeSummary
	nodeOrder   []string
	calls       map[string]RunTotals
	toolCalls   map[string]int
	eventCounts map[string]int
	firstTS     string
	lastTS      string
	terminal    string
	gates       int
	steering    int
	estimated   bool
}

func newManifestAccumulator() *manifestAccumulator {
	return &manifestAccumulator{
		nodes:       map[string]*NodeSummary{},
		calls:       map[string]RunTotals{},
		toolCalls:   map[string]int{},
		eventCounts: map[string]int{},
	}
}

// add folds one entry in.
func (a *manifestAccumulator) add(e jsonlLogEntry) {
	if e.Timestamp != "" {
		if a.firstTS == "" {
			a.firstTS = e.Timestamp
		}
		a.lastTS = e.Timestamp
	}
	a.eventCounts[e.Type]++
	if e.TerminalStatus != "" {
		a.terminal = e.TerminalStatus
	}
	if e.Estimated {
		a.estimated = true
	}
	a.addNode(e)
	a.addUsage(e)
	a.addHuman(e)
}

// addNode records per-node facts. Node identity comes from any event that
// names one, so a node reaches the manifest even when it produced no artifact
// directory.
func (a *manifestAccumulator) addNode(e jsonlLogEntry) {
	if e.NodeID == "" {
		return
	}
	n, ok := a.nodes[e.NodeID]
	if !ok {
		n = &NodeSummary{ID: e.NodeID, Attempts: 1}
		a.nodes[e.NodeID] = n
		a.nodeOrder = append(a.nodeOrder, e.NodeID)
	}
	if e.NodeKind != "" {
		n.Kind = e.NodeKind
	}
	if e.AttemptNo > n.Attempts {
		n.Attempts = e.AttemptNo
	}
	if e.OutcomeStatus != "" {
		n.Outcome = e.OutcomeStatus
	}
	a.addNodeByType(e, n)
}

// addNodeByType applies the per-event-type node updates. Split from addNode to
// keep each within the complexity ceiling.
func (a *manifestAccumulator) addNodeByType(e jsonlLogEntry, n *NodeSummary) {
	switch e.Type {
	case "turn_start":
		n.Turns++
	case "stage_failed":
		n.Failed = true
	case "tool_call_start":
		if e.ToolName != "" {
			a.toolCalls[e.ToolName]++
		}
	}
}

// addUsage records per-call usage keyed by call_id, so the two log paths that
// both report one call collapse instead of summing twice. Entries with no
// call_id are keyed by their own identity and still counted once.
func (a *manifestAccumulator) addUsage(e jsonlLogEntry) {
	if e.TokenInput == 0 && e.TokenOutput == 0 && e.EstimatedCost == 0 {
		return
	}
	key := e.CallID
	if key == "" {
		// No call id: fall back to a per-line key so pre-call-id logs still
		// aggregate, accepting that an overlapping pair counts twice there.
		key = e.Timestamp + "|" + e.Type + "|" + e.NodeID
	}
	// Last write wins for a given call: a finish event carries the
	// authoritative totals for that call, superseding partial figures.
	a.calls[key] = RunTotals{
		InputTokens:      e.TokenInput,
		OutputTokens:     e.TokenOutput,
		CacheReadTokens:  e.CacheReadTokens,
		CacheWriteTokens: e.CacheWriteTokens,
		ReasoningTokens:  e.ReasoningTokens,
		CostUSD:          e.EstimatedCost,
	}
}

// addHuman counts human intervention points.
func (a *manifestAccumulator) addHuman(e jsonlLogEntry) {
	switch e.Type {
	case "human_gate_opened", "human_gate_resolved":
		a.gates++
	case "steering_injected":
		a.steering++
	}
}

// finish writes accumulated state onto the manifest.
func (a *manifestAccumulator) finish(m *RunManifest) {
	m.StartedAt = a.firstTS
	m.FinishedAt = a.lastTS
	m.TerminalStatus = a.terminal
	m.Human = HumanSummary{Gates: a.gates, Steering: a.steering}
	m.EventCounts = a.eventCounts
	if len(a.toolCalls) > 0 {
		m.ToolCalls = a.toolCalls
	} else {
		m.ToolCalls = nil
	}

	m.Nodes = make([]NodeSummary, 0, len(a.nodeOrder))
	for _, id := range a.nodeOrder {
		m.Nodes = append(m.Nodes, *a.nodes[id])
	}

	totals := RunTotals{Estimated: a.estimated, LLMCalls: len(a.calls)}
	for _, c := range a.calls {
		totals.InputTokens += c.InputTokens
		totals.OutputTokens += c.OutputTokens
		totals.CacheReadTokens += c.CacheReadTokens
		totals.CacheWriteTokens += c.CacheWriteTokens
		totals.ReasoningTokens += c.ReasoningTokens
		totals.CostUSD += c.CostUSD
	}
	m.Totals = totals
}

// applyCheckpointToManifest folds in facts the checkpoint states directly
// rather than facts the log implies. Absent or unreadable checkpoints are
// skipped: an interrupted run may have none, and the rest of the manifest is
// still worth having.
func applyCheckpointToManifest(m *RunManifest, runDir string) {
	cp, err := LoadCheckpoint(filepath.Join(runDir, "checkpoint.json"))
	if err != nil || cp == nil {
		return
	}
	m.ReachedNode = cp.CurrentNode
	m.CompletedNodes = cp.CompletedNodes
	m.RestartCount = cp.RestartCount
	if cp.BundleIdentity != "" {
		m.BundleIdentity = cp.BundleIdentity
	}
	if goal := cp.Context["graph.goal"]; goal != "" {
		m.Goal = goal
	}
	// Nodes the checkpoint completed but that left no log line still belong in
	// the node table.
	m.Nodes = mergeCheckpointNodes(m.Nodes, cp.CompletedNodes)
}

// mergeCheckpointNodes appends completed nodes absent from the log-derived
// table. Enumerating from one source alone loses nodes: the log misses any node
// whose events were lost, and the artifact directory misses most nodes in a
// large run.
func mergeCheckpointNodes(nodes []NodeSummary, completed []string) []NodeSummary {
	seen := make(map[string]bool, len(nodes))
	for _, n := range nodes {
		seen[n.ID] = true
	}
	for _, id := range completed {
		if !seen[id] {
			nodes = append(nodes, NodeSummary{ID: id, Attempts: 1, Outcome: "success"})
			seen[id] = true
		}
	}
	return nodes
}

// applySpecToManifest reads back spec artifacts written by WriteSpecArtifacts.
// Only the digest and filenames are recorded — the manifest references the spec
// rather than inlining it, so it stays readable.
func applySpecToManifest(m *RunManifest, runDir string) {
	source, err := os.ReadFile(filepath.Join(runDir, SpecSourceFile)) //nolint:gosec // composed from a validated run dir
	if err == nil {
		m.Spec.SourceFile = SpecSourceFile
		m.Spec.SourceSHA256 = digest(source)
	}
	if _, err := os.Stat(filepath.Join(runDir, SpecIRFile)); err == nil {
		m.Spec.IRFile = SpecIRFile
	}
	m.Spec.Inputs = readSpecInputs(runDir)
	if m.Spec.BundleIdentity == "" {
		m.Spec.BundleIdentity = m.BundleIdentity
	}
	if m.Goal == "" {
		m.Goal = goalFromIR(runDir)
	}
}

// goalFromIR reads the workflow goal out of the stored IR.
//
// The checkpoint is the other source, but it is a side effect of node
// transitions: a single-node run never writes one, so a goal read only from
// there goes missing on exactly the smallest pipelines. The spec declares the
// goal outright, which makes the IR the more reliable source and the checkpoint
// the fallback.
func goalFromIR(runDir string) string {
	data, err := os.ReadFile(filepath.Join(runDir, SpecIRFile)) //nolint:gosec // composed from a validated run dir
	if err != nil {
		return ""
	}
	var ir struct {
		Goal string `json:"goal"`
	}
	if err := json.Unmarshal(data, &ir); err != nil {
		return ""
	}
	return ir.Goal
}

// readSpecInputs lists stored input documents by digest.
func readSpecInputs(runDir string) []SpecInput {
	entries, err := os.ReadDir(filepath.Join(runDir, SpecInputsDir))
	if err != nil {
		return nil
	}
	out := make([]SpecInput, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		ext := filepath.Ext(name)
		in := SpecInput{
			Name:   name,
			SHA256: strings.TrimSuffix(name, ext),
			Ext:    ext,
		}
		if info, err := e.Info(); err == nil {
			in.Size = int(info.Size())
		}
		out = append(out, in)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].SHA256 < out[j].SHA256 })
	if len(out) == 0 {
		return nil
	}
	return out
}
