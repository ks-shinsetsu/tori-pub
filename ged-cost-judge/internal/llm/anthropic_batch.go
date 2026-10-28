package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/anthropics/anthropic-sdk-go/v2"
	"github.com/anthropics/anthropic-sdk-go/v2/option"
)

// Money-safety rules of the batch path (each one closes a way to pay for the same requests twice):
//
//  1. A batch is recorded (state file, status "submitted") as soon as the API accepts it, and stays
//     "submitted" until every successful result has been handed to the BatchStore. Without a store
//     it is never forgotten automatically (MarkCollected does it). Resuming an ended batch only
//     downloads results again, which is free for 29 days.
//  2. A recorded batch that covers a request is always resumed before anything is submitted; if it
//     cannot be read, nothing is submitted in that call.
//  3. Results of a resumed batch for requests outside the current call are stored too (the state
//     file keeps the cache tags per custom_id), so nothing that was paid for is dropped.
//  4. Submissions are never retried by the SDK (no idempotency key). After a failure whose outcome
//     is unknown, the batches created since the upload started are listed and recorded as
//     "ambiguous", so the next attempt resumes them instead of submitting again.

const (
	// anthropicBatchRequestLimit is the Message Batches limit on requests per batch
	// (the other limit is 256 MB per batch; see defaultBatchBytes).
	anthropicBatchRequestLimit = 100_000
	defaultBatchBytes          = 32 << 20
	// batchResultsRetention is how long the API keeps batch results.
	batchResultsRetention = 29 * 24 * time.Hour
	// reconcileWindow is how far before the start of a failed upload a listed batch may have been
	// created (clock skew) to count as possibly ours.
	reconcileWindow = 10 * time.Minute
)

// Values of batchState.Status.
const (
	batchSubmitted = "submitted" // accepted by the API; results not persisted yet
	batchCollected = "collected" // every successful result was stored (or MarkCollected was called)
	batchGone      = "gone"      // results are no longer available (404 or past retention)
)

// batchItemRef is one request of a recorded batch: its custom_id (the CacheKey) and the cache tags
// it was requested under, so a result can be stored even when a later call does not ask for it.
type batchItemRef struct {
	CustomID string   `json:"custom_id"`
	Tags     []string `json:"tags,omitempty"`
}

// batchState is the on-disk record of a submitted batch (<BatchStateDir>/<id>.json).
type batchState struct {
	ID        string    `json:"id"`
	CreatedAt time.Time `json:"created_at"`
	Status    string    `json:"status"`
	// Ambiguous marks a batch found after a submission whose outcome was unknown. Its items are
	// unknown until its results are read, so every call resumes it until then.
	Ambiguous   bool           `json:"ambiguous,omitempty"`
	Items       []batchItemRef `json:"items"`
	CollectedAt *time.Time     `json:"collected_at,omitempty"`
	Succeeded   int            `json:"succeeded,omitempty"`
	Failed      int            `json:"failed,omitempty"`
}

// batchItem is one distinct request of a CompleteBatch call.
type batchItem struct {
	params anthropic.MessageBatchNewParamsRequestParams
	tags   []string
}

// CompleteBatch is CompleteBatchStore without a store: results are returned but no batch is ever
// marked collected (call MarkCollected after persisting them, or use CachingClient).
func (c *AnthropicClient) CompleteBatch(ctx context.Context, reqs []Request) ([]BatchResult, error) {
	return c.CompleteBatchStore(ctx, reqs, nil)
}

// CompleteBatchStore sends reqs as Message Batches (50% of the list price) and waits for the results.
//
// custom_id is CacheKey(request after ApplyDefaults); identical requests are sent once and the
// result is copied to each. Recorded batches that cover any of the requests are resumed first
// (successful results are reused; items that failed there are submitted again). The remaining
// requests are split by MaxBatchRequests / MaxBatchBytes; all chunks are submitted, then all are
// polled, even if a later submission or poll fails. Every successful result of every batch read is
// passed to store (when non-nil) before the batch is marked collected.
//
// Per-item failures (errored, expired, canceled, invalid request) are returned in BatchResult.Err;
// refusals and truncations are Responses (see Response.FinishError); LatencyMS is 0. On a
// whole-call error the returned slice still holds every result obtained, and the error is
// retryable (IsRetryable) when retrying cannot pay twice.
func (c *AnthropicClient) CompleteBatchStore(ctx context.Context, reqs []Request, store BatchStore) ([]BatchResult, error) {
	out := make([]BatchResult, len(reqs))
	keys := make([]string, len(reqs))
	items := map[string]*batchItem{}
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
		it, ok := items[k]
		if !ok {
			it = &batchItem{params: p.batchItem()}
			items[k] = it
			order = append(order, k)
		}
		if !slices.Contains(it.tags, req.Tag) {
			it.tags = append(it.tags, req.Tag)
		}
	}
	if len(order) == 0 {
		return out, nil
	}

	results := map[string]BatchResult{}
	var (
		polled []string
		errs   []error
	)
	take := func(got map[string]BatchResult, keepFailures bool) {
		for k, r := range got {
			if _, want := items[k]; want && (keepFailures || r.Err == nil) {
				results[k] = r
			}
		}
	}

	// 1. Resume recorded batches that cover any of these requests.
	pending, err := c.pendingStates()
	if err != nil {
		return c.finishBatch(out, keys, results, polled, err)
	}
	for _, st := range pending {
		if !st.Ambiguous && !coversAny(st.Items, items) {
			continue
		}
		polled = append(polled, st.ID)
		got, err := c.collect(ctx, st, items, store)
		take(got, false) // failures of an earlier run are submitted again
		if err != nil {
			errs = append(errs, err)
			if ctx.Err() != nil {
				break
			}
		}
	}
	if len(errs) > 0 {
		// A recorded batch that could not be read may hold results for these requests:
		// submitting them now could pay twice. Let the caller retry.
		return c.finishBatch(out, keys, results, polled, errors.Join(errs...))
	}

	// 2. Submit what is left.
	var todo []anthropic.MessageBatchNewParamsRequest
	for _, k := range order {
		if _, done := results[k]; !done {
			todo = append(todo, anthropic.MessageBatchNewParamsRequest{CustomID: k, Params: items[k].params})
		}
	}
	chunks, err := c.chunkBatch(todo)
	if err != nil {
		return c.finishBatch(out, keys, results, polled, err)
	}
	var submitted []*batchState
	for _, chunk := range chunks {
		st, err := c.submitBatch(ctx, chunk, items)
		if err != nil {
			errs = append(errs, err)
			break
		}
		submitted = append(submitted, st)
	}

	// 3. Wait for every batch that was accepted, even after a failed submission.
	for _, st := range submitted {
		polled = append(polled, st.ID)
		got, err := c.collect(ctx, st, items, store)
		take(got, true)
		if err != nil {
			errs = append(errs, err)
			if ctx.Err() != nil {
				break
			}
		}
	}
	return c.finishBatch(out, keys, results, polled, errors.Join(errs...))
}

// finishBatch maps results back to the request order. Requests without a result get an error that
// names the batches involved (for a manual ResumeBatch).
func (c *AnthropicClient) finishBatch(out []BatchResult, keys []string, results map[string]BatchResult, polled []string, err error) ([]BatchResult, error) {
	for i := range out {
		if out[i].Err != nil {
			continue // invalid request, never sent
		}
		r, ok := results[keys[i]]
		switch {
		case !ok && err != nil:
			out[i].Err = fmt.Errorf("llm: anthropic batch: no result for request %d (batches %v): %w", i, polled, err)
		case !ok:
			out[i].Err = fmt.Errorf("llm: anthropic batch: no result for request %d (custom_id %s, batches %v)", i, keys[i], polled)
		case r.Response != nil:
			cp := *r.Response // duplicates must not share one Response
			out[i] = BatchResult{Response: &cp}
		default:
			out[i] = r
		}
	}
	return out, err
}

// collect waits for a recorded batch, reads all of its results and, with a store, stores every
// successful one under all of its tags (recorded ones and those of the current call). The batch is
// marked collected only when every item was delivered and every success was stored. The returned
// map holds whatever was read, also when an error is returned.
func (c *AnthropicClient) collect(ctx context.Context, st *batchState, items map[string]*batchItem, store BatchStore) (map[string]BatchResult, error) {
	got, err := c.fetchResults(ctx, st.ID)
	if err != nil {
		if isNotFound(err) || time.Since(st.CreatedAt) > batchResultsRetention {
			// The results are gone: forget the batch so its requests can be submitted again.
			gone := *st
			gone.Status = batchGone
			return map[string]BatchResult{}, c.saveState(&gone)
		}
		return nil, err
	}

	updated := *st
	tagsOf := map[string][]string{}
	for _, ref := range st.Items {
		tagsOf[ref.CustomID] = ref.Tags
	}
	if st.Ambiguous {
		// Now the items are known: record them (with tags where this call knows them).
		updated.Ambiguous = false
		updated.Items = nil
		for k := range got {
			var tags []string
			if it, ok := items[k]; ok {
				tags = slices.Clone(it.tags)
			}
			updated.Items = append(updated.Items, batchItemRef{CustomID: k, Tags: tags})
		}
		slices.SortFunc(updated.Items, func(a, b batchItemRef) int { return strings.Compare(a.CustomID, b.CustomID) })
	}
	if store == nil {
		if st.Ambiguous {
			return got, c.saveState(&updated)
		}
		return got, nil
	}

	complete := true
	for _, ref := range updated.Items {
		if _, ok := got[ref.CustomID]; !ok {
			complete = false // should not happen; keep the batch to read it again
		}
	}
	// The state records the tags learned from this call, so later calls can store those results.
	refIdx := make(map[string]int, len(updated.Items))
	updated.Items = slices.Clone(updated.Items)
	for i, ref := range updated.Items {
		refIdx[ref.CustomID] = i
	}
	var storeErrs []error
	succeeded, failed, untagged := 0, 0, 0
	for k, r := range got {
		if r.Err != nil || r.Response == nil {
			failed++
			continue
		}
		succeeded++
		tags := slices.Clone(tagsOf[k])
		if it, ok := items[k]; ok {
			for _, t := range it.tags {
				if !slices.Contains(tags, t) {
					tags = append(tags, t)
				}
			}
		}
		if i, ok := refIdx[k]; ok {
			updated.Items[i].Tags = slices.Clone(tags)
		}
		if len(tags) == 0 {
			// A result of an ambiguous batch that no call has asked for yet: it cannot be stored
			// (no cache tag). The batch stays "submitted", so the call that wants it (matched by
			// custom_id) stores it later; re-reading an ended batch is free.
			untagged++
			continue
		}
		for _, tag := range tags {
			if err := store.StoreBatchResult(tag, k, r.Response); err != nil {
				storeErrs = append(storeErrs, err)
			}
		}
	}
	if len(storeErrs) > 0 {
		// Keep the batch "submitted": a rerun reads it again instead of paying again.
		if err := c.saveState(&updated); err != nil {
			storeErrs = append(storeErrs, err)
		}
		return got, fmt.Errorf("llm: anthropic batch %s: storing results: %w", st.ID, errors.Join(storeErrs...))
	}
	if !complete || untagged > 0 {
		return got, c.saveState(&updated)
	}
	now := time.Now().UTC()
	updated.Status = batchCollected
	updated.CollectedAt = &now
	updated.Succeeded = succeeded
	updated.Failed = failed
	return got, c.saveState(&updated)
}

// ResumeBatch polls batch id until it has ended and returns its results keyed by custom_id. It
// works for any batch of this API key and does not change its record (see MarkCollected).
func (c *AnthropicClient) ResumeBatch(ctx context.Context, id string) (map[string]BatchResult, error) {
	return c.fetchResults(ctx, id)
}

// MarkCollected marks recorded batches as collected, i.e. their results are persisted and they
// need not be resumed again. Only needed when CompleteBatch is used without a store.
func (c *AnthropicClient) MarkCollected(ids ...string) error {
	for _, id := range ids {
		st, err := c.stateByID(id)
		if err != nil {
			return err
		}
		if st == nil {
			continue
		}
		updated := *st
		now := time.Now().UTC()
		updated.Status = batchCollected
		updated.CollectedAt = &now
		if err := c.saveState(&updated); err != nil {
			return err
		}
	}
	return nil
}

// fetchResults waits for the batch to end and reads its results file.
func (c *AnthropicClient) fetchResults(ctx context.Context, id string) (map[string]BatchResult, error) {
	if err := c.waitBatch(ctx, id); err != nil {
		return nil, err
	}
	stream := c.api.Messages.Batches.ResultsStreaming(ctx, id, anthropic.MessageBatchResultsParams{})
	defer func() { _ = stream.Close() }()
	results := map[string]BatchResult{}
	for stream.Next() {
		item := stream.Current()
		results[item.CustomID] = c.batchItemResult(item.Result)
	}
	if err := stream.Err(); err != nil {
		if ctx.Err() != nil {
			return nil, batchInterrupted(id, ctx.Err())
		}
		return nil, fmt.Errorf("llm: anthropic batch %s results: %w", id, transient(wrapAnthropicError(err)))
	}
	return results, nil
}

// waitBatch polls until processing_status is "ended", backing off ×1.5 up to MaxPollInterval.
// Transient failures (network errors, 408/409/429, 5xx) are retried until they have lasted
// PollFailureTimeout; a definitive API error (other 4xx) or ctx cancellation ends the wait.
// The batch stays recorded in every case.
func (c *AnthropicClient) waitBatch(ctx context.Context, id string) error {
	interval := c.cfg.PollInterval
	var failingSince time.Time
	for {
		b, err := c.api.Messages.Batches.Get(ctx, id, anthropic.MessageBatchGetParams{})
		switch {
		case err != nil:
			if ctx.Err() != nil {
				return batchInterrupted(id, ctx.Err())
			}
			werr := wrapAnthropicError(err)
			if isDefinitive(werr) {
				return fmt.Errorf("llm: anthropic batch %s poll: %w", id, werr)
			}
			if failingSince.IsZero() {
				failingSince = time.Now()
			}
			if d := time.Since(failingSince); d >= c.cfg.PollFailureTimeout {
				return fmt.Errorf("llm: anthropic batch %s: polling failed for %s (the batch is recorded and resumes on the next call): %w",
					id, d.Round(time.Second), transient(werr))
			}
		case b.ProcessingStatus == anthropic.MessageBatchProcessingStatusEnded:
			return nil
		default:
			failingSince = time.Time{}
		}
		if err := sleepCtx(ctx, interval); err != nil {
			return batchInterrupted(id, err)
		}
		interval = min(interval*3/2, c.cfg.MaxPollInterval)
	}
}

func batchInterrupted(id string, err error) error {
	return fmt.Errorf("llm: anthropic batch %s: wait interrupted; the batch is recorded and resumes on the next call (or ResumeBatch(%q)): %w", id, id, err)
}

// isDefinitive reports an API error that retrying cannot fix (4xx other than 408/409/429).
func isDefinitive(err error) bool {
	var apiErr *APIError
	return errors.As(err, &apiErr) && apiErr.StatusCode >= 400 && apiErr.StatusCode < 500 && !apiErr.Retryable()
}

func isNotFound(err error) bool {
	var apiErr *APIError
	return errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusNotFound
}

// transient marks err as retryable unless it is a definitive API error.
func transient(err error) error {
	if isDefinitive(err) || IsRetryable(err) {
		return err
	}
	return fmt.Errorf("%w: %w", ErrTransient, err)
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
// A single request larger than MaxBatchBytes gets a chunk of its own.
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

// submitBatch creates one batch and records it before returning.
//
// The upload is never retried by the SDK: the Batches API has no idempotency key, so a retry after
// a lost response would create a second billed batch. A clean rejection (4xx) means nothing was
// created. Any other failure (network error, timeout, 5xx) leaves the outcome unknown: the batches
// created since the upload started are listed and recorded as ambiguous, and a retryable error is
// returned, so the next attempt resumes them instead of uploading again.
func (c *AnthropicClient) submitBatch(ctx context.Context, reqs []anthropic.MessageBatchNewParamsRequest, items map[string]*batchItem) (*batchState, error) {
	start := time.Now()
	b, err := c.api.Messages.Batches.New(ctx, anthropic.MessageBatchNewParams{Requests: reqs},
		option.WithMaxRetries(0), option.WithRequestTimeout(c.cfg.BatchSubmitTimeout))
	if err != nil {
		werr := wrapAnthropicError(err)
		var apiErr *APIError
		if errors.As(werr, &apiErr) && apiErr.StatusCode >= 400 && apiErr.StatusCode < 500 {
			return nil, fmt.Errorf("llm: anthropic batch submit rejected: %w", werr)
		}
		n, rerr := c.reconcileSubmit(ctx, start)
		if rerr != nil {
			// Not retryable: retrying blindly could create a second batch.
			return nil, fmt.Errorf("llm: anthropic batch submit failed with an unknown outcome (%v) and the batch list could not be read (%v); check the Console for an untracked batch before rerunning", werr, rerr)
		}
		if ctx.Err() != nil {
			return nil, fmt.Errorf("llm: anthropic batch submit interrupted; %d batch(es) created since the upload started are recorded and resume on the next call: %w", n, ctx.Err())
		}
		return nil, fmt.Errorf("llm: anthropic batch submit failed with an unknown outcome; %d batch(es) created since the upload started are recorded and resume on the next call: %w", n, transient(werr))
	}
	st := &batchState{ID: b.ID, CreatedAt: b.CreatedAt, Status: batchSubmitted, Items: make([]batchItemRef, len(reqs))}
	if st.CreatedAt.IsZero() {
		st.CreatedAt = time.Now().UTC()
	}
	for i, r := range reqs {
		st.Items[i] = batchItemRef{CustomID: r.CustomID, Tags: slices.Clone(items[r.CustomID].tags)}
	}
	if err := c.saveState(st); err != nil {
		// The batch runs (and is billed) regardless; it is in memory for this process.
		return nil, fmt.Errorf("llm: anthropic batch %s submitted but its state file could not be written (resume with ResumeBatch): %w", b.ID, err)
	}
	return st, nil
}

// reconcileSubmit records the batches created since start (minus clock-skew slack) that are not
// recorded yet as ambiguous, and returns how many it recorded. It runs even if ctx is canceled.
func (c *AnthropicClient) reconcileSubmit(ctx context.Context, start time.Time) (int, error) {
	rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Minute)
	defer cancel()
	page, err := c.api.Messages.Batches.List(rctx, anthropic.MessageBatchListParams{Limit: anthropic.Int(100)})
	if err != nil {
		return 0, wrapAnthropicError(err)
	}
	known, err := c.knownIDs()
	if err != nil {
		return 0, err
	}
	n := 0
	for _, b := range page.Data {
		if known[b.ID] || b.CreatedAt.Before(start.Add(-reconcileWindow)) {
			continue
		}
		if err := c.saveState(&batchState{ID: b.ID, CreatedAt: b.CreatedAt, Status: batchSubmitted, Ambiguous: true}); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
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

// stateByID returns the record of a batch (memory first, then disk); nil when unknown.
func (c *AnthropicClient) stateByID(id string) (*batchState, error) {
	c.mu.Lock()
	st, ok := c.states[id]
	c.mu.Unlock()
	if ok {
		return st, nil
	}
	return c.loadState(id)
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

// allStates returns every recorded batch (state dir and this process; memory wins).
func (c *AnthropicClient) allStates() (map[string]*batchState, error) {
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
		byID[id] = st
	}
	c.mu.Unlock()
	return byID, nil
}

func (c *AnthropicClient) knownIDs() (map[string]bool, error) {
	all, err := c.allStates()
	if err != nil {
		return nil, err
	}
	known := make(map[string]bool, len(all))
	for id := range all {
		known[id] = true
	}
	return known, nil
}

// pendingStates returns the recorded batches whose results were not persisted yet, oldest first.
func (c *AnthropicClient) pendingStates() ([]*batchState, error) {
	all, err := c.allStates()
	if err != nil {
		return nil, err
	}
	var out []*batchState
	for _, st := range all {
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

func coversAny(refs []batchItemRef, want map[string]*batchItem) bool {
	for _, r := range refs {
		if _, ok := want[r.CustomID]; ok {
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
