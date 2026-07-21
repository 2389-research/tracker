// ABOUTME: Self-test: a minimal reference interviewer proves the suite is
// transport-neutral (it depends on no transport, only the interviewer contract).
package conformance

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/2389-research/tracker/pipeline/handlers"
)

var errRefCanceled = errors.New("canceled")

// refGate is one posted gate awaiting a reply.
type refGate struct {
	prompt string
	reply  chan string
}

// refInterviewer is the smallest correct interviewer: each Ask* posts a gate and
// blocks until answered or canceled. It exists only to self-test the suite.
type refInterviewer struct {
	gates    chan *refGate
	canceled chan struct{}
	once     sync.Once
}

func newRef() *refInterviewer {
	return &refInterviewer{gates: make(chan *refGate, 8), canceled: make(chan struct{})}
}

var (
	_ handlers.Interviewer                = (*refInterviewer)(nil)
	_ handlers.FreeformInterviewer        = (*refInterviewer)(nil)
	_ handlers.LabeledFreeformInterviewer = (*refInterviewer)(nil)
	_ handlers.InterviewInterviewer       = (*refInterviewer)(nil)
)

func (r *refInterviewer) ask(prompt string) (string, error) {
	g := &refGate{prompt: prompt, reply: make(chan string, 1)}
	r.gates <- g
	select {
	case v := <-g.reply:
		return v, nil
	case <-r.canceled:
		return "", errRefCanceled
	}
}

func (r *refInterviewer) Ask(prompt string, _ []string, _ string) (string, error) {
	return r.ask(prompt)
}
func (r *refInterviewer) AskFreeform(prompt string) (string, error) { return r.ask(prompt) }
func (r *refInterviewer) AskFreeformWithLabels(prompt string, _ []string, _ string) (string, error) {
	return r.ask(prompt)
}

func (r *refInterviewer) AskInterview(qs []handlers.Question, _ *handlers.InterviewResult) (*handlers.InterviewResult, error) {
	res := &handlers.InterviewResult{}
	for _, q := range qs {
		a, err := r.ask(q.Text)
		if errors.Is(err, errRefCanceled) {
			res.Canceled = true
			return res, nil
		}
		if err != nil {
			return nil, err
		}
		res.Questions = append(res.Questions, handlers.InterviewAnswer{Text: q.Text, Answer: a})
	}
	return res, nil
}

func (r *refInterviewer) Cancel() { r.once.Do(func() { close(r.canceled) }) }

// TestReferenceInterviewer_Conformance runs the suite against the reference
// implementation, proving the suite depends only on the interviewer contract.
func TestReferenceInterviewer_Conformance(t *testing.T) {
	RunInterviewerSuite(t, func() Subject {
		r := newRef()
		return Subject{
			Interviewer: r,
			Answer: func(t *testing.T, reply string) string {
				select {
				case g := <-r.gates:
					g.reply <- reply
					return g.prompt
				case <-time.After(2 * time.Second):
					t.Fatal("reference interviewer posted no gate")
					return ""
				}
			},
			Cancel: r.Cancel,
		}
	})
}
