package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/anthropics/anthropic-sdk-go/v2"
)

const (
	// anthropicBatchRequestLimit is the Message Batches limit on requests per batch
	// (the other limit is 256 MB per batch; see defaultBatchBytes).
	anthropicBatchRequestLimit = 100_000
	defaultBatchBytes          = 200 << 20
	// maxPollFailures is how many consecutive transient errors polling tolerates.
	maxPollFailures = 10
)

// Values of batchState.Status.
const (
	batchSubmitted = "submitted" // accepted by the API; results not collected yet
	batchCollected = "collected" // results fetched and returned to the caller
)

// batchState is the on-disk record of a submitted batch (<BatchStateDir>/<id>.json).
// custom_id is the CacheKey of the request, so a rerun can tell which requests a pending batch covers.
type batchState struct {
	ID          string     `json:"id"`
	CreatedAt   time.Time  `json:"created_at"`
	Status      string     `json:"status"`
	CustomIDs   []string   `json:"custom_ids"`
	CollectedAt *time.Time `json:"collected_at,omitempty"`
	Succeeded   int        `json:"succeeded,omitempty"`
	Failed      int        `json:"failed,omitempty"`
}

// CompleteBatch sends reqs as Message Batches (50% of the list price) and waits for the results.
//
// custom_id is CacheKey(request after ApplyDefaults); identical requests are sent once and the
// result is copied to each. Before submitting, batches recorded as still pending (in BatchStateDir
// or by this process) that cover any of the requests are resumed by re-polling, so an interrupted
// run does not pay twice. The remaining requests are split by MaxBatchRequests / MaxBatchBytes,
// all chunks are submitted first, then polled. Per-item failures (errored, expired, canceled,
// refusal, invalid request) are returned in BatchResult.Err; LatencyMS is 0 for batch items.
//
// Note: once results are collected the batch is marked collected, so if the process dies before
// the caller stores them (CachingClient does so right away), a rerun submits those requests again.
func (c *AnthropicClient) CompleteBatch(ctx context.Context, reqs []Request) ([]BatchResult, error) {
	out := make([]BatchResult, len(reqs))
	keys := make([]string, len(reqs))
	items := map[string]anthropic.MessageBatchNewParamsRequestParams{}
	var order []string
	for i, req := range reqs {
		req = c.ApplyDefaults(req)
		p, err := c.buildParams(req)
		if err != nil {
			out[i].Err = err
			continue
		}
		k := CacheKey(req)
		keys[i] = k
		if _, dup := items[k]; !dup {
			items[k] = p.batchItem()
			order = append(order, k)
		}
	}
	if len(order) == 0 {
		return out, nil
	}

	results := map[string]BatchResult{}
	merge := func(got map[string]BatchResult) {
		for k, r := range got {
			if _, want := items[k]; want {
				results[k] = r
			}
		}
	}

	// 1. Resume batches that an interrupted run left behind.
	pending, err := c.pendingStates()
	if err != nil {
		return nil, err
	}
	for _, st := range pending {
		if !coversAny(st.CustomIDs, items) {
			continue
		}
		got, err := c.ResumeBatch(ctx, st.ID)
		if err != nil {
			return nil, err
		}
		merge(got)
	}

	// 2. Submit what is left, then wait for every new batch.
	var todo []anthropic.MessageBatchNewParamsRequest
	for _, k := range order {
		if _, done := results[k]; !done {
			todo = append(todo, anthropic.MessageBatchNewParamsRequest{CustomID: k, Params: items[k]})
		}
	}
	chunks, err := c.chunkBatch(todo)
	if err != nil {
		return nil, err
	}
	var ids []string
	for _, chunk := range chunks {
		// On failure, earlier chunks are already recorded and are resumed by the next call.
		id, err := c.submitBatch(ctx, chunk)
		if err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	for _, id := range ids {
		got, err := c.ResumeBatch(ctx, id)
		if err != nil {
			return nil, err
		}
		merge(got)
	}

	for i := range reqs {
		if out[i].Err != nil {
			continue
		}
		r, ok := results[keys[i]]
		switch {
		case !ok:
			out[i].Err = fmt.Errorf("llm: anthropic batch returned no result for request %d (custom_id %s)", i, keys[i])
		case r.Response != nil:
			cp := *r.Response // duplicates must not share one Response
			out[i] = BatchResult{Response: &cp}
		default:
			out[i] = r
		}
	}
	return out, nil
}

// ResumeBatch polls batch id until it has ended and returns its results keyed by custom_id.
// It works for any batch of this API key (the state file is optional); when the batch is recorded,
// the record is marked collected.
func (c *AnthropicClient) ResumeBatch(ctx context.Context, id string) (map[string]BatchResult, error) {
	if err := c.waitBatch(ctx, id); err != nil {
		return nil, err
	}
	stream := c.api.Messages.Batches.ResultsStreaming(ctx, id, anthropic.MessageBatchResultsParams{})
	defer func() { _ = stream.Close() }()
	results := map[string]BatchResult{}
	succeeded := 0
	for stream.Next() {
		item := stream.Current()
		r := c.batchItemResult(item.Result)
		if r.Err == nil {
			succeeded++
		}
		results[item.CustomID] = r
	}
	if err := stream.Err(); err != nil {
		return nil, fmt.Errorf("llm: anthropic batch %s results: %w", id, wrapAnthropicError(err))
	}
	if err := c.markCollected(id, succeeded, len(results)-succeeded); err != nil {
		return nil, err
	}
	return results, nil
}

// waitBatch polls until processing_status is "ended", backing off ×1.5 up to MaxPollInterval.
// Transient errors (429/5xx) are tolerated up to maxPollFailures in a row.
func (c *AnthropicClient) waitBatch(ctx context.Context, id string) error {
	interval := c.cfg.PollInterval
	failures := 0
	for {
		b, err := c.api.Messages.Batches.Get(ctx, id, anthropic.MessageBatchGetParams{})
		switch {
		case err != nil:
			werr := wrapAnthropicError(err)
			if ctx.Err() != nil || !IsRetryable(werr) || failures >= maxPollFailures {
				return fmt.Errorf("llm: anthropic batch %s poll: %w", id, werr)
			}
			failures++
		case b.ProcessingStatus == anthropic.MessageBatchProcessingStatusEnded:
			return nil
		default:
			failures = 0
		}
		if err := sleepCtx(ctx, interval); err != nil {
			return err
		}
		interval = min(interval*3/2, c.cfg.MaxPollInterval)
	}
}

// batchItemResult converts one line of the results file.
func (c *AnthropicClient) batchItemResult(u anthropic.MessageBatchResultUnion) BatchResult {
	switch u.Type {
	case "succeeded":
		msg := u.AsSucceeded().Message
		resp, err := c.toResponse(&msg, true)
		if err != nil {
			return BatchResult{Err: err}
		}
		return BatchResult{Response: resp}
	case "errored":
		e := u.AsErrored().Error.Error
		return BatchResult{Err: &APIError{StatusCode: anthropicErrorStatus(e.Type), Type: e.Type, Message: e.Message}}
	case "expired":
		return BatchResult{Err: &APIError{Type: BatchItemExpired, Message: "batch expired before the request was processed"}}
	case "canceled":
		return BatchResult{Err: &APIError{Type: BatchItemCanceled, Message: "batch was canceled before the request was processed"}}
	default:
		return BatchResult{Err: fmt.Errorf("llm: unknown batch result type %q", u.Type)}
	}
}

// chunkBatch splits requests by MaxBatchRequests and MaxBatchBytes (JSON size).
// A single request larger than MaxBatchBytes gets a chunk of its own (and is rejected by the API).
func (c *AnthropicClient) chunkBatch(reqs []anthropic.MessageBatchNewParamsRequest) ([][]anthropic.MessageBatchNewParamsRequest, error) {
	var (
		chunks [][]anthropic.MessageBatchNewParamsRequest
		cur    []anthropic.MessageBatchNewParamsRequest
		size   int
	)
	for _, r := range reqs {
		b, err := json.Marshal(r)
		if err != nil {
			return nil, fmt.Errorf("llm: marshal batch request %s: %w", r.CustomID, err)
		}
		n := len(b) + 1 // separator
		if len(cur) > 0 && (len(cur) >= c.cfg.MaxBatchRequests || size+n > c.cfg.MaxBatchBytes) {
			chunks = append(chunks, cur)
			cur, size = nil, 0
		}
		cur = append(cur, r)
		size += n
	}
	if len(cur) > 0 {
		chunks = append(chunks, cur)
	}
	return chunks, nil
}

// submitBatch creates one batch and records it before returning its id.
func (c *AnthropicClient) submitBatch(ctx context.Context, reqs []anthropic.MessageBatchNewParamsRequest) (string, error) {
	b, err := c.api.Messages.Batches.New(ctx, anthropic.MessageBatchNewParams{Requests: reqs})
	if err != nil {
		return "", wrapAnthropicError(err)
	}
	st := &batchState{ID: b.ID, CreatedAt: b.CreatedAt, Status: batchSubmitted, CustomIDs: make([]string, len(reqs))}
	if st.CreatedAt.IsZero() {
		st.CreatedAt = time.Now().UTC()
	}
	for i, r := range reqs {
		st.CustomIDs[i] = r.CustomID
	}
	if err := c.saveState(st); err != nil {
		// The batch runs (and is billed) regardless; surface the id so it can be resumed by hand.
		return "", fmt.Errorf("llm: anthropic batch %s submitted but not recorded (resume with ResumeBatch): %w", b.ID, err)
	}
	return b.ID, nil
}

func (c *AnthropicClient) statePath(id string) string {
	return filepath.Join(c.cfg.BatchStateDir, safeTag.ReplaceAllString(id, "_")+".json")
}

func (c *AnthropicClient) saveState(st *batchState) error {
	c.mu.Lock()
	c.states[st.ID] = st
	c.mu.Unlock()
	if c.cfg.BatchStateDir == "" {
		return nil
	}
	b, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return fmt.Errorf("llm: marshal batch state: %w", err)
	}
	return writeFileAtomic(c.statePath(st.ID), b)
}

// markCollected marks a recorded batch as collected. Unknown ids (batches not submitted through
// this client or its state dir) are ignored.
func (c *AnthropicClient) markCollected(id string, succeeded, failed int) error {
	c.mu.Lock()
	st, ok := c.states[id]
	c.mu.Unlock()
	if !ok {
		loaded, err := c.loadState(id)
		if err != nil {
			return err
		}
		if loaded == nil {
			return nil
		}
		st = loaded
	}
	now := time.Now().UTC()
	updated := *st
	updated.Status = batchCollected
	updated.CollectedAt = &now
	updated.Succeeded = succeeded
	updated.Failed = failed
	return c.saveState(&updated)
}

// loadState reads one state file; (nil, nil) when there is none.
func (c *AnthropicClient) loadState(id string) (*batchState, error) {
	if c.cfg.BatchStateDir == "" {
		return nil, nil
	}
	b, err := os.ReadFile(c.statePath(id))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("llm: read batch state: %w", err)
	}
	var st batchState
	if err := json.Unmarshal(b, &st); err != nil {
		return nil, fmt.Errorf("llm: parse batch state %s: %w", c.statePath(id), err)
	}
	return &st, nil
}

// pendingStates returns the recorded batches whose results were not collected yet
// (from this process and from BatchStateDir), oldest first.
func (c *AnthropicClient) pendingStates() ([]*batchState, error) {
	byID := map[string]*batchState{}
	if c.cfg.BatchStateDir != "" {
		entries, err := os.ReadDir(c.cfg.BatchStateDir)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("llm: read batch state dir: %w", err)
		}
		for _, e := range entries {
			name := e.Name()
			if e.IsDir() || !strings.HasSuffix(name, ".json") || strings.HasPrefix(name, ".") {
				continue
			}
			st, err := c.loadState(strings.TrimSuffix(name, ".json"))
			if err != nil {
				return nil, err
			}
			if st != nil {
				byID[st.ID] = st
			}
		}
	}
	c.mu.Lock()
	for id, st := range c.states {
		byID[id] = st // the in-memory record is the newest
	}
	c.mu.Unlock()
	var out []*batchState
	for _, st := range byID {
		if st.Status == batchSubmitted {
			out = append(out, st)
		}
	}
	// Oldest first, ties by id, so resumption order is deterministic.
	slices.SortFunc(out, func(a, b *batchState) int {
		if c := a.CreatedAt.Compare(b.CreatedAt); c != 0 {
			return c
		}
		return strings.Compare(a.ID, b.ID)
	})
	return out, nil
}

func coversAny[V any](ids []string, want map[string]V) bool {
	for _, id := range ids {
		if _, ok := want[id]; ok {
			return true
		}
	}
	return false
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
