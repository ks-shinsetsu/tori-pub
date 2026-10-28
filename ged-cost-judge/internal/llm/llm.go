// Package llm centralizes access to LLMs and embedding models.
//
// Conventions:
//   - Other packages must not import the openai or anthropic SDKs directly. Always go through this Client.
//   - Structured output always uses JSON Schema constraints (OpenAI Structured Outputs,
//     Anthropic output_config.format).
//   - Raw responses (Response.Raw) are never discarded (they remain in the cache as-is).
package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

// Role is a message role.
type Role string

const (
	RoleSystem Role = "system"
	RoleUser   Role = "user"
)

// Message is a single message.
type Message struct {
	Role    Role   `json:"role"`
	Content string `json:"content"`
}

func System(content string) Message { return Message{Role: RoleSystem, Content: content} }
func User(content string) Message   { return Message{Role: RoleUser, Content: content} }

// Schema is the JSON Schema passed to Structured Outputs.
type Schema struct {
	// Name may contain only a-z / A-Z / 0-9 / _ / -, up to 64 characters (an OpenAI constraint).
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Strict      bool            `json:"strict"`
	JSON        json.RawMessage `json:"json"`
}

// Request is a single completion request. The cache key is derived from this struct, so
// it should contain only values for which "same Request means same result" is desired.
type Request struct {
	Model    string    `json:"model"`
	Messages []Message `json:"messages"`
	// Temperature / Seed are unspecified when nil (reasoning models do not accept a temperature).
	// They must stay nil for Anthropic models: Claude 5.x models (and the API version the SDK
	// pins) reject sampling parameters, so the Anthropic client returns an error if they are set.
	Temperature *float64 `json:"temperature,omitempty"`
	Seed        *int64   `json:"seed,omitempty"`
	MaxTokens   int64    `json:"max_tokens,omitempty"`
	// If Schema is non-nil, Structured Outputs is used.
	Schema *Schema `json:"schema,omitempty"`
	// Thinking is the provider-neutral reasoning switch: "" (provider default), ThinkingAdaptive or
	// ThinkingOff. Only the Anthropic client accepts a non-empty value. Part of the cache key.
	Thinking string `json:"thinking,omitempty"`
	// Effort is the reasoning effort: "" (provider default) or one of low / medium / high / xhigh /
	// max (Anthropic output_config.effort, OpenAI reasoning_effort). Part of the cache key.
	// The provider default differs per model (Sonnet 5.5: high, Haiku 5.5: medium), so set it explicitly.
	Effort string `json:"effort,omitempty"`
	// Tag is the purpose name ("aif_relation", etc.).
	// Used as the cache namespace and the cost-aggregation label. It does not affect the result.
	Tag string `json:"-"`
}

// Values of Request.Thinking.
const (
	ThinkingAdaptive = "adaptive"
	ThinkingOff      = "off"
)

// validEfforts are the accepted Request.Effort values (the Anthropic effort levels).
var validEfforts = map[string]bool{"": true, "low": true, "medium": true, "high": true, "xhigh": true, "max": true}

func (r Request) Validate() error {
	if r.Model == "" {
		return errors.New("llm: model is empty")
	}
	if len(r.Messages) == 0 {
		return errors.New("llm: messages is empty")
	}
	switch r.Thinking {
	case "", ThinkingAdaptive, ThinkingOff:
	default:
		return fmt.Errorf("llm: unknown thinking mode %q", r.Thinking)
	}
	if !validEfforts[r.Effort] {
		return fmt.Errorf("llm: unknown effort %q", r.Effort)
	}
	return nil
}

// Usage is token usage and estimated cost.
type Usage struct {
	// InputTokens is the total prompt size, including cache reads and writes, for both providers
	// (OpenAI prompt_tokens; Anthropic input_tokens + cache_read + cache_creation).
	InputTokens int64 `json:"input_tokens"`
	// CachedInputTokens are input tokens read from the provider's prompt cache
	// (OpenAI cached_tokens, Anthropic cache_read_input_tokens). Included in InputTokens.
	CachedInputTokens int64 `json:"cached_input_tokens"`
	// CacheWriteInputTokens are input tokens written to the prompt cache
	// (Anthropic cache_creation_input_tokens). Included in InputTokens.
	CacheWriteInputTokens int64   `json:"cache_write_input_tokens,omitempty"`
	OutputTokens          int64   `json:"output_tokens"`
	CostUSD               float64 `json:"cost_usd"`
}

func (u Usage) Add(other Usage) Usage {
	return Usage{
		InputTokens:           u.InputTokens + other.InputTokens,
		CachedInputTokens:     u.CachedInputTokens + other.CachedInputTokens,
		CacheWriteInputTokens: u.CacheWriteInputTokens + other.CacheWriteInputTokens,
		OutputTokens:          u.OutputTokens + other.OutputTokens,
		CostUSD:               u.CostUSD + other.CostUSD,
	}
}

// Response is a completion result.
type Response struct {
	Text         string          `json:"text"`
	Model        string          `json:"model"`
	FinishReason string          `json:"finish_reason"`
	Usage        Usage           `json:"usage"`
	Raw          json.RawMessage `json:"raw,omitempty"`
	// Cached is true when returned from the local cache (cost is treated as 0).
	Cached    bool  `json:"cached"`
	LatencyMS int64 `json:"latency_ms"`
}

// FinishRefusal is Response.FinishReason when the model declined to answer (Anthropic stop_reason
// "refusal", OpenAI message.refusal). Such a response is billed, so it is returned (and cached)
// like any other; CompleteJSON turns it into ErrRefusal.
const FinishRefusal = "refusal"

var (
	// ErrRefusal is reported (by FinishError / CompleteJSON) when the model declined to answer.
	ErrRefusal = errors.New("llm: model refused to answer")
	// ErrTruncated is reported when the output stopped at max_tokens.
	ErrTruncated = errors.New("llm: output truncated at max_tokens")
)

// Truncated reports whether generation stopped at the token limit
// (Anthropic "max_tokens", OpenAI "length"). Truncated responses are never cached.
func (r *Response) Truncated() bool {
	return r.FinishReason == "max_tokens" || r.FinishReason == "length"
}

// FinishError returns ErrRefusal or ErrTruncated (wrapped with the model name) when the response
// cannot hold a complete answer, and nil otherwise. Neither is retryable as such: a refusal is
// deterministic enough to keep, and a truncation needs a larger MaxTokens.
func (r *Response) FinishError() error {
	switch {
	case r.FinishReason == FinishRefusal:
		return fmt.Errorf("%w (model %s; details in Response.Raw)", ErrRefusal, r.Model)
	case r.Truncated():
		return fmt.Errorf("%w (model %s, finish_reason %s; raise MaxTokens)", ErrTruncated, r.Model, r.FinishReason)
	}
	return nil
}

// Client abstracts LLM completion. Implementations are OpenAI and Anthropic plus caching and
// rate-control decorators.
type Client interface {
	Complete(ctx context.Context, req Request) (*Response, error)
}

// BatchResult is the outcome of one request of a batch. Exactly one of Response / Err is set,
// except when a response was received but could not be written to the local cache: then both are
// set (CachingClient.CompleteBatch).
type BatchResult struct {
	Response *Response
	Err      error
}

// BatchClient is a Client that can also submit many requests as one asynchronous batch
// (Anthropic Message Batches: 50% of the list price, results within 24 hours).
type BatchClient interface {
	Client
	// CompleteBatch returns one BatchResult per request, in the order of reqs.
	// A failure of a single item is reported in its BatchResult.Err (an *APIError when it came
	// from the API), so a partially failed batch is still usable. A non-nil error reports a
	// failure of the whole call (submission failed, context canceled, ...); the returned slice
	// still holds every response obtained before the failure (the other entries carry an Err).
	CompleteBatch(ctx context.Context, reqs []Request) ([]BatchResult, error)
}

// BatchStore persists results of batches. CachingClient implements it on top of FileCache.
type BatchStore interface {
	// StoreBatchResult persists a response for (tag, key). It may decline to store a response
	// (e.g. a truncated one) by returning nil without storing it.
	StoreBatchResult(tag, key string, resp *Response) error
}

// StoringBatchClient is a BatchClient that hands every successful result of every batch it reads
// (including results of a resumed batch for requests outside the current call) to a BatchStore,
// and forgets a batch only after all of its results were stored. That makes "collected" mean
// "persisted", so no failure between fetching and storing can lead to paying for a batch twice.
type StoringBatchClient interface {
	BatchClient
	CompleteBatchStore(ctx context.Context, reqs []Request, store BatchStore) ([]BatchResult, error)
}

// RequestDefaulter is implemented by clients that fill provider defaults (model, max_tokens,
// thinking, effort) into a Request. The caching decorator applies it before computing the cache
// key, so that client-level defaults are part of the key. Decorators forward it to their inner client.
type RequestDefaulter interface {
	ApplyDefaults(req Request) Request
}

// applyDefaults applies c's defaults when c implements RequestDefaulter.
func applyDefaults(c Client, req Request) Request {
	if d, ok := c.(RequestDefaulter); ok {
		return d.ApplyDefaults(req)
	}
	return req
}

// Embedder abstracts embedding inference.
type Embedder interface {
	// Embed returns vectors in the same order and of the same length as texts.
	Embed(ctx context.Context, texts []string) ([][]float32, error)
	// Model is the model name (part of the embedding cache key).
	Model() string
	// Dim is the vector dimension (part of the embedding cache key).
	Dim() int
}

// APIError is an upstream API error. Used to decide whether to retry.
type APIError struct {
	StatusCode int
	// Type is the provider's error type when known (e.g. "rate_limit_error", "overloaded_error",
	// or BatchItemExpired / BatchItemCanceled for batch items that never ran).
	Type    string
	Message string
	Err     error
}

// Pseudo error types for batch items that were not processed.
const (
	BatchItemExpired  = "batch_item_expired"
	BatchItemCanceled = "batch_item_canceled"
)

func (e *APIError) Error() string {
	if e.Message == "" {
		return fmt.Sprintf("llm: api error (status %d)", e.StatusCode)
	}
	return fmt.Sprintf("llm: api error (status %d): %s", e.StatusCode, e.Message)
}

func (e *APIError) Unwrap() error { return e.Err }

// Retryable reports whether the failure is transient. Covers 429, 5xx (incl. Anthropic's 529
// overloaded) and 408/409, plus batch items that expired or were canceled before running.
func (e *APIError) Retryable() bool {
	switch e.Type {
	case BatchItemExpired, BatchItemCanceled:
		return true
	}
	switch e.StatusCode {
	case 408, 409, 429:
		return true
	}
	return e.StatusCode >= 500
}

// ErrTransient marks failures that are safe and sensible to retry although they are not an
// *APIError (e.g. network errors while polling a batch that is recorded and will be resumed).
var ErrTransient = errors.New("llm: transient failure")

// IsRetryable reports whether the error chain contains ErrTransient or a retryable APIError.
func IsRetryable(err error) bool {
	if errors.Is(err, ErrTransient) {
		return true
	}
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		return apiErr.Retryable()
	}
	return false
}
