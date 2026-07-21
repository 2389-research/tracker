// ABOUTME: The Slack Socket Mode transport — the only file that imports slack-go.
// ABOUTME: One WebSocket in; demultiplexes events to the Runner; renders gates as Block Kit.
package main

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"
	"github.com/slack-go/slack/socketmode"
)

// SlackBot is the Socket Mode transport: one outbound WebSocket that
// demultiplexes inbound events (mentions, button clicks, thread replies) to the
// Runner, and a source of thread-bound ThreadUIs for output.
type SlackBot struct {
	api    *slack.Client
	sm     *socketmode.Client
	runner *Runner
	selfID string // the bot's own user id, to ignore its own messages

	allowedUsers map[string]bool // user ids allowed to drive the bot; empty = open

	mu          sync.Mutex
	pendingFree map[string]string // thread_ts → pending freeform gate id
}

// SetAllowlist restricts who may drive the bot to the given Slack user ids. An
// empty/absent list leaves the bot open to anyone in the channels it's in.
func (b *SlackBot) SetAllowlist(users []string) {
	m := make(map[string]bool, len(users))
	for _, u := range users {
		if u = strings.TrimSpace(u); u != "" {
			m[u] = true
		}
	}
	b.allowedUsers = m
}

// authorized reports whether user may drive the bot. An empty allowlist is open.
func (b *SlackBot) authorized(user string) bool {
	return len(b.allowedUsers) == 0 || b.allowedUsers[user]
}

// NewSlackBot verifies the bot token and prepares the Socket Mode client. Call
// SetRunner, then Run.
func NewSlackBot(botToken, appToken string) (*SlackBot, error) {
	api := slack.New(botToken, slack.OptionAppLevelToken(appToken))
	auth, err := api.AuthTest()
	if err != nil {
		return nil, fmt.Errorf("slack auth: %w", err)
	}
	return &SlackBot{
		api:         api,
		sm:          socketmode.New(api),
		selfID:      auth.UserID,
		pendingFree: make(map[string]string),
	}, nil
}

// SetRunner wires the orchestrator the transport dispatches inbound events to.
func (b *SlackBot) SetRunner(r *Runner) { b.runner = r }

// NewThreadUI returns a ThreadUI bound to one (channel, thread) for the Runner.
func (b *SlackBot) NewThreadUI(channel, threadTS string) ThreadUI {
	return &slackThreadUI{bot: b, channel: channel, threadTS: threadTS}
}

// Run starts the Socket Mode event loop and blocks until ctx is cancelled.
func (b *SlackBot) Run(ctx context.Context) error {
	go b.consume(ctx)
	return b.sm.RunContext(ctx)
}

func (b *SlackBot) consume(ctx context.Context) {
	for evt := range b.sm.Events {
		switch evt.Type {
		case socketmode.EventTypeEventsAPI:
			b.onEventsAPI(ctx, evt)
		case socketmode.EventTypeInteractive:
			b.onInteractive(evt)
		}
	}
}

// onEventsAPI handles app_mention (start a run) and message (thread reply).
func (b *SlackBot) onEventsAPI(ctx context.Context, evt socketmode.Event) {
	// Ack every dispatched envelope (before any shape check) so Socket Mode does
	// not redeliver it.
	if evt.Request != nil {
		_ = b.sm.Ack(*evt.Request)
	}
	api, ok := evt.Data.(slackevents.EventsAPIEvent)
	if !ok {
		return
	}
	if api.Type != slackevents.CallbackEvent {
		return
	}
	switch inner := api.InnerEvent.Data.(type) {
	case *slackevents.AppMentionEvent:
		b.onMention(ctx, inner)
	case *slackevents.MessageEvent:
		b.onThreadReply(inner)
	}
}

// onMention starts a run for a fresh @mention (ignoring the bot's own).
func (b *SlackBot) onMention(ctx context.Context, m *slackevents.AppMentionEvent) {
	if m.BotID != "" || m.User == b.selfID {
		return
	}
	threadTS := m.ThreadTimeStamp
	if threadTS == "" {
		threadTS = m.TimeStamp // a top-level mention roots a new thread
	}
	// Authorization gate: paid, host-side pipeline execution triggered from chat
	// must be restricted to allowlisted users when a list is configured. Gating
	// here (not in the Runner) also blocks control commands (cancel/status) for
	// unauthorized users, and keeps the Runner transport-agnostic — each
	// transport enforces its own identity model.
	if !b.authorized(m.User) {
		_ = b.NewThreadUI(m.Channel, threadTS).Post("Sorry — you're not on the allowlist to run pipelines here.")
		return
	}
	go b.runner.OnMention(ctx, m.Channel, threadTS, m.Text)
}

// onThreadReply resolves a pending freeform gate from a human reply in a thread.
func (b *SlackBot) onThreadReply(m *slackevents.MessageEvent) {
	if m.BotID != "" || m.User == "" || m.User == b.selfID || m.ThreadTimeStamp == "" {
		return
	}
	gateID := b.takePendingFreeform(m.ThreadTimeStamp)
	if gateID == "" {
		return
	}
	b.runner.OnInteraction(m.ThreadTimeStamp, gateID, GateAnswer{Freeform: strings.TrimSpace(m.Text)})
}

// onInteractive handles button clicks (choice/yes_no gates).
func (b *SlackBot) onInteractive(evt socketmode.Event) {
	if evt.Request != nil {
		_ = b.sm.Ack(*evt.Request)
	}
	cb, ok := evt.Data.(slack.InteractionCallback)
	if !ok {
		return
	}
	if cb.Type != slack.InteractionTypeBlockActions {
		return
	}
	threadTS := cb.Container.ThreadTs
	if threadTS == "" {
		threadTS = cb.Message.ThreadTimestamp
	}
	if threadTS == "" {
		threadTS = cb.Container.MessageTs
	}
	for _, action := range cb.ActionCallback.BlockActions {
		if gateID, ok := parseGateAction(action.ActionID); ok {
			b.runner.OnInteraction(threadTS, gateID, GateAnswer{Choice: action.Value})
		}
	}
}

func (b *SlackBot) setPendingFreeform(threadTS, gateID string) {
	b.mu.Lock()
	b.pendingFree[threadTS] = gateID
	b.mu.Unlock()
}

func (b *SlackBot) takePendingFreeform(threadTS string) string {
	b.mu.Lock()
	defer b.mu.Unlock()
	id := b.pendingFree[threadTS]
	delete(b.pendingFree, threadTS)
	return id
}

// clearPendingFreeform removes a thread's pending freeform entry, but only if it
// still points at gateID — so it doesn't clobber a newer gate in that thread.
func (b *SlackBot) clearPendingFreeform(threadTS, gateID string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.pendingFree[threadTS] == gateID {
		delete(b.pendingFree, threadTS)
	}
}

// gateActionPrefix namespaces our button action ids so unrelated interactions
// are ignored. Encoding is "gate|<index>|<gateID>" — the gateID is the trailing
// field so it round-trips even if it ever contains the "|" separator (the index
// only makes each button's action id unique and is not parsed back).
const gateActionPrefix = "gate"

func gateActionID(gateID string, i int) string {
	return fmt.Sprintf("%s|%d|%s", gateActionPrefix, i, gateID)
}

func parseGateAction(actionID string) (string, bool) {
	parts := strings.SplitN(actionID, "|", 3)
	if len(parts) < 3 || parts[0] != gateActionPrefix {
		return "", false
	}
	return parts[2], true
}

// slackThreadUI posts to one thread and renders gates as Block Kit.
type slackThreadUI struct {
	bot      *SlackBot
	channel  string
	threadTS string

	statusMu sync.Mutex
	statusTS string // the live status card's message ts (empty until first post)
}

// clearPending implements the pendingClearer capability: drop this thread's
// pending freeform entry when its gate stops waiting.
func (u *slackThreadUI) ClearPending(gateID string) {
	u.bot.clearPendingFreeform(u.threadTS, gateID)
}

func (u *slackThreadUI) Post(text string) error {
	_, _, err := u.bot.api.PostMessage(u.channel,
		slack.MsgOptionText(text, false),
		slack.MsgOptionTS(u.threadTS))
	return err
}

func (u *slackThreadUI) PostGate(g Gate) error {
	switch g.Kind {
	case GateChoice, GateYesNo:
		return u.postButtons(g)
	case GateFreeform:
		u.bot.setPendingFreeform(u.threadTS, g.ID)
		return u.Post("✍️ " + g.Prompt + "\n_Reply in this thread with your answer._")
	}
	return nil
}

func (u *slackThreadUI) postButtons(g Gate) error {
	buttons := make([]slack.BlockElement, 0, len(g.Choices))
	for i, choice := range g.Choices {
		btn := slack.NewButtonBlockElement(
			gateActionID(g.ID, i),
			choice, // carried back as BlockAction.Value
			slack.NewTextBlockObject(slack.PlainTextType, choice, false, false),
		)
		if choice == g.Default {
			btn.Style = slack.StylePrimary
		}
		buttons = append(buttons, btn)
	}
	prompt := slack.NewSectionBlock(
		slack.NewTextBlockObject(slack.MarkdownType, g.Prompt, false, false),
		nil, nil,
	)
	actions := slack.NewActionBlock(g.ID, buttons...)
	_, _, err := u.bot.api.PostMessage(u.channel,
		slack.MsgOptionBlocks(prompt, actions),
		slack.MsgOptionTS(u.threadTS))
	return err
}

// UpsertStatus implements chatops.StatusRenderer: post the live status card the
// first time, then edit that same message in place (chat.update) on every
// update — so the thread shows one dashboard that morphs as the run happens.
// The lock spans the API call so a concurrent update can't double-post the card.
func (u *slackThreadUI) UpsertStatus(card StatusCard) error {
	blocks := slack.MsgOptionBlocks(statusBlocks(card)...)
	u.statusMu.Lock()
	defer u.statusMu.Unlock()
	if u.statusTS == "" {
		_, ts, err := u.bot.api.PostMessage(u.channel, blocks, slack.MsgOptionTS(u.threadTS))
		if err != nil {
			return err
		}
		u.statusTS = ts
		return nil
	}
	_, _, _, err := u.bot.api.UpdateMessage(u.channel, u.statusTS, blocks)
	return err
}

// statusBlocks renders a StatusCard as Block Kit: a header line, a progress bar
// with the current node, and a context line with elapsed time and spend.
func statusBlocks(card StatusCard) []slack.Block {
	md := func(s string) *slack.TextBlockObject {
		return slack.NewTextBlockObject(slack.MarkdownType, s, false, false)
	}
	header := fmt.Sprintf("%s  *%s* · %s", stateEmoji(card.State), card.Workflow, card.State)

	progress := fmt.Sprintf("%s  %d/%d steps", progressBar(card.DoneCount, card.TotalCount), card.DoneCount, card.TotalCount)
	if card.CurrentNode != "" {
		progress += "\n└ " + card.CurrentNode
	}

	meta := "⏱ " + fmtDuration(card.Elapsed)
	switch {
	case card.BudgetUSD > 0:
		meta += fmt.Sprintf("   💸 $%.2f / $%.2f", card.CostUSD, card.BudgetUSD)
	case card.CostUSD > 0:
		meta += fmt.Sprintf("   💸 $%.2f", card.CostUSD)
	}

	return []slack.Block{
		slack.NewSectionBlock(md(header), nil, nil),
		slack.NewSectionBlock(md(progress), nil, nil),
		slack.NewContextBlock("", md(meta)),
	}
}

func stateEmoji(state string) string {
	switch state {
	case "success":
		return "✅"
	case "fail":
		return "❌"
	case "budget_exceeded":
		return "🛑"
	case "validation_overridden":
		return "☑️"
	default:
		return "🟢"
	}
}

// progressBar draws a fixed-width unicode meter.
func progressBar(done, total int) string {
	const width = 12
	if total <= 0 {
		return strings.Repeat("░", width)
	}
	filled := done * width / total
	if filled > width {
		filled = width
	}
	return strings.Repeat("▓", filled) + strings.Repeat("░", width-filled)
}

func fmtDuration(d time.Duration) string {
	d = d.Round(time.Second)
	if m := int(d / time.Minute); m > 0 {
		return fmt.Sprintf("%dm %02ds", m, int((d%time.Minute)/time.Second))
	}
	return fmt.Sprintf("%ds", int(d/time.Second))
}
