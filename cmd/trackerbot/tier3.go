// ABOUTME: Slack Tier-3 surfaces — `/tracker` slash command and the App Home tab.
// ABOUTME: Both reuse the Runner; only the Slack-specific wiring lives here.
//
// These two surfaces require extra Slack app configuration and a live workspace
// to verify end-to-end (see cmd/trackerbot/README.md § "Slash command & App
// Home"): a registered slash command, the App Home tab enabled, and the
// `commands` scope. The pure view builder (homeBlocks) is unit-tested; the event
// plumbing is exercised against a real workspace.
package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"
	"github.com/slack-go/slack/socketmode"
)

// onSlashCommand handles `/tracker <text>`. A slash command has no thread, so we
// open one by posting the request into the channel and using that opener
// message's ts as the run's identity — then route exactly like an @mention, so
// all the Runner's commands and gating apply unchanged.
func (b *SlackBot) onSlashCommand(ctx context.Context, evt socketmode.Event) {
	if evt.Request != nil {
		_ = b.sm.Ack(*evt.Request) // ack fast; progress posts in-thread
	}
	cmd, ok := evt.Data.(slack.SlashCommand)
	if !ok {
		return
	}
	text := strings.TrimSpace(cmd.Text)
	if text == "" {
		_ = b.postEphemeral(cmd, fmt.Sprintf("Usage: `%s <what you want>` — e.g. `%s run build_product`.", cmd.Command, cmd.Command))
		return
	}
	if !b.authorized(cmd.UserID) {
		_ = b.postEphemeral(cmd, "Sorry — you're not on the allowlist to run pipelines here.")
		return
	}
	// The opener message roots the thread; its ts is the run identity.
	_, ts, err := b.api.PostMessage(cmd.ChannelID,
		slack.MsgOptionText(fmt.Sprintf("<@%s> ran `%s %s`", cmd.UserID, cmd.Command, text), false))
	if err != nil {
		_ = b.postEphemeral(cmd, "Couldn't start a thread: "+err.Error())
		return
	}
	go b.runner.OnMention(ctx, cmd.ChannelID, ts, text)
}

// postEphemeral shows a message only to the slash-command invoker (usage, denial).
func (b *SlackBot) postEphemeral(cmd slack.SlashCommand, text string) error {
	_, err := b.api.PostEphemeral(cmd.ChannelID, cmd.UserID, slack.MsgOptionText(text, false))
	return err
}

// onAppHomeOpened publishes the bot's Home tab: a short how-to plus a live view
// of active runs. Republished each time the user opens Home, so it's current.
func (b *SlackBot) onAppHomeOpened(ev *slackevents.AppHomeOpenedEvent) {
	if ev.Tab != "home" {
		return
	}
	view := slack.HomeTabViewRequest{
		Type:   slack.VTHomeTab,
		Blocks: slack.Blocks{BlockSet: homeBlocks(b.runner.ActiveRuns())},
	}
	_, _ = b.api.PublishView(ev.User, view, "")
}

// homeBlocks builds the App Home Block Kit view: a header, a how-to, and the
// current active-run list (or an empty-state line). Pure — unit-tested.
func homeBlocks(runs []RunView) []slack.Block {
	md := func(s string) *slack.TextBlockObject {
		return slack.NewTextBlockObject(slack.MarkdownType, s, false, false)
	}
	section := func(s string) slack.Block { return slack.NewSectionBlock(md(s), nil, nil) }

	blocks := []slack.Block{
		slack.NewHeaderBlock(slack.NewTextBlockObject(slack.PlainTextType, "🛰️  trackerbot", false, false)),
		section("Mention me in any channel — `@trackerbot make me a CLI that greets people` — or run `/tracker <what you want>`. I execute the pipeline in a thread and keep you posted there."),
		section("*Commands:* `retry` · `bump <dollars>` · `steer <guidance>` · `status` · `cancel` · `runs` · `workflows`"),
		slack.NewDividerBlock(),
	}
	if len(runs) == 0 {
		return append(blocks, section("*Active runs:* none right now."))
	}
	blocks = append(blocks, section("*Active runs*"))
	for _, r := range runs {
		blocks = append(blocks, section(fmt.Sprintf("• `%s` — %s", r.Key, r.State)))
	}
	return blocks
}
