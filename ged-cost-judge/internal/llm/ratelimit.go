package llm

import (
	"context"
	"fmt"
	"math/rand/v2"
	"time"

	"golang.org/x/time/rate"
)

// RetryConfig configures retries.
type RetryConfig struct {
	// MaxAttempts is the number of attempts including the first. 1 means no retries.
	MaxAttempts int
	BaseBackoff time.Duration
	MaxBackoff  time.Duration
}

func DefaultRetryConfig() RetryConfig {
	return RetryConfig{MaxAttempts: 4, BaseBackoff: time.Second, MaxBackoff: 30 * time.Second}
}

// ThrottledClient is a decorator adding rate control and retries.
//
// Even with concurrent calls, the API rate is managed centrally by this single limiter.
type ThrottledClient struct {
	inner   Client
	limiter *rate.Limiter
	retry   RetryConfig
}

var _ Client = (*ThrottledClient)(nil)

// NewThrottledClient limits to rps (requests per second) with burst.
// rps <= 0 means no limit.
func NewThrottledClient(inner Client, rps float64, burst int, retry RetryConfig) *ThrottledClient {
	var limiter *rate.Limiter
	if rps > 0 {
		if burst <= 0 {
			burst = 1
		}
		limiter = rate.NewLimiter(rate.Limit(rps), burst)
	}
	if retry.MaxAttempts <= 0 {
		retry = DefaultRetryConfig()
	}
	return &ThrottledClient{inner: inner, limiter: limiter, retry: retry}
}

func (c *ThrottledClient) Complete(ctx context.Context, req Request) (*Response, error) {
	var lastErr error
	for attempt := range c.retry.MaxAttempts {
		if c.limiter != nil {
			if err := c.limiter.Wait(ctx); err != nil {
				return nil, fmt.Errorf("llm: rate limiter wait: %w", err)
			}
		}
		resp, err := c.inner.Complete(ctx, req)
		if err == nil {
			return resp, nil
		}
		lastErr = err
		if !IsRetryable(err) || attempt == c.retry.MaxAttempts-1 {
			return nil, err
		}
		if err := sleepBackoff(ctx, c.retry, attempt); err != nil {
			return nil, err
		}
	}
	return nil, lastErr
}

// ApplyDefaults forwards to the inner client (see RequestDefaulter).
func (c *ThrottledClient) ApplyDefaults(req Request) Request { return applyDefaults(c.inner, req) }

var _ BatchClient = (*ThrottledClient)(nil)

// CompleteBatch forwards to the inner BatchClient, taking one limiter token per call and retrying
// the whole call on a retryable error (e.g. 429/529 on submission). Retrying is safe: the
// Anthropic client persists a batch only after it was accepted and resumes it instead of
// resubmitting. Per-item errors are not retried here. If the inner client is not a BatchClient,
// the requests are completed one by one through Complete (with its rate limit and retries).
func (c *ThrottledClient) CompleteBatch(ctx context.Context, reqs []Request) ([]BatchResult, error) {
	bc, ok := c.inner.(BatchClient)
	if !ok {
		out := make([]BatchResult, len(reqs))
		for i, req := range reqs {
			resp, err := c.Complete(ctx, req)
			if ctxErr := ctx.Err(); ctxErr != nil {
				return nil, ctxErr
			}
			out[i] = BatchResult{Response: resp, Err: err}
		}
		return out, nil
	}
	var lastErr error
	for attempt := range c.retry.MaxAttempts {
		if c.limiter != nil {
			if err := c.limiter.Wait(ctx); err != nil {
				return nil, fmt.Errorf("llm: rate limiter wait: %w", err)
			}
		}
		out, err := bc.CompleteBatch(ctx, reqs)
		if err == nil {
			return out, nil
		}
		lastErr = err
		if !IsRetryable(err) || attempt == c.retry.MaxAttempts-1 {
			return nil, err
		}
		if err := sleepBackoff(ctx, c.retry, attempt); err != nil {
			return nil, err
		}
	}
	return nil, lastErr
}

// sleepBackoff waits with exponential backoff + jitter.
func sleepBackoff(ctx context.Context, cfg RetryConfig, attempt int) error {
	backoff := cfg.BaseBackoff << attempt
	if backoff > cfg.MaxBackoff || backoff <= 0 {
		backoff = cfg.MaxBackoff
	}
	// Spread over 50–100% so goroutines that failed together do not all retry at once.
	jittered := time.Duration(float64(backoff) * (0.5 + 0.5*rand.Float64()))
	timer := time.NewTimer(jittered)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
