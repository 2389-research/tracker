package tracker_test

import (
	"bytes"
	"context"
	"fmt"

	tracker "github.com/2389-research/tracker"
)

func ExampleDiagnose() {
	report, err := tracker.Diagnose(context.Background(), "testdata/runs/failed")
	if err != nil {
		fmt.Println("diagnose failed")
		return
	}
	fmt.Println("failed node:", report.Failures[0].NodeID)
	fmt.Println("retry count:", report.Failures[0].RetryCount)
	// Output:
	// failed node: Build
	// retry count: 2
}

func ExampleDoctor() {
	report, err := tracker.Doctor(context.Background(), tracker.DoctorConfig{
		WorkDir: ".",
	})
	if err != nil {
		fmt.Println("doctor failed")
		return
	}
	fmt.Println("checks:", len(report.Checks) > 0)
	// Output:
	// checks: true
}

func ExampleNewNDJSONWriter() {
	var buf bytes.Buffer
	w := tracker.NewNDJSONWriter(&buf)
	_ = w.Write(tracker.StreamEvent{
		Timestamp: "2026-04-17T10:00:00.000Z",
		Source:    "pipeline",
		Type:      "pipeline_started",
		RunID:     "run-123",
	})
	fmt.Println(buf.Len() > 0)
	// Output:
	// true
}

