// ABOUTME: A terminal REPL transport — a second consumer of transport/chatops that
// ABOUTME: proves the boundary: type a request, answer gates inline, watch it run.
//
// This is a first-class peer to the Slack bot, built from the same core: a
// chatops.ThreadUI for output + gate rendering, and an inbound loop that routes
// each stdin line to the Runner (a fresh request, or an answer to a pending
// gate). It needs no external service, so it's exercised end-to-end in tests via
// the Dispatcher seam.
package cli

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"

	chatops "github.com/2389-research/tracker/transport/chatops"
)

// The REPL is one conversation, so it uses one fixed thread/channel identity —
// the Runner keys every run and command on it, exactly as Slack keys on thread_ts.
const (
	Thread  = "cli"
	Channel = "cli"
)

// Dispatcher is the slice of *chatops.Runner the REPL drives. Narrowing to an
// interface keeps the loop testable with a fake — no LLM or pipeline needed.
type Dispatcher interface {
	OnMention(ctx context.Context, channel, threadTS, text string)
	OnInteraction(threadTS, gateID string, answer chatops.GateAnswer) bool
}

// Session holds the terminal transport's output writer and the single pending
// gate (the one the next input line answers). Bind its ThreadUI into
// RunnerDeps.NewThreadUI, then call Run with the Runner as the Dispatcher.
type Session struct {
	out io.Writer

	mu      sync.Mutex
	pending *chatops.Gate
}

// NewSession returns a REPL session writing to out (usually os.Stdout).
func NewSession(out io.Writer) *Session { return &Session{out: out} }

// ThreadUI returns the session's ThreadUI. Wire it as RunnerDeps.NewThreadUI;
// the REPL is single-thread, so every (channel, thread) maps to this one UI.
func (s *Session) ThreadUI(_ /*channel*/, _ /*threadTS*/ string) chatops.ThreadUI {
	return cliUI{s: s}
}

// Run reads input lines and routes each: an answer to a pending gate, a control
// line (/quit, /exit), or a fresh request. Requests dispatch on a goroutine so
// the loop stays free to read the gate answers a run will ask for — mirroring
// the Slack event loop's non-blocking mention handling. Returns when input ends
// or the user quits.
func (s *Session) Run(ctx context.Context, in io.Reader, disp Dispatcher) error {
	s.println("trackerchat — type a request (e.g. `make me a CLI that greets people`), or /quit.")
	sc := bufio.NewScanner(in)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024) // allow long pasted prompts
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		if line == "/quit" || line == "/exit" {
			return nil
		}
		if g := s.takePending(); g != nil {
			disp.OnInteraction(Thread, g.ID, mapGateAnswer(*g, line))
			continue
		}
		go disp.OnMention(ctx, Channel, Thread, line)
	}
	return sc.Err()
}

func (s *Session) println(text string) { fmt.Fprintln(s.out, text) }

func (s *Session) setPending(g chatops.Gate) {
	s.mu.Lock()
	s.pending = &g
	s.mu.Unlock()
}

// takePending returns and clears the pending gate (nil if none), so a line
// answers at most one gate and a stale gate can't capture a later request.
func (s *Session) takePending() *chatops.Gate {
	s.mu.Lock()
	defer s.mu.Unlock()
	g := s.pending
	s.pending = nil
	return g
}

// clearPending drops the pending gate only if it still points at gateID, so the
// interviewer cancelling an old gate can't clobber a newer one.
func (s *Session) clearPending(gateID string) {
	s.mu.Lock()
	if s.pending != nil && s.pending.ID == gateID {
		s.pending = nil
	}
	s.mu.Unlock()
}

// cliUI is the session's chatops.ThreadUI: prints messages and renders gates to
// the terminal, arming the session's pending-gate slot. It also implements
// chatops.PendingClearer so a cancelled gate releases the slot.
type cliUI struct{ s *Session }

func (u cliUI) Post(text string) error {
	u.s.println(text)
	return nil
}

func (u cliUI) PostGate(g chatops.Gate) error {
	u.s.setPending(g)
	u.s.println(renderGate(g))
	return nil
}

func (u cliUI) ClearPending(gateID string) { u.s.clearPending(gateID) }

// renderGate formats a gate for the terminal: a numbered list for choices (with
// a [default] marker), or a reply hint for freeform.
func renderGate(g chatops.Gate) string {
	var b strings.Builder
	if g.Kind == chatops.GateFreeform {
		fmt.Fprintf(&b, "✍️  %s\n   (type your answer)", g.Prompt)
		return b.String()
	}
	fmt.Fprintf(&b, "❓ %s", g.Prompt)
	for i, c := range g.Choices {
		marker := ""
		if c == g.Default {
			marker = "  [default]"
		}
		fmt.Fprintf(&b, "\n   %d) %s%s", i+1, c, marker)
	}
	b.WriteString("\n   (reply with a number, a label, or your own text)")
	return b.String()
}

// mapGateAnswer turns a typed line into a GateAnswer for the gate kind: a number
// or label selects a choice; anything else (or any freeform gate) is the
// free-text answer / "other" escape.
func mapGateAnswer(g chatops.Gate, line string) chatops.GateAnswer {
	if g.Kind == chatops.GateFreeform {
		return chatops.GateAnswer{Freeform: line}
	}
	if n, err := strconv.Atoi(line); err == nil && n >= 1 && n <= len(g.Choices) {
		return chatops.GateAnswer{Choice: g.Choices[n-1]}
	}
	for _, c := range g.Choices {
		if strings.EqualFold(c, line) {
			return chatops.GateAnswer{Choice: c}
		}
	}
	// Unmatched on a choice gate → the custom "other" answer (the hybrid modal's
	// freeform escape), so a user is never trapped among fixed labels.
	return chatops.GateAnswer{Freeform: line}
}
