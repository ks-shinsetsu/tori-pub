package llm

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// plantBatch simulates a batch that an earlier run submitted (and then died): the fake API knows
// it and its state file says "submitted". It returns the custom_ids.
func plantBatch(t *testing.T, f *fakeAnthropic, c *AnthropicClient, stateDir, id string, reqs []Request) []string {
	t.Helper()
	st := batchState{ID: id, CreatedAt: time.Now().UTC(), Status: batchSubmitted}
	var ids []string
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, r := range reqs {
		r = c.ApplyDefaults(r)
		p, err := c.buildParams(r)
		if err != nil {
			t.Fatal(err)
		}
		raw, err := json.Marshal(p.batchItem())
		if err != nil {
			t.Fatal(err)
		}
		var params map[string]any
		if err := json.Unmarshal(raw, &params); err != nil {
			t.Fatal(err)
		}
		k := CacheKey(r)
		ids = append(ids, k)
		f.batches[id] = append(f.batches[id], fakeBatchItem{CustomID: k, Params: params})
		st.Items = append(st.Items, batchItemRef{CustomID: k, Tags: []string{r.Tag}})
	}
	f.createdAt[id] = st.CreatedAt
	b, _ := json.Marshal(st)
	if err := os.WriteFile(filepath.Join(stateDir, id+".json"), b, 0o644); err != nil {
		t.Fatal(err)
	}
	return ids
}

func fastRetry(n int) RetryConfig {
	return RetryConfig{MaxAttempts: n, BaseBackoff: time.Millisecond, MaxBackoff: time.Millisecond}
}

// B1: a retryable failure on the second batch of a call must not resubmit the first one.
// Before the fix: batch 1 was marked collected before its results were cached, the retry found it
// neither pending nor cached and submitted it again (3 batches for 2 requests).
func TestBatchRetryAfterLaterBatchFailureDoesNotResubmit(t *testing.T) {
	f, srv := newFakeAnthropic(t)
	stateDir, cacheDir := t.TempDir(), t.TempDir()
	inner := newTestAnthropic(t, srv, func(cfg *AnthropicConfig) {
		cfg.BatchStateDir = stateDir
		cfg.MaxBatchRequests = 1
	})
	c := NewCachingClient(NewThrottledClient(inner, 0, 0, fastRetry(3)), NewFileCache(cacheDir))
	c.BatchRetry = fastRetry(3)
	f.resultsFail["msgbatch_2"] = 1 // one 500 on the results download of the second batch

	reqs := []Request{judgeReq("claude-haiku-5-5", "first"), judgeReq("claude-haiku-5-5", "second")}
	res, err := c.CompleteBatch(context.Background(), reqs)
	if err != nil {
		t.Fatal(err)
	}
	if f.batchCreates != 2 {
		t.Fatalf("batches created = %d, want 2 (no resubmission)", f.batchCreates)
	}
	for i, want := range []string{"first", "second"} {
		if res[i].Err != nil || res[i].Response.Text != `{"echo":"`+want+`"}` || res[i].Response.Cached {
			t.Errorf("item %d: %+v", i, res[i])
		}
	}
	for _, id := range []string{"msgbatch_1", "msgbatch_2"} {
		if st := readState(t, filepath.Join(stateDir, id+".json")); st.Status != batchCollected {
			t.Errorf("%s: status %q, want collected", id, st.Status)
		}
	}
}

// B1, plain retry decorator without a cache (the reviewer's setup): the retry resumes both batches.
func TestBatchThrottledRetryWithoutCacheDoesNotResubmit(t *testing.T) {
	f, srv := newFakeAnthropic(t)
	inner := newTestAnthropic(t, srv, func(cfg *AnthropicConfig) {
		cfg.BatchStateDir = t.TempDir()
		cfg.MaxBatchRequests = 1
	})
	c := NewThrottledClient(inner, 0, 0, fastRetry(3))
	f.resultsFail["msgbatch_2"] = 1
	res, err := c.CompleteBatch(context.Background(), []Request{judgeReq("claude-haiku-5-5", "x"), judgeReq("claude-haiku-5-5", "y")})
	if err != nil {
		t.Fatal(err)
	}
	if f.batchCreates != 2 || res[0].Err != nil || res[1].Err != nil {
		t.Errorf("creates=%d results=%+v", f.batchCreates, res)
	}
}

// B1, process death between batches: the stored batch is served from the cache, the other one is
// resumed; nothing is submitted again.
func TestBatchRerunAfterFailedCallResumes(t *testing.T) {
	f, srv := newFakeAnthropic(t)
	stateDir, cacheDir := t.TempDir(), t.TempDir()
	mod := func(cfg *AnthropicConfig) {
		cfg.BatchStateDir = stateDir
		cfg.MaxBatchRequests = 1
	}
	reqs := []Request{judgeReq("claude-haiku-5-5", "p"), judgeReq("claude-haiku-5-5", "q")}

	run1 := NewCachingClient(newTestAnthropic(t, srv, mod), NewFileCache(cacheDir))
	run1.BatchRetry = fastRetry(1)
	f.resultsFail["msgbatch_2"] = 1
	res, err := run1.CompleteBatch(context.Background(), reqs)
	if err == nil || !IsRetryable(err) {
		t.Fatalf("run 1: want a retryable error, got %v", err)
	}
	if res[0].Err != nil || res[0].Response == nil {
		t.Errorf("run 1 must still return the first result: %+v", res[0])
	}
	if !strings.Contains(err.Error(), "msgbatch_2") {
		t.Errorf("error should name the batch: %v", err)
	}

	run2 := NewCachingClient(newTestAnthropic(t, srv, mod), NewFileCache(cacheDir)) // fresh process
	res, err = run2.CompleteBatch(context.Background(), reqs)
	if err != nil {
		t.Fatal(err)
	}
	if f.batchCreates != 2 {
		t.Errorf("batches created = %d, want 2", f.batchCreates)
	}
	if !res[0].Response.Cached || res[1].Response.Cached || res[1].Response.Text != `{"echo":"q"}` {
		t.Errorf("run 2: %+v / %+v", res[0].Response, res[1].Response)
	}
}

// B2: resuming a batch for a subset of its requests stores the results of the others too, so a
// later call for them is served from the cache. Before the fix a second batch was created.
func TestResumedBatchStoresResultsOutsideCurrentCall(t *testing.T) {
	f, srv := newFakeAnthropic(t)
	stateDir, cacheDir := t.TempDir(), t.TempDir()
	inner := newTestAnthropic(t, srv, func(cfg *AnthropicConfig) { cfg.BatchStateDir = stateDir })
	one, two := judgeReq("claude-haiku-5-5", "one"), judgeReq("claude-haiku-5-5", "two")
	two.Tag = "other-tag"
	plantBatch(t, f, inner, stateDir, "msgbatch_old", []Request{one, two})

	c := NewCachingClient(inner, NewFileCache(cacheDir))
	if res, err := c.CompleteBatch(context.Background(), []Request{one}); err != nil || res[0].Err != nil {
		t.Fatalf("call for {one}: %v %+v", err, res)
	}
	res, err := c.CompleteBatch(context.Background(), []Request{two})
	if err != nil {
		t.Fatal(err)
	}
	if f.batchCreates != 0 {
		t.Errorf("batches created = %d, want 0", f.batchCreates)
	}
	if !res[0].Response.Cached || res[0].Response.Text != `{"echo":"two"}` {
		t.Errorf("{two} should be a cache hit under its recorded tag: %+v", res[0].Response)
	}
	if st := readState(t, filepath.Join(stateDir, "msgbatch_old.json")); st.Status != batchCollected {
		t.Errorf("status %q, want collected", st.Status)
	}
}

// M1: a submission whose response is lost is not retried by the SDK; the batch it created is
// found by listing, recorded, and resumed by the retry instead of uploading again.
func TestBatchLostSubmitResponseIsReconciled(t *testing.T) {
	f, srv := newFakeAnthropic(t)
	stateDir := t.TempDir()
	inner := newTestAnthropic(t, srv, func(cfg *AnthropicConfig) {
		cfg.BatchStateDir = stateDir
		cfg.MaxRetries = 2 // SDK retries enabled in general; the upload must still not be retried
	})
	c := NewCachingClient(inner, NewFileCache(t.TempDir()))
	c.BatchRetry = fastRetry(3)
	f.dropSubmits = 1

	res, err := c.CompleteBatch(context.Background(), []Request{judgeReq("claude-haiku-5-5", "lost")})
	if err != nil {
		t.Fatal(err)
	}
	if f.batchCreates != 1 {
		t.Errorf("batches created = %d, want 1", f.batchCreates)
	}
	if res[0].Err != nil || res[0].Response.Text != `{"echo":"lost"}` {
		t.Errorf("result: %+v", res[0])
	}
	st := readState(t, filepath.Join(stateDir, "msgbatch_1.json"))
	if st.Status != batchCollected || st.Ambiguous || len(st.Items) != 1 {
		t.Errorf("reconciled state = %+v", st)
	}
}

// R1: a lost-response batch is first read by a call for unrelated requests (after the process
// that submitted it died). Its results have no recorded tag yet, so it must stay pending until the
// original requests come back; then they are stored and nothing is submitted twice.
func TestAmbiguousBatchReadByUnrelatedCallStaysPending(t *testing.T) {
	f, srv := newFakeAnthropic(t)
	stateDir, cacheDir := t.TempDir(), t.TempDir()
	mod := func(cfg *AnthropicConfig) { cfg.BatchStateDir = stateDir }
	orig, other := judgeReq("claude-haiku-5-5", "orig"), judgeReq("claude-haiku-5-5", "other")

	// Run 1: the upload is accepted but the response is lost; the process dies right after.
	run1 := NewCachingClient(newTestAnthropic(t, srv, mod), NewFileCache(cacheDir))
	run1.BatchRetry = fastRetry(1)
	f.dropSubmits = 1
	if _, err := run1.CompleteBatch(context.Background(), []Request{orig}); err == nil {
		t.Fatal("run 1: want an error for the lost submission")
	}
	if st := readState(t, filepath.Join(stateDir, "msgbatch_1.json")); !st.Ambiguous || st.Status != batchSubmitted {
		t.Fatalf("run 1 state = %+v, want ambiguous submitted", st)
	}

	// Run 2: an unrelated call reads the ambiguous batch first.
	inner2 := newTestAnthropic(t, srv, mod)
	run2 := NewCachingClient(inner2, NewFileCache(cacheDir))
	if res, err := run2.CompleteBatch(context.Background(), []Request{other}); err != nil || res[0].Err != nil {
		t.Fatalf("run 2: %v %+v", err, res)
	}
	keyOrig := CacheKey(inner2.ApplyDefaults(orig))
	st := readState(t, filepath.Join(stateDir, "msgbatch_1.json"))
	if st.Ambiguous || st.Status != batchSubmitted || len(st.Items) != 1 || st.Items[0].CustomID != keyOrig || len(st.Items[0].Tags) != 0 {
		t.Fatalf("after the unrelated call: %+v, want submitted with the item recorded untagged", st)
	}

	// Run 3: the original call resumes the batch, stores its result, and submits nothing.
	run3 := NewCachingClient(newTestAnthropic(t, srv, mod), NewFileCache(cacheDir))
	res, err := run3.CompleteBatch(context.Background(), []Request{orig})
	if err != nil || res[0].Err != nil || res[0].Response.Text != `{"echo":"orig"}` {
		t.Fatalf("run 3: %v %+v", err, res)
	}
	batchesWithOrig := 0
	for _, items := range f.batches {
		for _, it := range items {
			if it.CustomID == keyOrig {
				batchesWithOrig++
			}
		}
	}
	if batchesWithOrig != 1 || f.batchCreates != 2 {
		t.Errorf("batches with orig = %d (want 1), creates = %d (want 2: orig + other)", batchesWithOrig, f.batchCreates)
	}
	if st := readState(t, filepath.Join(stateDir, "msgbatch_1.json")); st.Status != batchCollected {
		t.Errorf("final status %q, want collected", st.Status)
	}
	if _, err := NewFileCache(cacheDir).Get(orig.Tag, keyOrig); err != nil {
		t.Errorf("orig not cached: %v", err)
	}
}

// M2: connection failures while polling do not end the wait.
func TestBatchPollingSurvivesTransportErrors(t *testing.T) {
	f, srv := newFakeAnthropic(t)
	c := newTestAnthropic(t, srv, nil)
	f.pollDrops["msgbatch_1"] = 3
	res, err := c.CompleteBatch(context.Background(), []Request{judgeReq("claude-haiku-5-5", "z")})
	if err != nil {
		t.Fatal(err)
	}
	if res[0].Err != nil {
		t.Error(res[0].Err)
	}
}

// M2/M3: when polling keeps failing, the error is retryable and names the batch; the batch stays
// recorded. Cancellation also names the batch.
func TestBatchWaitErrorsNameTheBatch(t *testing.T) {
	f, srv := newFakeAnthropic(t)
	stateDir := t.TempDir()
	c := newTestAnthropic(t, srv, func(cfg *AnthropicConfig) {
		cfg.BatchStateDir = stateDir
		cfg.PollFailureTimeout = 5 * time.Millisecond
	})
	f.pollDrops["msgbatch_1"] = 1_000_000
	_, err := c.CompleteBatch(context.Background(), []Request{judgeReq("claude-haiku-5-5", "w")})
	if err == nil || !IsRetryable(err) || !strings.Contains(err.Error(), "msgbatch_1") {
		t.Errorf("poll failure: %v (retryable=%v)", err, IsRetryable(err))
	}

	f.mu.Lock()
	f.pollDrops["msgbatch_1"] = 0
	f.pollsUntilEnded = 1_000_000
	f.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	_, err = c.CompleteBatch(ctx, []Request{judgeReq("claude-haiku-5-5", "w")})
	if !errors.Is(err, context.DeadlineExceeded) || !strings.Contains(err.Error(), "msgbatch_1") {
		t.Errorf("cancel: %v", err)
	}
	if f.batchCreates != 1 {
		t.Errorf("batches created = %d, want 1 (the second call resumes)", f.batchCreates)
	}
	if st := readState(t, filepath.Join(stateDir, "msgbatch_1.json")); st.Status != batchSubmitted {
		t.Errorf("status %q, want submitted", st.Status)
	}
}

// Expired / canceled items map to retryable per-item errors.
func TestBatchExpiredAndCanceledItemsAreRetryable(t *testing.T) {
	f, srv := newFakeAnthropic(t)
	c := newTestAnthropic(t, srv, nil)
	e, x := judgeReq("claude-haiku-5-5", "e"), judgeReq("claude-haiku-5-5", "x")
	f.itemTypes[CacheKey(c.ApplyDefaults(e))] = "expired"
	f.itemTypes[CacheKey(c.ApplyDefaults(x))] = "canceled"
	res, err := c.CompleteBatch(context.Background(), []Request{e, x})
	if err != nil {
		t.Fatal(err)
	}
	for i, typ := range []string{BatchItemExpired, BatchItemCanceled} {
		var apiErr *APIError
		if !errors.As(res[i].Err, &apiErr) || apiErr.Type != typ || !IsRetryable(res[i].Err) {
			t.Errorf("item %d: %v", i, res[i].Err)
		}
	}
}

// M4: a refusal is a cached Response that CompleteJSON reports as ErrRefusal; a truncated
// structured output is reported as ErrTruncated and not cached.
func TestRefusalIsCachedAndTruncationIsNot(t *testing.T) {
	f, srv := newFakeAnthropic(t)
	c := NewCachingClient(newTestAnthropic(t, srv, nil), NewFileCache(t.TempDir()))
	ctx := context.Background()

	f.messageStop = "refusal"
	req := judgeReq("claude-sonnet-5-5", "refuse me")
	for range 2 {
		_, resp, err := CompleteJSON[verdictOut](ctx, c, req, "verdict", "")
		if !errors.Is(err, ErrRefusal) || resp == nil || resp.FinishReason != FinishRefusal || resp.Text != "" {
			t.Errorf("refusal: err=%v resp=%+v", err, resp)
		}
	}
	if len(f.messageBodies) != 1 {
		t.Errorf("refusal calls = %d, want 1 (second from cache)", len(f.messageBodies))
	}

	f.messageStop = "max_tokens"
	req = judgeReq("claude-sonnet-5-5", "truncate me")
	for range 2 {
		if _, _, err := CompleteJSON[verdictOut](ctx, c, req, "verdict", ""); !errors.Is(err, ErrTruncated) {
			t.Errorf("truncation: %v", err)
		}
	}
	if len(f.messageBodies) != 3 {
		t.Errorf("calls = %d, want 3 (truncated output not cached)", len(f.messageBodies))
	}
}

// M5: ClientConfig pins Effort (and Thinking) into the request, hence into the cache key.
func TestClientConfigPinsEffort(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "test-key")
	t.Setenv("OPENAI_API_KEY", "test-key")
	for _, model := range []string{"claude-haiku-5-5", "gpt-5.6-luna"} {
		cfg := ClientConfig{Effort: "low"}
		if strings.HasPrefix(model, "claude-") {
			cfg.Thinking = ThinkingAdaptive
		}
		c, err := NewClientForModel(model, cfg)
		if err != nil {
			t.Fatal(err)
		}
		cc := NewCachingClient(c, NewFileCache(t.TempDir()))
		got := cc.ApplyDefaults(Request{Model: model, Messages: []Message{User("x")}})
		if got.Effort != "low" {
			t.Errorf("%s: effort = %q", model, got.Effort)
		}
		if strings.HasPrefix(model, "claude-") && got.Thinking != ThinkingAdaptive {
			t.Errorf("%s: thinking = %q", model, got.Thinking)
		}
	}
	if _, err := NewClientForModel("gpt-5.6-luna", ClientConfig{Thinking: ThinkingOff}); err == nil {
		t.Error("thinking on a gpt model: want error")
	}
	// Without a configured effort, OpenAI requests (and their cache keys) are unchanged.
	c, err := NewClientForModel("gpt-5.6-luna", ClientConfig{})
	if err != nil {
		t.Fatal(err)
	}
	req := Request{Model: "gpt-5-mini", Messages: []Message{User("hi")}}
	if CacheKey(applyDefaults(c, req)) != "b1bd3ecd2490bd2457ed2e8808ccb3f012a550346e00d3c601fda22234faa29f" {
		t.Error("OpenAI cache key changed without a configured effort")
	}
}
