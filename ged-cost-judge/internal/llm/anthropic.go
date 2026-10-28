package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/anthropics/anthropic-sdk-go/v2"
	"github.com/anthropics/anthropic-sdk-go/v2/option"
)

// DefaultAnthropicMaxTokens is the max_tokens used when neither the Request nor the config sets one.
// Thinking tokens count toward max_tokens, so it is generous; only generated tokens are billed.
const DefaultAnthropicMaxTokens = 16000

// AnthropicConfig configures the Anthropic client.
type AnthropicConfig struct {
	APIKey  string
	BaseURL string // empty means the official endpoint (tests point it at an httptest server)
	// Model is used when Request.Model is empty.
	Model string
	// MaxTokens is used when Request.MaxTokens is 0. 0 means DefaultAnthropicMaxTokens.
	MaxTokens int64
	// Thinking / Effort are used when the Request leaves them empty (values as in Request).
	// They are filled in by ApplyDefaults, which CachingClient calls before computing the cache
	// key, so the effective setting is always part of the key.
	Thinking string
	Effort   string
	// CacheSystemPrompt puts a prompt-cache breakpoint (5-minute TTL) on the system prompt.
	// It changes cost, not output, so it is not part of the cache key. Prompts shorter than the
	// model's minimum cacheable length are silently not cached.
	CacheSystemPrompt bool
	// MaxRetries is the number of SDK built-in retries (retry-after is honored).
	// 0 keeps the SDK default (2); a negative value disables them.
	MaxRetries int
	Timeout    time.Duration
	// HTTPClient replaces the SDK's HTTP client (optional).
	HTTPClient *http.Client
	// Pricing is the cost estimation table. nil means DefaultPricing.
	Pricing *PriceTable

	// BatchStateDir is where submitted Message Batches are recorded (<dir>/<batch-id>.json), so an
	// interrupted run resumes them instead of resubmitting. Empty keeps the record in memory only
	// (safe within one process, lost on exit). Use a directory that is not a cache tag.
	BatchStateDir string
	// PollInterval is the first wait between batch status polls (default 30s); it grows ×1.5 per
	// poll up to MaxPollInterval (default 5m).
	PollInterval    time.Duration
	MaxPollInterval time.Duration
	// PollFailureTimeout is how long polling keeps retrying through consecutive transient failures
	// (network errors, 429, 5xx) before giving up (default 30m). The batch stays recorded either way.
	PollFailureTimeout time.Duration
	// BatchSubmitTimeout bounds one batch upload (default 30m). Submissions are never retried by
	// the SDK (the Batches API has no idempotency key); see submitBatch.
	BatchSubmitTimeout time.Duration
	// MaxBatchRequests / MaxBatchBytes split large submissions. Defaults are the API limit on
	// requests (100,000) and 32 MB per upload (the API allows 256 MB; small uploads make a lost
	// response, the only case that can create an untracked batch, cheap and rare).
	MaxBatchRequests int
	MaxBatchBytes    int
}

// AnthropicClient is the Anthropic (Messages API / Message Batches) implementation of Client and BatchClient.
//
// Requests are sent without sampling parameters: Claude 5.x models reject temperature / top_p /
// top_k (and so does the API version the SDK pins), so a Request with Temperature or Seed set is an
// error. Reproducibility comes from the response cache, not from sampling settings.
type AnthropicClient struct {
	api     anthropic.Client
	cfg     AnthropicConfig
	pricing *PriceTable

	mu     sync.Mutex
	states map[string]*batchState // batches submitted or resumed by this process
}

var (
	_ Client             = (*AnthropicClient)(nil)
	_ StoringBatchClient = (*AnthropicClient)(nil)
	_ RequestDefaulter   = (*AnthropicClient)(nil)
)

// NewAnthropic creates an Anthropic client. An empty APIKey is an error
// (relying on implicit credential discovery means a missing key is noticed only late in a run).
func NewAnthropic(cfg AnthropicConfig) (*AnthropicClient, error) {
	if cfg.APIKey == "" {
		return nil, errors.New("llm: ANTHROPIC_API_KEY is not set")
	}
	// Validate the defaults with the same rules as a Request.
	probe := Request{Model: "probe", Messages: []Message{User("probe")}, Thinking: cfg.Thinking, Effort: cfg.Effort}
	if err := probe.Validate(); err != nil {
		return nil, fmt.Errorf("llm: anthropic config: %w", err)
	}
	opts := []option.RequestOption{option.WithAPIKey(cfg.APIKey)}
	if cfg.BaseURL != "" {
		opts = append(opts, option.WithBaseURL(cfg.BaseURL))
	}
	switch {
	case cfg.MaxRetries > 0:
		opts = append(opts, option.WithMaxRetries(cfg.MaxRetries))
	case cfg.MaxRetries < 0:
		opts = append(opts, option.WithMaxRetries(0))
	}
	if cfg.Timeout > 0 {
		opts = append(opts, option.WithRequestTimeout(cfg.Timeout))
	}
	if cfg.HTTPClient != nil {
		opts = append(opts, option.WithHTTPClient(cfg.HTTPClient))
	}
	pricing := cfg.Pricing
	if pricing == nil {
		pricing = DefaultPricing()
	}
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = 30 * time.Second
	}
	if cfg.MaxPollInterval <= 0 {
		cfg.MaxPollInterval = 5 * time.Minute
	}
	if cfg.MaxPollInterval < cfg.PollInterval {
		cfg.MaxPollInterval = cfg.PollInterval
	}
	if cfg.MaxBatchRequests <= 0 || cfg.MaxBatchRequests > anthropicBatchRequestLimit {
		cfg.MaxBatchRequests = anthropicBatchRequestLimit
	}
	if cfg.MaxBatchBytes <= 0 {
		cfg.MaxBatchBytes = defaultBatchBytes
	}
	if cfg.PollFailureTimeout <= 0 {
		cfg.PollFailureTimeout = 30 * time.Minute
	}
	if cfg.BatchSubmitTimeout <= 0 {
		cfg.BatchSubmitTimeout = 30 * time.Minute
	}
	return &AnthropicClient{
		api:     anthropic.NewClient(opts...),
		cfg:     cfg,
		pricing: pricing,
		states:  map[string]*batchState{},
	}, nil
}

// ApplyDefaults fills Model, MaxTokens, Thinking and Effort from the config when the Request leaves
// them empty.
func (c *AnthropicClient) ApplyDefaults(req Request) Request {
	if req.Model == "" {
		req.Model = c.cfg.Model
	}
	if req.MaxTokens == 0 {
		req.MaxTokens = c.cfg.MaxTokens
		if req.MaxTokens == 0 {
			req.MaxTokens = DefaultAnthropicMaxTokens
		}
	}
	if req.Thinking == "" {
		req.Thinking = c.cfg.Thinking
	}
	if req.Effort == "" {
		req.Effort = c.cfg.Effort
	}
	return req
}

func (c *AnthropicClient) Complete(ctx context.Context, req Request) (*Response, error) {
	req = c.ApplyDefaults(req)
	p, err := c.buildParams(req)
	if err != nil {
		return nil, err
	}
	start := time.Now()
	msg, err := c.api.Messages.New(ctx, p.messageNew())
	latency := time.Since(start)
	if err != nil {
		return nil, wrapAnthropicError(err)
	}
	resp, err := c.toResponse(msg, false)
	if err != nil {
		return nil, err
	}
	resp.LatencyMS = latency.Milliseconds()
	return resp, nil
}

// anthropicParams holds the request fields shared by Messages.New and a Message Batches item
// (the SDK uses two distinct parameter structs for them).
type anthropicParams struct {
	model     string
	maxTokens int64
	system    []anthropic.TextBlockParam
	messages  []anthropic.MessageParam
	thinking  anthropic.ThinkingConfigParamUnion
	output    anthropic.OutputConfigParam
}

func (p anthropicParams) messageNew() anthropic.MessageNewParams {
	return anthropic.MessageNewParams{
		Model:        p.model,
		MaxTokens:    p.maxTokens,
		System:       p.system,
		Messages:     p.messages,
		Thinking:     p.thinking,
		OutputConfig: p.output,
	}
}

func (p anthropicParams) batchItem() anthropic.MessageBatchNewParamsRequestParams {
	return anthropic.MessageBatchNewParamsRequestParams{
		Model:        p.model,
		MaxTokens:    p.maxTokens,
		System:       p.system,
		Messages:     p.messages,
		Thinking:     p.thinking,
		OutputConfig: p.output,
	}
}

// buildParams maps a (defaulted) Request onto the Messages API.
//
//   - system messages → the top-level system prompt (one text block each, in order)
//   - user messages → one user turn (one text block each; the API would merge consecutive user
//     turns anyway, this keeps the request explicit)
//   - Schema → output_config.format (JSON schema structured output). Name / Description / Strict are
//     OpenAI concepts; Anthropic constrains decoding to the schema itself. The schema must satisfy
//     the API's subset (additionalProperties: false on every object, no numeric/string-length
//     constraints, no recursion), which SchemaFor already produces.
//   - Effort → output_config.effort; Thinking → thinking (see thinkingParam)
func (c *AnthropicClient) buildParams(req Request) (anthropicParams, error) {
	if err := req.Validate(); err != nil {
		return anthropicParams{}, err
	}
	if req.Temperature != nil || req.Seed != nil {
		return anthropicParams{}, fmt.Errorf("llm: anthropic: %s does not accept temperature/seed; leave them nil", req.Model)
	}
	if req.MaxTokens <= 0 {
		return anthropicParams{}, fmt.Errorf("llm: anthropic: max_tokens must be positive, got %d", req.MaxTokens)
	}
	p := anthropicParams{model: req.Model, maxTokens: req.MaxTokens}
	var userBlocks []anthropic.ContentBlockParamUnion
	for _, m := range req.Messages {
		switch m.Role {
		case RoleSystem:
			p.system = append(p.system, anthropic.TextBlockParam{Text: m.Content})
		case RoleUser:
			userBlocks = append(userBlocks, anthropic.NewTextBlock(m.Content))
		default:
			return anthropicParams{}, fmt.Errorf("llm: unknown role %q", m.Role)
		}
	}
	if len(userBlocks) == 0 {
		return anthropicParams{}, errors.New("llm: anthropic: request has no user message")
	}
	p.messages = []anthropic.MessageParam{anthropic.NewUserMessage(userBlocks...)}
	if c.cfg.CacheSystemPrompt && len(p.system) > 0 {
		p.system[len(p.system)-1].CacheControl = anthropic.NewCacheControlEphemeralParam()
	}

	thinking, err := thinkingParam(req.Model, req.Thinking, req.Effort)
	if err != nil {
		return anthropicParams{}, err
	}
	p.thinking = thinking
	if req.Effort != "" {
		p.output.Effort = anthropic.OutputConfigEffort(req.Effort)
	}
	if req.Schema != nil {
		var schema map[string]any
		if err := json.Unmarshal(req.Schema.JSON, &schema); err != nil {
			return anthropicParams{}, fmt.Errorf("llm: invalid json schema %q: %w", req.Schema.Name, err)
		}
		p.output.Format = anthropic.JSONOutputFormatParam{Schema: schema}
	}
	return p, nil
}

// thinkingParam maps Request.Thinking onto the per-model thinking configuration.
//
//   - "" → omitted: the model default (adaptive thinking on Claude 5.x).
//   - "adaptive" → {type: "adaptive"} (budget_tokens is rejected on Claude 5.x and never sent).
//   - "off" → {type: "disabled"}, except Sonnet 5.5, which rejects "disabled" and turns thinking
//     off with {type: "between_tools"}. Opus 5.5 / Fable / Mythos cannot turn thinking off.
//     Turning thinking off is accepted only at effort high or below.
func thinkingParam(model, thinking, effort string) (anthropic.ThinkingConfigParamUnion, error) {
	switch thinking {
	case "":
		return anthropic.ThinkingConfigParamUnion{}, nil
	case ThinkingAdaptive:
		return anthropic.ThinkingConfigParamUnion{OfAdaptive: &anthropic.ThinkingConfigAdaptiveParam{}}, nil
	case ThinkingOff:
		if effort == "xhigh" || effort == "max" {
			return anthropic.ThinkingConfigParamUnion{}, fmt.Errorf("llm: anthropic: thinking off requires effort high or below, got %q", effort)
		}
		switch {
		case strings.HasPrefix(model, "claude-sonnet-5-5"):
			return anthropic.ThinkingConfigParamUnion{OfBetweenTools: &anthropic.ThinkingConfigBetweenToolsParam{}}, nil
		case strings.HasPrefix(model, "claude-opus-5-5"),
			strings.HasPrefix(model, "claude-fable-"),
			strings.HasPrefix(model, "claude-mythos-"):
			return anthropic.ThinkingConfigParamUnion{}, fmt.Errorf("llm: anthropic: thinking cannot be turned off on %s (lower Effort instead)", model)
		default:
			return anthropic.ThinkingConfigParamUnion{OfDisabled: &anthropic.ThinkingConfigDisabledParam{}}, nil
		}
	default:
		return anthropic.ThinkingConfigParamUnion{}, fmt.Errorf("llm: unknown thinking mode %q", thinking)
	}
}

// toResponse converts an API message. batch selects batch pricing (BatchDiscount).
//
// A refusal (stop_reason "refusal", billed) is returned as a Response with FinishReason
// FinishRefusal and empty Text, so it is cached and recorded like any other judgment; the category
// and explanation are in Raw (stop_details). CompleteJSON reports it as ErrRefusal.
func (c *AnthropicClient) toResponse(msg *anthropic.Message, batch bool) (*Response, error) {
	// Thinking blocks are kept only in Raw; Text is the concatenated visible text
	// (one block with the JSON document when structured output is used).
	var text strings.Builder
	if msg.StopReason != anthropic.StopReasonRefusal {
		for _, block := range msg.Content {
			if block.Type == "text" {
				text.WriteString(block.Text)
			}
		}
	}
	u := msg.Usage
	usage := Usage{
		// Anthropic's input_tokens excludes cache reads/writes; sum them so that InputTokens is
		// the total prompt size as for OpenAI (ChatCost relies on this).
		InputTokens:           u.InputTokens + u.CacheReadInputTokens + u.CacheCreationInputTokens,
		CachedInputTokens:     u.CacheReadInputTokens,
		CacheWriteInputTokens: u.CacheCreationInputTokens,
		OutputTokens:          u.OutputTokens,
	}
	if batch {
		usage.CostUSD = c.pricing.ChatCostBatch(msg.Model, usage)
	} else {
		usage.CostUSD = c.pricing.ChatCost(msg.Model, usage)
	}
	raw := json.RawMessage(msg.RawJSON())
	if len(raw) == 0 {
		b, err := json.Marshal(msg)
		if err != nil {
			return nil, fmt.Errorf("llm: marshal anthropic message: %w", err)
		}
		raw = b
	}
	return &Response{
		Text:         text.String(),
		Model:        msg.Model,
		FinishReason: string(msg.StopReason),
		Usage:        usage,
		Raw:          raw,
	}, nil
}

// wrapAnthropicError converts SDK errors into *APIError so that IsRetryable works
// (429, 5xx and 529 overloaded are retryable; 4xx are not).
func wrapAnthropicError(err error) error {
	var apiErr *anthropic.Error
	if errors.As(err, &apiErr) {
		typ, msg := parseAnthropicErrorBody(apiErr.RawJSON())
		if typ == "" {
			typ = string(apiErr.Type())
		}
		if msg == "" {
			msg = http.StatusText(apiErr.StatusCode)
		}
		return &APIError{StatusCode: apiErr.StatusCode, Type: typ, Message: msg, Err: err}
	}
	return fmt.Errorf("llm: anthropic request failed: %w", err)
}

// parseAnthropicErrorBody extracts error.type / error.message from an API error body.
func parseAnthropicErrorBody(body string) (typ, msg string) {
	var parsed struct {
		Error struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if body == "" || json.Unmarshal([]byte(body), &parsed) != nil {
		return "", ""
	}
	return parsed.Error.Type, parsed.Error.Message
}

// anthropicErrorStatus maps an API error type to its HTTP status (used for batch items, which
// report only the type).
func anthropicErrorStatus(typ string) int {
	switch typ {
	case "invalid_request_error":
		return http.StatusBadRequest
	case "authentication_error":
		return http.StatusUnauthorized
	case "billing_error":
		return http.StatusPaymentRequired
	case "permission_error":
		return http.StatusForbidden
	case "not_found_error":
		return http.StatusNotFound
	case "request_too_large":
		return http.StatusRequestEntityTooLarge
	case "rate_limit_error":
		return http.StatusTooManyRequests
	case "api_error":
		return http.StatusInternalServerError
	case "timeout_error":
		return http.StatusGatewayTimeout
	case "overloaded_error":
		return 529
	}
	return 0
}
