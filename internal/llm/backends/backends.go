// Package backends wires the wire implementations into the llm.Wrapper: Anthropic Messages for Anthropic providers,
// OpenAI-compatible chat completions for everything else. It is a separate package so llm does not import its own
// implementations.
package backends

import (
	"net/http"

	"github.com/kjmaguire/drydock/internal/llm"
	"github.com/kjmaguire/drydock/internal/llm/anthropic"
	"github.com/kjmaguire/drydock/internal/llm/openai"
	"github.com/kjmaguire/drydock/internal/providers"
)

// Factory is the llm.BackendFactory for the registry's providers.
func Factory(p providers.Provider, apiKey string, hc *http.Client) (llm.Client, error) {
	if p.Flavor == providers.FlavorAnthropic {
		return anthropic.New(p, apiKey, hc)
	}
	return openai.New(p, apiKey, hc)
}
