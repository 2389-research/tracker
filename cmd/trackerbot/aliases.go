// ABOUTME: Aliases the transport-neutral chatops types into package main so the
// ABOUTME: Slack transport (slack.go) and wiring (main.go) read cleanly.
package main

import chatops "github.com/2389-research/tracker/transport/chatops"

type (
	Gate           = chatops.Gate
	GateAnswer     = chatops.GateAnswer
	GateKind       = chatops.GateKind
	ThreadUI       = chatops.ThreadUI
	Runner         = chatops.Runner
	RunnerDeps     = chatops.RunnerDeps
	RunRecord      = chatops.RunRecord
	IntentResolver = chatops.IntentResolver
)

const (
	GateChoice   = chatops.GateChoice
	GateYesNo    = chatops.GateYesNo
	GateFreeform = chatops.GateFreeform
)
