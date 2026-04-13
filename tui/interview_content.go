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
	selected     bool           // true once user confirms a selection (Enter)
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
	questions  []handlers.Question
	fields     []interviewField
	cursor     int           // which question is focused (0-indexed into full list)
	page       int           // current page (0-indexed)
	pageSize   int           // questions per page (default 10)
	replyCh    chan<- string // JSON string reply
	done       bool
	width      int
	height     int
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
		ic.prefillField(i, f, ans)
	}
}

// prefillField restores a single field from a previous answer.
func (ic *InterviewContent) prefillField(i int, f interviewField, ans handlers.InterviewAnswer) {
	if f.question.IsYesNo {
		ic.prefillYesNo(i, ans)
	} else if len(f.question.Options) > 0 {
		ic.prefillSelect(i, f, ans)
	} else {
		ic.fields[i].textInput.SetValue(ans.Answer)
	}
}

// prefillYesNo restores a yes/no confirmation field.
func (ic *InterviewContent) prefillYesNo(i int, ans handlers.InterviewAnswer) {
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
}

// prefillSelect restores a radio-select field, falling back to "Other" when no option matches.
func (ic *InterviewContent) prefillSelect(i int, f interviewField, ans handlers.InterviewAnswer) {
	matched := false
	for j, opt := range f.question.Options {
		if strings.EqualFold(opt, ans.Answer) {
			ic.fields[i].selectCursor = j
			ic.fields[i].selected = true
			matched = true
			break
		}
	}
	if !matched && ans.Answer != "" {
		ic.fields[i].selectCursor = len(f.question.Options)
		ic.fields[i].isOther = true
		ic.fields[i].selected = true
		ic.fields[i].otherInput.SetValue(ans.Answer)
	}
	if ans.Elaboration != "" {
		ic.fields[i].elaboration.SetValue(ans.Elaboration)
	}
}

// collectAnswers builds an InterviewResult from the current field states.
func (ic *InterviewContent) collectAnswers() handlers.InterviewResult {
	answers := make([]handlers.InterviewAnswer, len(ic.fields))
	for i, f := range ic.fields {
		answers[i] = ic.collectFieldAnswer(f)
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

// collectFieldAnswer extracts the answer for a single field based on its type.
func (ic *InterviewContent) collectFieldAnswer(f interviewField) handlers.InterviewAnswer {
	ans := handlers.InterviewAnswer{
		ID:      fmt.Sprintf("q%d", f.question.Index),
		Text:    f.question.Text,
		Options: f.question.Options,
	}
	if f.question.IsYesNo {
		ans.Answer, ans.Elaboration = collectYesNoAnswer(f)
	} else if len(f.question.Options) > 0 {
		ans.Answer, ans.Elaboration = collectSelectAnswer(f)
	} else {
		ans.Answer = strings.TrimSpace(f.textInput.Value())
	}
	return ans
}

// collectYesNoAnswer returns the answer and elaboration for a yes/no field.
func collectYesNoAnswer(f interviewField) (answer, elaboration string) {
	if f.confirmed != nil {
		if *f.confirmed {
			answer = "Yes"
		} else {
			answer = "No"
		}
	}
	elaboration = strings.TrimSpace(f.elaboration.Value())
	return
}

// collectSelectAnswer returns the answer and elaboration for a radio-select field.
func collectSelectAnswer(f interviewField) (answer, elaboration string) {
	if f.selected {
		if f.isOther || f.selectCursor >= len(f.question.Options) {
			answer = strings.TrimSpace(f.otherInput.Value())
		} else {
			answer = f.question.Options[f.selectCursor]
		}
	}
	elaboration = strings.TrimSpace(f.elaboration.Value())
	return
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

	// "Other" and open-ended text fields: Enter confirms and advances.
	// Elaboration fields: Enter inserts newlines (multi-line is useful there).
	if km.Type == tea.KeyEnter && !f.editingElab {
		ic.inTextarea = false
		f.selected = true
		ic.blurAll(f)
		return ic.moveCursor(1)
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
	// Check IsYesNo before Options so yes/no questions with residual
	// options (e.g. ["yes","no"] from filterOtherOption) route to the
	// confirm UI instead of the radio select UI.
	if f.question.IsYesNo {
		return ic.updateConfirmField(km, f)
	}
	if len(f.question.Options) > 0 {
		return ic.updateSelectField(km, f)
	}
	return ic.updateTextField(km, f)
}

// updateSelectField handles navigation within a select (radio) field.
func (ic *InterviewContent) updateSelectField(km tea.KeyMsg, f *interviewField) tea.Cmd {
	totalOpts := len(f.question.Options) + 1 // +1 for "Other"

	if cmd := ic.handleSelectNavKeys(km, f, totalOpts); cmd != nil {
		return cmd
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

// handleSelectNavKeys handles Up/Down/Enter/Escape navigation for a select field.
// Returns a non-nil Cmd when the caller should return immediately.
func (ic *InterviewContent) handleSelectNavKeys(km tea.KeyMsg, f *interviewField, totalOpts int) tea.Cmd {
	switch km.Type {
	case tea.KeyUp:
		if f.selectCursor > 0 {
			f.selectCursor--
			f.isOther = false
		} else {
			return ic.moveCursor(-1)
		}
	case tea.KeyDown:
		if f.selectCursor < totalOpts-1 {
			f.selectCursor++
			f.isOther = f.selectCursor >= len(f.question.Options)
		} else {
			return ic.moveCursor(1)
		}
	case tea.KeyEnter:
		return ic.handleSelectEnter(f)
	case tea.KeyEscape:
		if ic.cursor > 0 {
			return ic.moveCursor(-1)
		}
		return ic.cancel()
	default:
		return nil
	}
	return nil
}

// handleSelectEnter processes Enter on a select field (confirm or activate "Other").
func (ic *InterviewContent) handleSelectEnter(f *interviewField) tea.Cmd {
	if f.selectCursor >= len(f.question.Options) {
		// "Other" — activate otherInput textarea.
		f.isOther = true
		f.selected = true
		ic.inTextarea = true
		f.otherInput.Focus()
		return nil
	}
	f.selected = true
	return ic.moveCursor(1)
}

// updateConfirmField handles the yes/no toggle.
func (ic *InterviewContent) updateConfirmField(km tea.KeyMsg, f *interviewField) tea.Cmd {
	switch km.Type {
	case tea.KeyUp:
		return ic.moveCursor(-1)
	case tea.KeyDown:
		return ic.moveCursor(1)
	case tea.KeyEnter:
		// Set yes (or toggle) and advance.
		if f.confirmed == nil {
			v := true
			f.confirmed = &v
		} else {
			v := !*f.confirmed
			f.confirmed = &v
		}
		return ic.moveCursor(1)
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
		return ic.moveCursor(1)
	case "n", "N":
		v := false
		f.confirmed = &v
		return ic.moveCursor(1)
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

// cancelForm collects partial answers, marks the result as canceled, sends it
// on the reply channel, then closes the channel. This way the handler receives
// both the Canceled flag and any partial answers the user already entered.
func (ic *InterviewContent) cancelForm() tea.Cmd {
	if ic.done {
		return nil
	}
	ic.done = true
	if ic.replyCh != nil {
		result := ic.collectAnswers()
		result.Canceled = true
		ic.replyCh <- handlers.SerializeInterviewResult(result)
		close(ic.replyCh)
		ic.replyCh = nil
	}
	return func() tea.Msg { return MsgModalDismiss{} }
}

// cancel is called from Esc at the top level.
func (ic *InterviewContent) cancel() tea.Cmd {
	return ic.cancelForm()
}

// wrapText wraps text to fit within the interview content width with a left indent.
func (ic *InterviewContent) wrapText(text string, indent int) string {
	maxW := ic.width - indent - 2 // 2 for margin
	if maxW < 20 {
		maxW = 20
	}
	return lipgloss.NewStyle().Width(maxW).Render(text)
}

// View renders the interview form — one question at a time with progress summary.
func (ic *InterviewContent) View() string {
	var sb strings.Builder
	sb.WriteString(ic.renderProgress())
	sb.WriteString(ic.renderAnsweredSummary())
	if ic.cursor < len(ic.fields) {
		sb.WriteString(ic.renderCurrentQuestion())
	}
	sb.WriteString(ic.renderFooter())
	return sb.String()
}

// renderProgress renders the header title and progress bar.
func (ic *InterviewContent) renderProgress() string {
	var sb strings.Builder
	titleStyle := lipgloss.NewStyle().Bold(true).Foreground(ColorReadout)
	confirmedStyle := lipgloss.NewStyle().Foreground(ColorGreen)

	answered := 0
	for _, f := range ic.fields {
		if ic.fieldAnswered(&f) {
			answered++
		}
	}
	sb.WriteString(titleStyle.Render(fmt.Sprintf("INTERVIEW  (%d/%d answered)", answered, len(ic.fields))))
	sb.WriteString("\n")

	barWidth := 40
	if ic.width > 60 {
		barWidth = ic.width / 2
	}
	filled := 0
	if len(ic.fields) > 0 {
		filled = (answered * barWidth) / len(ic.fields)
	}
	bar := strings.Repeat("━", filled) + strings.Repeat("─", barWidth-filled)
	sb.WriteString(confirmedStyle.Render(bar))
	sb.WriteString("\n\n")
	return sb.String()
}

// renderAnsweredSummary renders the compact list of previously answered questions.
func (ic *InterviewContent) renderAnsweredSummary() string {
	if ic.cursor == 0 {
		return ""
	}
	confirmedStyle := lipgloss.NewStyle().Foreground(ColorGreen)
	summaryW := ic.width - 6
	if summaryW < 20 {
		summaryW = 20
	}
	summaryStyle := confirmedStyle.Width(summaryW)
	skippedStyle := Styles.Muted.Width(summaryW)

	availRows := ic.height - 15
	if availRows < 3 {
		availRows = 3
	}
	summaryStart := 0
	if ic.cursor > availRows {
		summaryStart = ic.cursor - availRows
	}

	var sb strings.Builder
	for idx := summaryStart; idx < ic.cursor && idx < len(ic.fields); idx++ {
		f := &ic.fields[idx]
		answer := ic.fieldAnswerText(f)
		if answer != "" {
			sb.WriteString(summaryStyle.Render(fmt.Sprintf("  ✓ Q%d: %s → %s", f.question.Index, f.question.Text, answer)))
		} else {
			sb.WriteString(skippedStyle.Render(fmt.Sprintf("  · Q%d: %s (skipped)", f.question.Index, f.question.Text)))
		}
		sb.WriteString("\n")
	}
	sb.WriteString("\n")
	return sb.String()
}

// renderCurrentQuestion renders the full block for the question at ic.cursor.
func (ic *InterviewContent) renderCurrentQuestion() string {
	f := &ic.fields[ic.cursor]
	accentStyle := lipgloss.NewStyle().Bold(true).Foreground(ColorReadout).Width(ic.width - 2)

	var sb strings.Builder
	qLabel := fmt.Sprintf("Q%d of %d: %s", f.question.Index, len(ic.fields), f.question.Text)
	sb.WriteString(accentStyle.Render(qLabel))
	sb.WriteString("\n")

	if f.question.Context != "" {
		ctxW := ic.width - 6
		if ctxW < 20 {
			ctxW = 20
		}
		ctxStyle := lipgloss.NewStyle().Faint(true).Italic(true).Width(ctxW)
		sb.WriteString("  " + ctxStyle.Render(f.question.Context) + "\n")
	}
	sb.WriteString("\n")

	if f.question.IsYesNo {
		sb.WriteString(ic.renderYesNoField(f))
	} else if len(f.question.Options) > 0 {
		sb.WriteString(ic.renderSelectField(f))
	} else {
		sb.WriteString(ic.renderTextField(f))
	}
	return sb.String()
}

// renderYesNoField renders the yes/no toggle for a confirm question.
func (ic *InterviewContent) renderYesNoField(f *interviewField) string {
	confirmedStyle := lipgloss.NewStyle().Foreground(ColorGreen)
	normalStyle := lipgloss.NewStyle()

	yStr := "○ Yes"
	nStr := "○ No"
	if f.confirmed != nil {
		if *f.confirmed {
			yStr = confirmedStyle.Render("● Yes  ✓")
			nStr = normalStyle.Render("○ No")
		} else {
			yStr = normalStyle.Render("○ Yes")
			nStr = confirmedStyle.Render("● No  ✓")
		}
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("  %s    %s", yStr, nStr))
	sb.WriteString("\n")
	sb.WriteString(ic.renderElaboration(f, "  y/n toggle  Enter confirm  Tab elaborate"))
	return sb.String()
}

// renderSelectField renders the radio-select options for a multi-choice question.
func (ic *InterviewContent) renderSelectField(f *interviewField) string {
	var sb strings.Builder
	sb.WriteString(ic.renderSelectOptions(f))
	sb.WriteString(ic.renderElaboration(f, "  ↑↓ select  Enter confirm  Tab elaborate"))
	return sb.String()
}

// renderSelectOptions renders the option list rows including the "Other" row.
func (ic *InterviewContent) renderSelectOptions(f *interviewField) string {
	var sb strings.Builder
	sb.WriteString(ic.renderOptionRows(f))
	sb.WriteString(ic.renderOtherRow(f))
	return sb.String()
}

// renderOptionRows renders each named option as a radio row.
func (ic *InterviewContent) renderOptionRows(f *interviewField) string {
	confirmedStyle := lipgloss.NewStyle().Foreground(ColorGreen)
	selectedStyle := lipgloss.NewStyle().Bold(true).Foreground(ColorGreen)
	normalStyle := lipgloss.NewStyle()

	optW := ic.width - 8
	if optW < 20 {
		optW = 20
	}

	var sb strings.Builder
	for j, opt := range f.question.Options {
		isHovered := j == f.selectCursor && !f.isOther
		isChosen := f.selected && j == f.selectCursor && !f.isOther
		wrapped := lipgloss.NewStyle().Width(optW).Render(opt)
		if isChosen {
			sb.WriteString(confirmedStyle.Render(fmt.Sprintf("  ● %s  ✓", wrapped)))
		} else if isHovered {
			sb.WriteString(selectedStyle.Render(fmt.Sprintf("  ● %s", wrapped)))
		} else {
			sb.WriteString(normalStyle.Render(fmt.Sprintf("  ○ %s", wrapped)))
		}
		sb.WriteString("\n")
	}
	return sb.String()
}

// renderOtherRow renders the "Other" option row and its textarea when active.
func (ic *InterviewContent) renderOtherRow(f *interviewField) string {
	selectedStyle := lipgloss.NewStyle().Bold(true).Foreground(ColorGreen)
	normalStyle := lipgloss.NewStyle()

	var sb strings.Builder
	if f.selectCursor >= len(f.question.Options) && !ic.inTextarea {
		sb.WriteString(selectedStyle.Render("  ● Other"))
	} else if f.isOther && ic.inTextarea && !f.editingElab {
		sb.WriteString(selectedStyle.Render("  ● Other:"))
	} else {
		sb.WriteString(normalStyle.Render("  ○ Other"))
	}
	sb.WriteString("\n")

	if f.isOther && ic.inTextarea && !f.editingElab {
		sb.WriteString(f.otherInput.View())
		sb.WriteString("\n")
		sb.WriteString(Styles.Muted.Render("  Enter confirm  Esc cancel"))
		sb.WriteString("\n")
	}
	return sb.String()
}

// renderElaboration renders the elaboration textarea or hint line for select/confirm questions.
func (ic *InterviewContent) renderElaboration(f *interviewField, hint string) string {
	var sb strings.Builder
	if f.editingElab {
		sb.WriteString(Styles.Muted.Render("  Add details (optional):"))
		sb.WriteString("\n")
		sb.WriteString(f.elaboration.View())
		sb.WriteString("\n")
	} else if !ic.inTextarea {
		sb.WriteString("\n")
		sb.WriteString(Styles.Muted.Render(hint))
		sb.WriteString("\n")
	}
	return sb.String()
}

// renderTextField renders an open-ended text input field.
func (ic *InterviewContent) renderTextField(f *interviewField) string {
	normalStyle := lipgloss.NewStyle()
	var sb strings.Builder
	if ic.inTextarea {
		sb.WriteString(f.textInput.View())
		sb.WriteString("\n")
		sb.WriteString(Styles.Muted.Render("  Enter confirm  Esc cancel"))
		sb.WriteString("\n")
	} else {
		val := f.textInput.Value()
		if val == "" {
			sb.WriteString(Styles.Muted.Render("  (press Enter to type your answer)"))
		} else {
			sb.WriteString(normalStyle.Render(fmt.Sprintf("  %s", val)))
		}
		sb.WriteString("\n\n")
		sb.WriteString(Styles.Muted.Render("  Enter to type  Esc back"))
		sb.WriteString("\n")
	}
	return sb.String()
}

// renderFooter renders the remaining-questions preview and status line.
func (ic *InterviewContent) renderFooter() string {
	var sb strings.Builder
	remaining := len(ic.fields) - ic.cursor - 1
	if remaining > 0 {
		sb.WriteString("\n")
		sb.WriteString(Styles.Muted.Render(fmt.Sprintf("  %d question%s remaining", remaining, pluralS(remaining))))
		sb.WriteString("\n")
	}
	sb.WriteString("\n")
	if ic.inTextarea {
		sb.WriteString(Styles.Muted.Render("Esc back to navigation  Ctrl+S submit all"))
	} else {
		sb.WriteString(Styles.Muted.Render("↑↓ navigate  Ctrl+S submit all  Esc cancel"))
	}
	return sb.String()
}

// fieldAnswered returns true if the field has a non-empty answer.
func (ic *InterviewContent) fieldAnswered(f *interviewField) bool {
	if f.question.IsYesNo {
		return f.confirmed != nil
	}
	if len(f.question.Options) > 0 {
		if !f.selected {
			return false
		}
		// "Other" with empty text is not truly answered.
		if f.isOther || f.selectCursor >= len(f.question.Options) {
			return strings.TrimSpace(f.otherInput.Value()) != ""
		}
		return true
	}
	return strings.TrimSpace(f.textInput.Value()) != ""
}

// fieldAnswerText returns a short summary of the field's answer for the progress list.
func (ic *InterviewContent) fieldAnswerText(f *interviewField) string {
	if f.question.IsYesNo {
		if f.confirmed == nil {
			return ""
		}
		if *f.confirmed {
			return "Yes"
		}
		return "No"
	}
	if len(f.question.Options) > 0 {
		if !f.selected {
			return ""
		}
		if f.isOther || f.selectCursor >= len(f.question.Options) {
			return strings.TrimSpace(f.otherInput.Value())
		}
		return f.question.Options[f.selectCursor]
	}
	return strings.TrimSpace(f.textInput.Value())
}

// pluralS returns "s" if n != 1.
func pluralS(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}
