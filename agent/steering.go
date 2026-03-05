// ABOUTME: Mid-session steering allows injecting instructions into an active agent loop.
// ABOUTME: Steering messages are checked between turns via a non-blocking channel read.
package agent

// WithSteering attaches a steering channel to receive mid-session instructions.
func WithSteering(ch <-chan string) SessionOption {
	return func(s *Session) {
		s.steering = ch
	}
}
