// ABOUTME: Fullscreen multi-field interview form modal for interview-mode human gates.
// ABOUTME: Renders questions as radio selects, yes/no toggles, or textareas with pagination.
package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/2389-research/tracker/pipeline/handlers"
)

// interviewField holds the UI state for a single interview question.
type interviewField struct {
	question handlers.Question

	// Select fields (question has Options)
	selectCursor int            // index into options; len(options) = "Other"
	isOther      bool           // true when "Other" textarea is focused
	otherInput   textarea.Model // for custom "Other" text

	// Confirm fields (question IsYesNo)
	confirmed *bool // nil = unanswered, true/false = answered

	// Text fields (all other questions)
	textInput textarea.Model

	// Common
	elaboration textarea.Model // "Add details" textarea for select/confirm questions
	editingElab bool           // true when elaboration textarea is focused
}

// InterviewContent implements ModalContent, Cancellable, and FullscreenContent
// for multi-field interview forms shown in the TUI modal.
type InterviewContent struct {
	questions []handlers.Question
	fields    []interviewField
	cursor    int          // which question is focused (0-indexed into full list)
	page      int          // current page (0-indexed)
	pageSize  int          // questions per page (default 10)
	replyCh   chan<- string // JSON string reply
	done      bool
	width     int
	height    int
	inTextarea bool // true when a textarea has focus (text field, other input, or elaboration)
}

// IsFullscreen signals the modal to use the full terminal.
func (ic *InterviewContent) IsFullscreen() bool { return true }

// SetSize updates dimensions.
func (ic *InterviewContent) SetSize(w, h int) {
	ic.width = w
	ic.height = h
	taWidth := w - 8
	if taWidth < 20 {
		taWidth = 20
	}
	for i := range ic.fields {
		ic.fields[i].textInput.SetWidth(taWidth)
		ic.fields[i].otherInput.SetWidth(taWidth)
		ic.fields[i].elaboration.SetWidth(taWidth)
	}
}

// NewInterviewContent creates a fullscreen interview form.
// If previous is non-nil, fields are pre-filled from matching answers by ID.
func NewInterviewContent(questions []handlers.Question, previous *handlers.InterviewResult, replyCh chan<- string, width, height int) *InterviewContent {
	if width < 40 {
		width = 80
	}
	if height < 10 {
		height = 24
	}

	taWidth := width - 8
	if taWidth < 20 {
		taWidth = 20
	}

	fields := make([]interviewField, len(questions))
	for i, q := range questions {
		fields[i] = interviewField{
			question:    q,
			otherInput:  makeTextarea(taWidth, "Type custom answer..."),
			textInput:   makeTextarea(taWidth, "Type your answer..."),
			elaboration: makeTextarea(taWidth, "Add details (optional)..."),
		}
	}

	ic := &InterviewContent{
		questions: questions,
		fields:    fields,
		pageSize:  10,
		replyCh:   replyCh,
		width:     width,
		height:    height,
	}

	// Pre-fill from previous result if provided.
	if previous != nil {
		ic.prefill(previous)
	}

	return ic
}

// makeTextarea creates a textarea configured for interview fields.
func makeTextarea(width int, placeholder string) textarea.Model {
	ta := textarea.New()
	ta.ShowLineNumbers = false
	ta.Prompt = ""
	ta.Placeholder = placeholder
	ta.SetWidth(width)
	ta.SetHeight(3)
	ta.MaxHeight = 6
	ta.CharLimit = 0
	ta.FocusedStyle.CursorLine = lipgloss.NewStyle()
	ta.FocusedStyle.Base = lipgloss.NewStyle().BorderForeground(ColorLabel)
	ta.BlurredStyle.Base = ta.FocusedStyle.Base
	ta.Blur()
	return ta
}

// prefill populates fields from a previous InterviewResult, matching by ID.
func (ic *InterviewContent) prefill(prev *handlers.InterviewResult) {
	// Build lookup by ID.
	byID := make(map[string]handlers.InterviewAnswer, len(prev.Questions))
	for _, a := range prev.Questions {
		byID[a.ID] = a
	}

	for i, f := range ic.fields {
		id := fmt.Sprintf("q%d", f.question.Index)
		ans, ok := byID[id]
		if !ok {
			continue
		}

		if len(f.question.Options) > 0 {
			// Select field: find matching option or set as "Other".
			matched := false
			for j, opt := range f.question.Options {
				if strings.EqualFold(opt, ans.Answer) {
					ic.fields[i].selectCursor = j
					matched = true
					break
				}
			}
			if !matched && ans.Answer != "" {
				// Set as "Other"
				ic.fields[i].selectCursor = len(f.question.Options)
				ic.fields[i].isOther = true
				ic.fields[i].otherInput.SetValue(ans.Answer)
			}
			if ans.Elaboration != "" {
				ic.fields[i].elaboration.SetValue(ans.Elaboration)
			}
		} else if f.question.IsYesNo {
			// Confirm field.
			switch strings.ToLower(ans.Answer) {
			case "yes":
				v := true
				ic.fields[i].confirmed = &v
			case "no":
				v := false
				ic.fields[i].confirmed = &v
			}
			if ans.Elaboration != "" {
				ic.fields[i].elaboration.SetValue(ans.Elaboration)
			}
		} else {
			// Text field.
			ic.fields[i].textInput.SetValue(ans.Answer)
		}
	}
}

// collectAnswers builds an InterviewResult from the current field states.
func (ic *InterviewContent) collectAnswers() handlers.InterviewResult {
	answers := make([]handlers.InterviewAnswer, len(ic.fields))
	for i, f := range ic.fields {
		ans := handlers.InterviewAnswer{
			ID:      fmt.Sprintf("q%d", f.question.Index),
			Text:    f.question.Text,
			Options: f.question.Options,
		}

		if len(f.question.Options) > 0 {
			// Select field.
			if f.isOther || f.selectCursor >= len(f.question.Options) {
				ans.Answer = strings.TrimSpace(f.otherInput.Value())
			} else {
				ans.Answer = f.question.Options[f.selectCursor]
			}
			ans.Elaboration = strings.TrimSpace(f.elaboration.Value())
		} else if f.question.IsYesNo {
			// Confirm field.
			if f.confirmed != nil {
				if *f.confirmed {
					ans.Answer = "Yes"
				} else {
					ans.Answer = "No"
				}
			}
			ans.Elaboration = strings.TrimSpace(f.elaboration.Value())
		} else {
			// Text field.
			ans.Answer = strings.TrimSpace(f.textInput.Value())
		}

		answers[i] = ans
	}
	result := handlers.InterviewResult{Questions: answers}
	for _, a := range answers {
		if a.Answer == "" {
			result.Incomplete = true
			break
		}
	}
	return result
}

// Update handles keyboard input for the interview form.
func (ic *InterviewContent) Update(msg tea.Msg) tea.Cmd {
	if ic.done {
		return nil
	}

	km, ok := msg.(tea.KeyMsg)
	if !ok {
		// Forward non-key messages to active textarea.
		if ic.inTextarea && ic.cursor < len(ic.fields) {
			return ic.updateActiveTextarea(msg)
		}
		return nil
	}

	// Global keys.
	switch km.String() {
	case "ctrl+s":
		return ic.submit()
	}

	// If we're in a textarea, route to textarea handling.
	if ic.inTextarea {
		return ic.updateTextareaMode(km)
	}

	return ic.updateNavigationMode(km)
}

// updateActiveTextarea forwards a non-key message to whichever textarea is active.
func (ic *InterviewContent) updateActiveTextarea(msg tea.Msg) tea.Cmd {
	f := &ic.fields[ic.cursor]
	var cmd tea.Cmd

	if f.editingElab {
		f.elaboration, cmd = f.elaboration.Update(msg)
	} else if len(f.question.Options) > 0 && f.isOther {
		f.otherInput, cmd = f.otherInput.Update(msg)
	} else if len(f.question.Options) == 0 && !f.question.IsYesNo {
		f.textInput, cmd = f.textInput.Update(msg)
	}
	return cmd
}

// updateTextareaMode handles keys when a textarea is focused.
func (ic *InterviewContent) updateTextareaMode(km tea.KeyMsg) tea.Cmd {
	f := &ic.fields[ic.cursor]

	switch km.String() {
	case "esc":
		// Exit textarea, return to navigation.
		ic.inTextarea = false
		ic.blurAll(f)
		return nil
	case "ctrl+s":
		return ic.submit()
	}

	// Forward to the active textarea.
	var cmd tea.Cmd
	if f.editingElab {
		f.elaboration, cmd = f.elaboration.Update(km)
	} else if len(f.question.Options) > 0 && f.isOther {
		f.otherInput, cmd = f.otherInput.Update(km)
	} else if len(f.question.Options) == 0 && !f.question.IsYesNo {
		f.textInput, cmd = f.textInput.Update(km)
	}
	return cmd
}

// updateNavigationMode handles keys when navigating between questions.
func (ic *InterviewContent) updateNavigationMode(km tea.KeyMsg) tea.Cmd {
	if ic.cursor >= len(ic.fields) {
		return nil
	}
	f := &ic.fields[ic.cursor]

	// Field-specific input handling.
	if len(f.question.Options) > 0 {
		return ic.updateSelectField(km, f)
	}
	if f.question.IsYesNo {
		return ic.updateConfirmField(km, f)
	}
	return ic.updateTextField(km, f)
}

// updateSelectField handles navigation within a select (radio) field.
func (ic *InterviewContent) updateSelectField(km tea.KeyMsg, f *interviewField) tea.Cmd {
	totalOpts := len(f.question.Options) + 1 // +1 for "Other"

	switch km.Type {
	case tea.KeyUp:
		if f.selectCursor > 0 {
			f.selectCursor--
			f.isOther = false
		} else {
			// Move to previous question.
			return ic.moveCursor(-1)
		}
	case tea.KeyDown:
		if f.selectCursor < totalOpts-1 {
			f.selectCursor++
			f.isOther = f.selectCursor >= len(f.question.Options)
		} else {
			// Move to next question.
			return ic.moveCursor(1)
		}
	case tea.KeyEnter:
		if f.selectCursor >= len(f.question.Options) {
			// "Other" — activate otherInput textarea.
			f.isOther = true
			ic.inTextarea = true
			f.otherInput.Focus()
			return nil
		}
		// Confirm selection and move to next question.
		return ic.moveCursor(1)
	case tea.KeyEscape:
		// Move to previous question; cancel only if at the first question.
		if ic.cursor > 0 {
			return ic.moveCursor(-1)
		}
		return ic.cancel()
	}

	switch km.String() {
	case "tab":
		// Move to elaboration textarea.
		f.editingElab = true
		ic.inTextarea = true
		f.elaboration.Focus()
		return nil
	case "shift+tab":
		// Nothing to go back to from option selection.
	case "pgup":
		return ic.changePage(-1)
	case "pgdown":
		return ic.changePage(1)
	}
	return nil
}

// updateConfirmField handles the yes/no toggle.
func (ic *InterviewContent) updateConfirmField(km tea.KeyMsg, f *interviewField) tea.Cmd {
	switch km.Type {
	case tea.KeyUp:
		return ic.moveCursor(-1)
	case tea.KeyDown:
		return ic.moveCursor(1)
	case tea.KeyEnter:
		// Toggle or set yes.
		if f.confirmed == nil {
			v := true
			f.confirmed = &v
		} else {
			v := !*f.confirmed
			f.confirmed = &v
		}
		return nil
	case tea.KeyEscape:
		if ic.cursor > 0 {
			return ic.moveCursor(-1)
		}
		return ic.cancel()
	}

	switch km.String() {
	case "y", "Y":
		v := true
		f.confirmed = &v
		return nil
	case "n", "N":
		v := false
		f.confirmed = &v
		return nil
	case "tab":
		f.editingElab = true
		ic.inTextarea = true
		f.elaboration.Focus()
		return nil
	case "pgup":
		return ic.changePage(-1)
	case "pgdown":
		return ic.changePage(1)
	}
	return nil
}

// updateTextField handles navigation for text input fields.
func (ic *InterviewContent) updateTextField(km tea.KeyMsg, f *interviewField) tea.Cmd {
	switch km.Type {
	case tea.KeyUp:
		return ic.moveCursor(-1)
	case tea.KeyDown:
		return ic.moveCursor(1)
	case tea.KeyEnter:
		// Focus the textarea.
		ic.inTextarea = true
		f.textInput.Focus()
		return nil
	case tea.KeyEscape:
		if ic.cursor > 0 {
			return ic.moveCursor(-1)
		}
		return ic.cancel()
	}

	switch km.String() {
	case "pgup":
		return ic.changePage(-1)
	case "pgdown":
		return ic.changePage(1)
	}
	return nil
}

// moveCursor moves the question cursor by delta, handling page changes.
func (ic *InterviewContent) moveCursor(delta int) tea.Cmd {
	next := ic.cursor + delta
	if next < 0 || next >= len(ic.fields) {
		return nil
	}
	ic.cursor = next
	// Auto-change page if needed.
	targetPage := ic.cursor / ic.pageSize
	if targetPage != ic.page {
		ic.page = targetPage
	}
	return nil
}

// changePage moves to the next/previous page.
func (ic *InterviewContent) changePage(delta int) tea.Cmd {
	totalPages := ic.totalPages()
	newPage := ic.page + delta
	if newPage < 0 || newPage >= totalPages {
		return nil
	}
	ic.page = newPage
	ic.cursor = ic.page * ic.pageSize
	return nil
}

// totalPages returns the total number of pages.
func (ic *InterviewContent) totalPages() int {
	if len(ic.fields) == 0 {
		return 1
	}
	pages := len(ic.fields) / ic.pageSize
	if len(ic.fields)%ic.pageSize != 0 {
		pages++
	}
	return pages
}

// blurAll blurs all textareas on a field.
func (ic *InterviewContent) blurAll(f *interviewField) {
	f.textInput.Blur()
	f.otherInput.Blur()
	f.elaboration.Blur()
	f.editingElab = false
}

// submit sends the collected answers as JSON on replyCh.
func (ic *InterviewContent) submit() tea.Cmd {
	if ic.done {
		return nil
	}
	ic.done = true
	result := ic.collectAnswers()
	if ic.replyCh != nil {
		ic.replyCh <- handlers.SerializeInterviewResult(result)
		ic.replyCh = nil
	}
	return func() tea.Msg { return MsgModalDismiss{} }
}

// Cancel implements Cancellable. Closes the reply channel to signal cancellation,
// consistent with all other ModalContent types (ChoiceContent, FreeformContent, etc.).
func (ic *InterviewContent) Cancel() { ic.cancelForm() }

// cancelForm closes the reply channel to signal cancellation.
// The receiver (askMode2Interview) detects the closed channel via the ok flag
// and returns InterviewResult{Canceled: true}.
func (ic *InterviewContent) cancelForm() tea.Cmd {
	if ic.done {
		return nil
	}
	ic.done = true
	if ic.replyCh != nil {
		close(ic.replyCh)
		ic.replyCh = nil
	}
	return func() tea.Msg { return MsgModalDismiss{} }
}

// cancel is called from Esc at the top level.
func (ic *InterviewContent) cancel() tea.Cmd {
	return ic.cancelForm()
}

// View renders the fullscreen interview form.
func (ic *InterviewContent) View() string {
	var sb strings.Builder

	titleStyle := lipgloss.NewStyle().Bold(true).Foreground(ColorReadout)
	sb.WriteString(titleStyle.Render("INTERVIEW"))
	sb.WriteString("\n\n")

	// Determine visible range from pagination.
	start := ic.page * ic.pageSize
	end := start + ic.pageSize
	if end > len(ic.fields) {
		end = len(ic.fields)
	}

	accentStyle := lipgloss.NewStyle().Bold(true).Foreground(ColorReadout)
	normalQStyle := lipgloss.NewStyle().Bold(true)
	selectedStyle := lipgloss.NewStyle().Bold(true).Foreground(ColorGreen)
	normalStyle := lipgloss.NewStyle()

	for idx := start; idx < end; idx++ {
		f := &ic.fields[idx]
		isFocused := idx == ic.cursor

		// Question header.
		qLabel := fmt.Sprintf("Q%d: %s", f.question.Index, f.question.Text)
		if isFocused {
			sb.WriteString(accentStyle.Render(qLabel))
		} else {
			sb.WriteString(normalQStyle.Render(qLabel))
		}
		sb.WriteString("\n")

		if len(f.question.Options) > 0 {
			// Radio select field.
			for j, opt := range f.question.Options {
				if isFocused && j == f.selectCursor && !f.isOther {
					sb.WriteString(selectedStyle.Render(fmt.Sprintf("  ● %s", opt)))
				} else {
					sb.WriteString(normalStyle.Render(fmt.Sprintf("  ○ %s", opt)))
				}
				sb.WriteString("\n")
			}
			// "Other" option.
			if isFocused && (f.selectCursor >= len(f.question.Options)) && !ic.inTextarea {
				sb.WriteString(selectedStyle.Render("  ● Other"))
			} else if f.isOther && ic.inTextarea && !f.editingElab {
				sb.WriteString(selectedStyle.Render("  ● Other:"))
			} else {
				sb.WriteString(normalStyle.Render("  ○ Other"))
			}
			sb.WriteString("\n")
			// Show other textarea when active.
			if f.isOther && ic.inTextarea && isFocused && !f.editingElab {
				sb.WriteString(f.otherInput.View())
				sb.WriteString("\n")
			}
			// Elaboration textarea.
			if isFocused && f.editingElab {
				sb.WriteString(Styles.Muted.Render("  Add details (optional):"))
				sb.WriteString("\n")
				sb.WriteString(f.elaboration.View())
				sb.WriteString("\n")
			} else if isFocused && !ic.inTextarea {
				sb.WriteString(Styles.Muted.Render("  Tab → Add details (optional)"))
				sb.WriteString("\n")
			}

		} else if f.question.IsYesNo {
			// Yes/No toggle.
			yStr := "[ ] Yes"
			nStr := "[ ] No"
			if f.confirmed != nil {
				if *f.confirmed {
					yStr = "[Y] Yes"
				} else {
					nStr = "[N] No"
				}
			}
			if isFocused {
				sb.WriteString(accentStyle.Render(fmt.Sprintf("  %s  %s", yStr, nStr)))
			} else {
				sb.WriteString(normalStyle.Render(fmt.Sprintf("  %s  %s", yStr, nStr)))
			}
			sb.WriteString("\n")
			// Elaboration textarea.
			if isFocused && f.editingElab {
				sb.WriteString(Styles.Muted.Render("  Add details (optional):"))
				sb.WriteString("\n")
				sb.WriteString(f.elaboration.View())
				sb.WriteString("\n")
			} else if isFocused && !ic.inTextarea {
				sb.WriteString(Styles.Muted.Render("  Tab → Add details (optional)"))
				sb.WriteString("\n")
			}

		} else {
			// Text field.
			if isFocused && ic.inTextarea {
				sb.WriteString(f.textInput.View())
			} else {
				val := f.textInput.Value()
				if val == "" {
					sb.WriteString(Styles.Muted.Render("  (press Enter to type)"))
				} else {
					sb.WriteString(normalStyle.Render(fmt.Sprintf("  %s", val)))
				}
			}
			sb.WriteString("\n")
		}

		sb.WriteString("\n")
	}

	// Status line.
	totalPages := ic.totalPages()
	pageInfo := fmt.Sprintf("Page %d/%d", ic.page+1, totalPages)
	hint := fmt.Sprintf("%s — ↑↓ navigate, Tab elaborate, Ctrl+S submit, Esc back/cancel", pageInfo)
	if ic.inTextarea {
		hint = fmt.Sprintf("%s — type answer, Esc back to navigation, Ctrl+S submit", pageInfo)
	}
	sb.WriteString(Styles.Muted.Render(hint))

	return sb.String()
}
