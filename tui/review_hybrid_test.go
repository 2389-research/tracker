// ABOUTME: Tests for ReviewHybridContent — scrollable review context with radio labels and "other".
// ABOUTME: Also pins HybridContent's key handling where the two gates differ (ctrl+s on "other").
package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// expectGateReply fails unless the gate sent want on ch.
func expectGateReply(t *testing.T, ch chan string, want string) {
	t.Helper()
	select {
	case got, ok := <-ch:
		if !ok {
			t.Fatalf("expected %q, reply channel was closed", want)
		}
		if got != want {
			t.Errorf("expected %q, got %q", want, got)
		}
	default:
		t.Fatalf("expected %q on reply channel", want)
	}
}

// expectNoGateReply fails if the gate sent a value or closed ch.
func expectNoGateReply(t *testing.T, ch chan string) {
	t.Helper()
	select {
	case got, ok := <-ch:
		t.Fatalf("expected no reply, got %q (open=%v)", got, ok)
	default:
	}
}

func typeRunes(m ModalContent, s string) {
	for _, r := range s {
		m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
}

func newTestReviewHybrid(ch chan string, defaultLabel string) *ReviewHybridContent {
	return NewReviewHybridContent("Escalation", "Build failed:\n\nexit status 1",
		[]string{"accept", "retry", "abandon"}, defaultLabel, ch, 80, 30)
}

func TestReviewHybridContentSelectLabel(t *testing.T) {
	ch := make(chan string, 1)
	r := newTestReviewHybrid(ch, "")
	r.Update(tea.KeyMsg{Type: tea.KeyDown})
	r.Update(tea.KeyMsg{Type: tea.KeyEnter})
	expectGateReply(t, ch, "retry")
}

func TestReviewHybridContentCtrlSSubmitsLabel(t *testing.T) {
	ch := make(chan string, 1)
	r := newTestReviewHybrid(ch, "")
	r.Update(tea.KeyMsg{Type: tea.KeyDown})
	r.Update(tea.KeyMsg{Type: tea.KeyDown})
	r.Update(tea.KeyMsg{Type: tea.KeyCtrlS})
	expectGateReply(t, ch, "abandon")
}

func TestReviewHybridContentSelectDefaultIgnoresCase(t *testing.T) {
	ch := make(chan string, 1)
	r := newTestReviewHybrid(ch, "RETRY")
	r.Update(tea.KeyMsg{Type: tea.KeyEnter})
	expectGateReply(t, ch, "retry")
}

func TestReviewHybridContentCursorStopsAtEnds(t *testing.T) {
	ch := make(chan string, 1)
	r := newTestReviewHybrid(ch, "")
	r.Update(tea.KeyMsg{Type: tea.KeyUp})
	if r.cursor != 0 {
		t.Errorf("up at the top moved the cursor to %d", r.cursor)
	}
	for range 10 {
		r.Update(tea.KeyMsg{Type: tea.KeyDown})
	}
	if r.cursor != 3 {
		t.Errorf("expected the cursor to stop on \"other\" (3), got %d", r.cursor)
	}
}

func TestReviewHybridContentOtherSubmitsTypedText(t *testing.T) {
	ch := make(chan string, 1)
	r := newTestReviewHybrid(ch, "")
	for range 3 {
		r.Update(tea.KeyMsg{Type: tea.KeyDown})
	}
	r.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if !r.onOther {
		t.Fatal("expected enter on \"other\" to focus the textarea")
	}
	typeRunes(r, "rerun with -v")
	r.Update(tea.KeyMsg{Type: tea.KeyCtrlS})
	expectGateReply(t, ch, "rerun with -v")
}

func TestReviewHybridContentCtrlSOnOtherFocusesTextarea(t *testing.T) {
	ch := make(chan string, 1)
	r := newTestReviewHybrid(ch, "")
	for range 3 {
		r.Update(tea.KeyMsg{Type: tea.KeyDown})
	}
	r.Update(tea.KeyMsg{Type: tea.KeyCtrlS})
	if !r.onOther {
		t.Error("expected ctrl+s on \"other\" to focus the textarea")
	}
	expectNoGateReply(t, ch)
}

func TestReviewHybridContentEmptyOtherNotSubmitted(t *testing.T) {
	ch := make(chan string, 1)
	r := newTestReviewHybrid(ch, "")
	for range 3 {
		r.Update(tea.KeyMsg{Type: tea.KeyDown})
	}
	r.Update(tea.KeyMsg{Type: tea.KeyEnter})
	typeRunes(r, "   ")
	r.Update(tea.KeyMsg{Type: tea.KeyCtrlS})
	expectNoGateReply(t, ch)
	if r.done || !r.onOther {
		t.Errorf("blank feedback should leave the textarea open (done=%v onOther=%v)", r.done, r.onOther)
	}
}

func TestReviewHybridContentOtherModeReturnsToOptions(t *testing.T) {
	for _, key := range []tea.KeyMsg{{Type: tea.KeyEscape}, {Type: tea.KeyUp}} {
		t.Run(key.String(), func(t *testing.T) {
			ch := make(chan string, 1)
			r := newTestReviewHybrid(ch, "")
			for range 3 {
				r.Update(tea.KeyMsg{Type: tea.KeyDown})
			}
			r.Update(tea.KeyMsg{Type: tea.KeyEnter})
			r.Update(key)
			if r.onOther {
				t.Errorf("%s should leave the textarea", key)
			}
			if r.cursor != 3 {
				t.Errorf("%s should keep the cursor on \"other\" (3), got %d", key, r.cursor)
			}
			expectNoGateReply(t, ch)
		})
	}
}

func TestReviewHybridContentPageKeysDoNotSubmit(t *testing.T) {
	ch := make(chan string, 1)
	r := newTestReviewHybrid(ch, "")
	r.Update(tea.KeyMsg{Type: tea.KeyPgDown})
	r.Update(tea.KeyMsg{Type: tea.KeyPgUp})
	expectNoGateReply(t, ch)
	if r.cursor != 0 || r.done {
		t.Errorf("page keys moved the cursor or answered the gate (cursor=%d done=%v)", r.cursor, r.done)
	}
}

func TestReviewHybridContentEscCancelsGate(t *testing.T) {
	ch := make(chan string, 1)
	r := newTestReviewHybrid(ch, "")
	r.Update(tea.KeyMsg{Type: tea.KeyEscape})
	if _, ok := <-ch; ok {
		t.Error("expected channel to be closed on cancel")
	}
}

func TestReviewHybridContentCancelIsIdempotent(t *testing.T) {
	ch := make(chan string, 1)
	r := newTestReviewHybrid(ch, "")
	r.Cancel()
	r.Cancel() // a second close would panic
	if _, ok := <-ch; ok {
		t.Error("expected channel to be closed on cancel")
	}
}

func TestReviewHybridContentIgnoresKeysAfterAnswer(t *testing.T) {
	ch := make(chan string, 1)
	r := newTestReviewHybrid(ch, "")
	r.Update(tea.KeyMsg{Type: tea.KeyEnter})
	expectGateReply(t, ch, "accept")
	r.Update(tea.KeyMsg{Type: tea.KeyEscape})
	r.Cancel()
	expectNoGateReply(t, ch)
}

func TestReviewHybridContentViewRendersContextAndOptions(t *testing.T) {
	r := newTestReviewHybrid(nil, "")
	view := stripAnsi(r.View())
	for _, want := range []string{"exit status 1", "Review (", "accept", "retry", "abandon", "other (provide feedback)", "pgup/pgdn scroll"} {
		if !strings.Contains(view, want) {
			t.Errorf("expected %q in view", want)
		}
	}
}

func TestReviewHybridContentViewShowsTextareaHint(t *testing.T) {
	r := newTestReviewHybrid(nil, "")
	for range 3 {
		r.Update(tea.KeyMsg{Type: tea.KeyDown})
	}
	r.Update(tea.KeyMsg{Type: tea.KeyEnter})
	view := r.View()
	for _, want := range []string{"other:", "esc back to options"} {
		if !strings.Contains(view, want) {
			t.Errorf("expected %q in view", want)
		}
	}
}

func TestHybridContentCtrlSOnOtherSubmitsTypedText(t *testing.T) {
	ch := make(chan string, 1)
	h := NewHybridContent("Pick", []string{"approve"}, "", ch)
	h.Update(tea.KeyMsg{Type: tea.KeyDown})
	h.Update(tea.KeyMsg{Type: tea.KeyEnter})
	typeRunes(h, "tighten scope")
	h.Update(tea.KeyMsg{Type: tea.KeyEscape})
	if h.onOther {
		t.Fatal("esc should leave the textarea")
	}
	// Back on the radio list with the cursor on "other": ctrl+s sends the kept text.
	h.Update(tea.KeyMsg{Type: tea.KeyCtrlS})
	expectGateReply(t, ch, "tighten scope")
}

func TestHybridContentEmptyOtherNotSubmitted(t *testing.T) {
	ch := make(chan string, 1)
	h := NewHybridContent("Pick", []string{"approve"}, "", ch)
	h.Update(tea.KeyMsg{Type: tea.KeyDown})
	h.Update(tea.KeyMsg{Type: tea.KeyCtrlS})
	expectNoGateReply(t, ch)
	if h.done {
		t.Error("blank feedback should not answer the gate")
	}
}

func TestHybridContentUpLeavesTextareaOnOther(t *testing.T) {
	ch := make(chan string, 1)
	h := NewHybridContent("Pick", []string{"approve", "reject"}, "", ch)
	h.Update(tea.KeyMsg{Type: tea.KeyDown})
	h.Update(tea.KeyMsg{Type: tea.KeyDown})
	h.Update(tea.KeyMsg{Type: tea.KeyEnter})
	h.Update(tea.KeyMsg{Type: tea.KeyUp})
	if h.onOther || h.cursor != 2 {
		t.Errorf("up should leave the textarea with the cursor on \"other\" (onOther=%v cursor=%d)", h.onOther, h.cursor)
	}
	h.Update(tea.KeyMsg{Type: tea.KeyUp})
	h.Update(tea.KeyMsg{Type: tea.KeyEnter})
	expectGateReply(t, ch, "reject")
}

func TestHybridContentIgnoresKeysAfterAnswer(t *testing.T) {
	ch := make(chan string, 1)
	h := NewHybridContent("Pick", []string{"approve"}, "", ch)
	h.Update(tea.KeyMsg{Type: tea.KeyEnter})
	expectGateReply(t, ch, "approve")
	h.Update(tea.KeyMsg{Type: tea.KeyEscape})
	h.Cancel()
	expectNoGateReply(t, ch)
}

func TestHybridContentDefaultIgnoresCase(t *testing.T) {
	ch := make(chan string, 1)
	h := NewHybridContent("Pick", []string{"approve", "reject"}, "Reject", ch)
	h.Update(tea.KeyMsg{Type: tea.KeyEnter})
	expectGateReply(t, ch, "reject")
}
