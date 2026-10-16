// ABOUTME: The provider label a session's usage is accounted under: the client's usage label for the
// ABOUTME: configured provider (openai-subscription for openai on a ChatGPT subscription), else the provider.
package agent

import "github.com/2389-research/tracker/llm"

// usageLabeler is the optional usage-label capability of the concrete LLM
// client (*llm.Client). Type-asserted so the base Completer interface stays
// minimal and test stubs need not implement it.
type usageLabeler interface {
	UsageLabel(provider string) string
}

// The concrete client must provide it, so real runs always get the label.
var _ usageLabeler = (*llm.Client)(nil)

// usageLabel returns the provider name SessionResult.Provider carries, which
// session stats and the trace's per-provider usage totals bucket under: the
// client's label for the configured provider (openai-subscription when openai
// runs on a ChatGPT subscription, priced at $0), or the configured provider
// when the client gives no label. Routing never reads it; requests still name
// s.config.Provider.
func (s *Session) usageLabel() string {
	if labeler, ok := s.client.(usageLabeler); ok {
		if label := labeler.UsageLabel(s.config.Provider); label != "" {
			return label
		}
	}
	return s.config.Provider
}
