// Package chatops is the transport-neutral core of a chat-shaped Tracker
// front-end: session-to-run routing (Runner), the human-gate interviewer
// (ThreadInterviewer over a ThreadUI seam), the event-to-notification filter
// (notifier), result delivery, durable resume (Store), and free-text intent
// resolution. A concrete transport (Slack, and later Discord/Teams/Email)
// supplies a ThreadUI plus its inbound event loop and reuses everything here —
// so a new chat transport is a ThreadUI + auth, not a rewrite.
//
// See docs/plans/2026-07-21-transport-implementation-plans.md (Phase 0).
package chatops
