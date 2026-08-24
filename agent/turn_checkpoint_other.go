// ABOUTME: Non-unix stub for turnCheckpointNoFollow — Windows lacks O_NOFOLLOW.
//go:build !unix

package agent

// turnCheckpointNoFollow is a no-op on platforms that lack O_NOFOLLOW.
const turnCheckpointNoFollow = 0
