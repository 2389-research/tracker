// ABOUTME: The Slack Socket Mode transport — the only file that imports slack-go.
// ABOUTME: One WebSocket in; demultiplexes events to the Runner; renders gates as Block Kit.
package main

import (
	"context"
	"fmt"
	"strings"
	"sync"

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

	mu          sync.Mutex
	pendingFree map[string]string // thread_ts → pending freeform gate id
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
	api, ok := evt.Data.(slackevents.EventsAPIEvent)
	if !ok {
		return
	}
	if evt.Request != nil {
		_ = b.sm.Ack(*evt.Request)
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
	cb, ok := evt.Data.(slack.InteractionCallback)
	if !ok {
		return
	}
	if evt.Request != nil {
		_ = b.sm.Ack(*evt.Request)
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

// gateActionPrefix namespaces our button action ids so unrelated interactions
// are ignored. Encoding is "gate|<gateID>|<index>".
const gateActionPrefix = "gate"

func gateActionID(gateID string, i int) string {
	return fmt.Sprintf("%s|%s|%d", gateActionPrefix, gateID, i)
}

func parseGateAction(actionID string) (string, bool) {
	parts := strings.SplitN(actionID, "|", 3)
	if len(parts) < 2 || parts[0] != gateActionPrefix {
		return "", false
	}
	return parts[1], true
}

// slackThreadUI posts to one thread and renders gates as Block Kit.
type slackThreadUI struct {
	bot      *SlackBot
	channel  string
	threadTS string
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
