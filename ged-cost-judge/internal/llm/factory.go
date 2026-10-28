package llm

import (
	"fmt"
	"os"
	"strings"
	"time"
)

// ClientConfig holds the provider-independent settings for NewClientForModel.
type ClientConfig struct {
	// MaxRetries is the number of SDK built-in retries (0 keeps the SDK default).
	MaxRetries int
	Timeout    time.Duration
	// Pricing is the cost estimation table. nil means DefaultPricing.
	Pricing *PriceTable
	// BatchStateDir is passed to AnthropicConfig.BatchStateDir (ignored for OpenAI).
	// Use a directory that is not a cache tag, e.g. ".cache/llm-batches".
	BatchStateDir string
	// Thinking / Effort are the defaults for requests that leave them empty (values as in Request).
	// They become part of the cache key, so a pre-registered run should always set Effort: the
	// server-side default differs per model (Sonnet 5.5: high, Haiku 5.5: medium) and may change.
	// Thinking is Anthropic-only; setting it for a gpt- model is an error.
	Thinking string
	Effort   string
}

// NewClientForModel returns the client for model's provider: "claude-*" → Anthropic,
// "gpt-*" → OpenAI. The API key is read from ANTHROPIC_API_KEY / OPENAI_API_KEY; the caller is
// responsible for loading .env first. The returned Anthropic client also implements BatchClient.
func NewClientForModel(model string, cfg ClientConfig) (Client, error) {
	// Each branch returns a nil interface on error (not a typed nil pointer).
	switch {
	case strings.HasPrefix(model, "claude-"):
		c, err := NewAnthropic(AnthropicConfig{
			APIKey:        os.Getenv("ANTHROPIC_API_KEY"),
			Model:         model,
			MaxRetries:    cfg.MaxRetries,
			Timeout:       cfg.Timeout,
			Pricing:       cfg.Pricing,
			BatchStateDir: cfg.BatchStateDir,
			Thinking:      cfg.Thinking,
			Effort:        cfg.Effort,
		})
		if err != nil {
			return nil, err
		}
		return c, nil
	case strings.HasPrefix(model, "gpt-"):
		if cfg.Thinking != "" {
			return nil, fmt.Errorf("llm: Thinking is not supported for %s (use Effort)", model)
		}
		c, err := NewOpenAI(OpenAIConfig{
			APIKey:     os.Getenv("OPENAI_API_KEY"),
			MaxRetries: cfg.MaxRetries,
			Timeout:    cfg.Timeout,
			Pricing:    cfg.Pricing,
			Effort:     cfg.Effort,
		})
		if err != nil {
			return nil, err
		}
		return c, nil
	default:
		return nil, fmt.Errorf("llm: no provider for model %q (expected a claude- or gpt- prefix)", model)
	}
}
