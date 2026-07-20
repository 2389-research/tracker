// ABOUTME: Entry point for trackerbot — a Slack front-end that drives Tracker pipelines.
// ABOUTME: Socket Mode transport, run manager, and event wiring are added incrementally.
package main

import (
	"fmt"
	"os"
)

func main() {
	// The Slack Socket Mode transport is wired up in a later step. Until then
	// this binary is a scaffold for the transport-neutral bot logic (gate
	// bridge, run manager, intent, notify, delivery) built and tested here.
	fmt.Fprintln(os.Stderr, "trackerbot: transport not yet wired — see cmd/trackerbot")
}
