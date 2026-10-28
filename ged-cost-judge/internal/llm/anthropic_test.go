package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeAnthropic is an offline stand-in for the Messages and Message Batches endpoints.
type fakeAnthropic struct {
	t *testing.T

	mu sync.Mutex
	// Messages API
	messageBodies []map[string]any
	errStatus     int    // when non-zero, POST /v1/messages fails with this status
	errType       string // error.type of that failure
	// Message Batches API
	batches         map[string][]fakeBatchItem
	batchCreates    int
	polls           map[string]int
	pollsUntilEnded int
	failCustomIDs   map[string]bool // items that come back errored
	nextID          int
}

type fakeBatchItem struct {
	CustomID string         `json:"custom_id"`
	Params   map[string]any `json:"params"`
}

func newFakeAnthropic(t *testing.T) (*fakeAnthropic, *httptest.Server) {
	f := &fakeAnthropic{
		t:               t,
		batches:         map[string][]fakeBatchItem{},
		polls:           map[string]int{},
		pollsUntilEnded: 2,
		failCustomIDs:   map[string]bool{},
	}
	srv := httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(srv.Close)
	return f, srv
}

func (f *fakeAnthropic) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	path := r.URL.Path
	switch {
	case r.Method == http.MethodPost && path == "/v1/messages":
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			f.t.Errorf("decode message body: %v", err)
		}
		f.messageBodies = append(f.messageBodies, body)
		if f.errStatus != 0 {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(f.errStatus)
			fmt.Fprintf(w, `{"type":"error","error":{"type":%q,"message":"fake %d"},"request_id":"req_1"}`, f.errType, f.errStatus)
			return
		}
		writeJSON(w, fakeMessage("msg_1", body["model"].(string), `{"verdict":"A"}`, 1000, 200, 300))

	case r.Method == http.MethodPost && path == "/v1/messages/batches":
		var body struct {
			Requests []fakeBatchItem `json:"requests"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			f.t.Errorf("decode batch body: %v", err)
		}
		f.batchCreates++
		f.nextID++
		id := fmt.Sprintf("msgbatch_%d", f.nextID)
		f.batches[id] = body.Requests
		writeJSON(w, fakeBatch(id, "in_progress"))

	case r.Method == http.MethodGet && strings.HasPrefix(path, "/v1/messages/batches/") && strings.HasSuffix(path, "/results"):
		id := strings.TrimSuffix(strings.TrimPrefix(path, "/v1/messages/batches/"), "/results")
		items, ok := f.batches[id]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/x-jsonl")
		// Reverse order: results are not guaranteed to follow request order.
		for i := len(items) - 1; i >= 0; i-- {
			it := items[i]
			var line string
			if f.failCustomIDs[it.CustomID] {
				line = fmt.Sprintf(`{"custom_id":%q,"result":{"type":"errored","error":{"type":"error","error":{"type":"invalid_request_error","message":"bad item"},"request_id":"req_x"}}}`, it.CustomID)
			} else {
				msg := fakeMessage("msg_"+it.CustomID[:8], it.Params["model"].(string), echoText(it.Params), 100, 0, 10)
				line = fmt.Sprintf(`{"custom_id":%q,"result":{"type":"succeeded","message":%s}}`, it.CustomID, msg)
			}
			fmt.Fprintln(w, line)
		}

	case r.Method == http.MethodGet && strings.HasPrefix(path, "/v1/messages/batches/"):
		id := strings.TrimPrefix(path, "/v1/messages/batches/")
		if _, ok := f.batches[id]; !ok {
			http.NotFound(w, r)
			return
		}
		f.polls[id]++
		status := "in_progress"
		if f.polls[id] >= f.pollsUntilEnded {
			status = "ended"
		}
		writeJSON(w, fakeBatch(id, status))

	default:
		f.t.Errorf("unexpected request %s %s", r.Method, path)
		http.NotFound(w, r)
	}
}

// echoText builds the structured output of a batch item from its user text, so tests can check
// that results are mapped back to the right request.
func echoText(params map[string]any) string {
	msgs := params["messages"].([]any)
	content := msgs[0].(map[string]any)["content"].([]any)
	text := content[0].(map[string]any)["text"].(string)
	b, _ := json.Marshal(map[string]string{"echo": text})
	return string(b)
}

func fakeMessage(id, model, text string, in, cacheRead, out int) string {
	b, _ := json.Marshal(map[string]any{
		"id": id, "type": "message", "role": "assistant", "model": model,
		"content": []any{
			map[string]any{"type": "thinking", "thinking": "", "signature": "sig"},
			map[string]any{"type": "text", "text": text},
		},
		"stop_reason": "end_turn", "stop_sequence": nil,
		"usage": map[string]any{
			"input_tokens": in, "cache_read_input_tokens": cacheRead,
			"cache_creation_input_tokens": 0, "output_tokens": out,
		},
	})
	return string(b)
}

func fakeBatch(id, status string) string {
	b, _ := json.Marshal(map[string]any{
		"id": id, "type": "message_batch", "processing_status": status,
		"request_counts": map[string]int{"processing": 0, "succeeded": 0, "errored": 0, "canceled": 0, "expired": 0},
		"created_at":     "2026-10-28T00:00:00Z", "expires_at": "2026-10-29T00:00:00Z",
		"ended_at": nil, "archived_at": nil, "cancel_initiated_at": nil, "results_url": nil,
	})
	return string(b)
}

func writeJSON(w http.ResponseWriter, body string) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(body))
}

func newTestAnthropic(t *testing.T, srv *httptest.Server, mod func(*AnthropicConfig)) *AnthropicClient {
	t.Helper()
	cfg := AnthropicConfig{
		APIKey:       "test-key", // never a real key: every request goes to the httptest server
		BaseURL:      srv.URL,
		MaxRetries:   -1,
		PollInterval: time.Millisecond,
	}
	if mod != nil {
		mod(&cfg)
	}
	c, err := NewAnthropic(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func judgeReq(model, text string) Request {
	return Request{
		Model:    model,
		Messages: []Message{System("You are a judge."), User(text)},
		Tag:      "judge",
	}
}

type verdictOut struct {
	Verdict string `json:"verdict"`
}

func TestAnthropicCompleteStructuredOutput(t *testing.T) {
	f, srv := newFakeAnthropic(t)
	c := newTestAnthropic(t, srv, nil)
	req := judgeReq("claude-haiku-5-5", "Which is closer?")
	req.Effort = "low"
	req.Thinking = ThinkingAdaptive

	out, resp, err := CompleteJSON[verdictOut](context.Background(), c, req, "verdict", "")
	if err != nil {
		t.Fatal(err)
	}
	if out.Verdict != "A" {
		t.Errorf("verdict = %q, want A", out.Verdict)
	}
	if resp.Model != "claude-haiku-5-5" || resp.FinishReason != "end_turn" {
		t.Errorf("model/finish = %q/%q", resp.Model, resp.FinishReason)
	}
	wantUsage := Usage{InputTokens: 1200, CachedInputTokens: 200, OutputTokens: 300}
	got := resp.Usage
	got.CostUSD = 0
	if got != wantUsage {
		t.Errorf("usage = %+v, want %+v", got, wantUsage)
	}
	// 1000 fresh × $0.10 + 200 cached × $0.01 + 300 out × $0.50, per Mtok.
	if want := 252e-6; math.Abs(resp.Usage.CostUSD-want) > 1e-12 {
		t.Errorf("cost = %g, want %g", resp.Usage.CostUSD, want)
	}
	if !strings.Contains(string(resp.Raw), `"msg_1"`) || !strings.Contains(string(resp.Raw), `"thinking"`) {
		t.Errorf("raw does not hold the full message: %s", resp.Raw)
	}

	if len(f.messageBodies) != 1 {
		t.Fatalf("messages calls = %d, want 1", len(f.messageBodies))
	}
	body := f.messageBodies[0]
	if body["model"] != "claude-haiku-5-5" || body["max_tokens"] != float64(DefaultAnthropicMaxTokens) {
		t.Errorf("model/max_tokens = %v/%v", body["model"], body["max_tokens"])
	}
	for _, k := range []string{"temperature", "top_p", "top_k"} {
		if _, ok := body[k]; ok {
			t.Errorf("request must not send %s", k)
		}
	}
	system := body["system"].([]any)
	if system[0].(map[string]any)["text"] != "You are a judge." {
		t.Errorf("system = %v", system)
	}
	msgs := body["messages"].([]any)
	if len(msgs) != 1 || msgs[0].(map[string]any)["role"] != "user" {
		t.Errorf("messages = %v", msgs)
	}
	if body["thinking"].(map[string]any)["type"] != "adaptive" {
		t.Errorf("thinking = %v", body["thinking"])
	}
	oc := body["output_config"].(map[string]any)
	if oc["effort"] != "low" {
		t.Errorf("effort = %v", oc["effort"])
	}
	format := oc["format"].(map[string]any)
	schema := format["schema"].(map[string]any)
	if format["type"] != "json_schema" || schema["additionalProperties"] != false {
		t.Errorf("format = %v", format)
	}
}

func TestAnthropicThinkingMapping(t *testing.T) {
	cases := []struct {
		model, thinking, effort string
		want                    string // JSON of the thinking param; "" = omitted; "error" = rejected
	}{
		{"claude-haiku-5-5", "", "", ""},
		{"claude-haiku-5-5", ThinkingAdaptive, "", `{"type":"adaptive"}`},
		{"claude-haiku-5-5", ThinkingOff, "low", `{"type":"disabled"}`},
		{"claude-sonnet-5-5", ThinkingOff, "high", `{"type":"between_tools"}`},
		{"claude-sonnet-5-5", ThinkingOff, "max", "error"},
		{"claude-opus-5-5", ThinkingOff, "low", "error"},
	}
	for _, tc := range cases {
		p, err := thinkingParam(tc.model, tc.thinking, tc.effort)
		if tc.want == "error" {
			if err == nil {
				t.Errorf("%s/%s/%s: want error", tc.model, tc.thinking, tc.effort)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s/%s/%s: %v", tc.model, tc.thinking, tc.effort, err)
			continue
		}
		got := ""
		if p.OfAdaptive != nil || p.OfDisabled != nil || p.OfBetweenTools != nil || p.OfEnabled != nil {
			b, err := json.Marshal(p)
			if err != nil {
				t.Fatal(err)
			}
			got = string(b)
		}
		if got != tc.want {
			t.Errorf("%s/%s/%s: thinking = %s, want %s", tc.model, tc.thinking, tc.effort, got, tc.want)
		}
	}
}

func TestAnthropicRejectsSampling(t *testing.T) {
	f, srv := newFakeAnthropic(t)
	c := newTestAnthropic(t, srv, nil)
	temp := 0.0
	req := judgeReq("claude-sonnet-5-5", "x")
	req.Temperature = &temp
	if _, err := c.Complete(context.Background(), req); err == nil {
		t.Fatal("want error for temperature on an Anthropic request")
	}
	if len(f.messageBodies) != 0 {
		t.Error("request must not reach the API")
	}
}

func TestAnthropicErrorMapping(t *testing.T) {
	cases := []struct {
		status    int
		typ       string
		retryable bool
	}{
		{429, "rate_limit_error", true},
		{529, "overloaded_error", true},
		{500, "api_error", true},
		{400, "invalid_request_error", false},
		{401, "authentication_error", false},
	}
	for _, tc := range cases {
		f, srv := newFakeAnthropic(t)
		f.errStatus, f.errType = tc.status, tc.typ
		c := newTestAnthropic(t, srv, nil)
		_, err := c.Complete(context.Background(), judgeReq("claude-haiku-5-5", "x"))
		var apiErr *APIError
		if !errors.As(err, &apiErr) {
			t.Fatalf("%d: error %v is not *APIError", tc.status, err)
		}
		if apiErr.StatusCode != tc.status || apiErr.Type != tc.typ || apiErr.Message != fmt.Sprintf("fake %d", tc.status) {
			t.Errorf("%d: got %+v", tc.status, apiErr)
		}
		if IsRetryable(err) != tc.retryable {
			t.Errorf("%d: retryable = %v, want %v", tc.status, IsRetryable(err), tc.retryable)
		}
		if len(f.messageBodies) != 1 {
			t.Errorf("%d: calls = %d, want 1 (SDK retries disabled)", tc.status, len(f.messageBodies))
		}
	}
}

func TestAnthropicBatchMapsResultsAndFailures(t *testing.T) {
	f, srv := newFakeAnthropic(t)
	stateDir := t.TempDir()
	c := newTestAnthropic(t, srv, func(cfg *AnthropicConfig) { cfg.BatchStateDir = stateDir })

	a := judgeReq("claude-haiku-5-5", "alpha")
	b := judgeReq("claude-haiku-5-5", "beta")
	cc := judgeReq("claude-sonnet-5-5", "gamma")
	aDup := a
	aDup.Tag = "other" // same cache key as a: sent once
	f.failCustomIDs[CacheKey(c.ApplyDefaults(b))] = true

	res, err := c.CompleteBatch(context.Background(), []Request{a, b, cc, aDup})
	if err != nil {
		t.Fatal(err)
	}
	if f.batchCreates != 1 || len(f.batches["msgbatch_1"]) != 3 {
		t.Fatalf("batches = %d with %d items, want 1 with 3", f.batchCreates, len(f.batches["msgbatch_1"]))
	}
	if f.polls["msgbatch_1"] < 2 {
		t.Errorf("polls = %d, want polling until ended", f.polls["msgbatch_1"])
	}
	for i, want := range map[int]string{0: "alpha", 2: "gamma", 3: "alpha"} {
		r := res[i]
		if r.Err != nil {
			t.Fatalf("item %d: %v", i, r.Err)
		}
		if r.Response.Text != fmt.Sprintf(`{"echo":%q}`, want) {
			t.Errorf("item %d text = %s", i, r.Response.Text)
		}
	}
	if res[0].Response == res[3].Response {
		t.Error("duplicate requests must not share one *Response")
	}
	// Batch pricing: (100 × $0.10 + 10 × $0.50) / Mtok × 0.5 for Haiku.
	if want := 7.5e-6; math.Abs(res[0].Response.Usage.CostUSD-want) > 1e-12 {
		t.Errorf("batch cost = %g, want %g", res[0].Response.Usage.CostUSD, want)
	}
	var apiErr *APIError
	if !errors.As(res[1].Err, &apiErr) || apiErr.StatusCode != 400 || apiErr.Type != "invalid_request_error" {
		t.Errorf("failed item err = %v", res[1].Err)
	}
	if IsRetryable(res[1].Err) || res[1].Response != nil {
		t.Errorf("failed item: retryable=%v response=%v", IsRetryable(res[1].Err), res[1].Response)
	}

	st := readState(t, filepath.Join(stateDir, "msgbatch_1.json"))
	if st.Status != batchCollected || st.Succeeded != 2 || st.Failed != 1 || len(st.CustomIDs) != 3 {
		t.Errorf("state = %+v", st)
	}
}

func TestAnthropicBatchChunking(t *testing.T) {
	f, srv := newFakeAnthropic(t)
	c := newTestAnthropic(t, srv, func(cfg *AnthropicConfig) { cfg.MaxBatchRequests = 2 })
	reqs := []Request{judgeReq("claude-haiku-5-5", "1"), judgeReq("claude-haiku-5-5", "2"), judgeReq("claude-haiku-5-5", "3")}
	res, err := c.CompleteBatch(context.Background(), reqs)
	if err != nil {
		t.Fatal(err)
	}
	if f.batchCreates != 2 || len(f.batches["msgbatch_1"]) != 2 || len(f.batches["msgbatch_2"]) != 1 {
		t.Errorf("chunks: creates=%d sizes=%d,%d", f.batchCreates, len(f.batches["msgbatch_1"]), len(f.batches["msgbatch_2"]))
	}
	for i, r := range res {
		if r.Err != nil || r.Response.Text != fmt.Sprintf(`{"echo":"%d"}`, i+1) {
			t.Errorf("item %d: %+v", i, r)
		}
	}
}

func TestAnthropicBatchResumesFromStateFile(t *testing.T) {
	f, srv := newFakeAnthropic(t)
	stateDir := t.TempDir()
	c := newTestAnthropic(t, srv, func(cfg *AnthropicConfig) { cfg.BatchStateDir = stateDir })
	reqs := []Request{judgeReq("claude-haiku-5-5", "one"), judgeReq("claude-haiku-5-5", "two")}

	// An earlier run submitted these requests and died while polling.
	var ids []string
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
		f.batches["msgbatch_old"] = append(f.batches["msgbatch_old"], fakeBatchItem{CustomID: k, Params: params})
	}
	old := batchState{ID: "msgbatch_old", CreatedAt: time.Date(2026, 10, 28, 0, 0, 0, 0, time.UTC), Status: batchSubmitted, CustomIDs: ids}
	b, _ := json.Marshal(old)
	if err := os.WriteFile(filepath.Join(stateDir, "msgbatch_old.json"), b, 0o644); err != nil {
		t.Fatal(err)
	}

	res, err := c.CompleteBatch(context.Background(), reqs)
	if err != nil {
		t.Fatal(err)
	}
	if f.batchCreates != 0 {
		t.Errorf("resumed run submitted %d new batches, want 0", f.batchCreates)
	}
	if res[0].Err != nil || res[0].Response.Text != `{"echo":"one"}` || res[1].Err != nil || res[1].Response.Text != `{"echo":"two"}` {
		t.Errorf("results = %+v / %+v", res[0], res[1])
	}
	if st := readState(t, filepath.Join(stateDir, "msgbatch_old.json")); st.Status != batchCollected {
		t.Errorf("state status = %q, want collected", st.Status)
	}

	// A collected batch is not resumed again: an uncached rerun submits a new batch.
	if _, err := c.CompleteBatch(context.Background(), reqs); err != nil {
		t.Fatal(err)
	}
	if f.batchCreates != 1 {
		t.Errorf("batch creates after collection = %d, want 1", f.batchCreates)
	}

	// ResumeBatch works on an id directly.
	got, err := c.ResumeBatch(context.Background(), "msgbatch_old")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[ids[0]].Response == nil {
		t.Errorf("ResumeBatch = %+v", got)
	}
}

func TestCachingBatchClientSendsOnlyMisses(t *testing.T) {
	f, srv := newFakeAnthropic(t)
	inner := newTestAnthropic(t, srv, func(cfg *AnthropicConfig) { cfg.Effort = "low" })
	cache := NewFileCache(t.TempDir())
	c := NewCachingClient(NewThrottledClient(inner, 0, 0, RetryConfig{MaxAttempts: 1}), cache)
	ctx := context.Background()

	a := judgeReq("claude-haiku-5-5", "a")
	b := judgeReq("claude-haiku-5-5", "b")
	bad := judgeReq("claude-haiku-5-5", "bad")
	f.failCustomIDs[CacheKey(inner.ApplyDefaults(bad))] = true

	if _, err := c.CompleteBatch(ctx, []Request{a}); err != nil {
		t.Fatal(err)
	}
	res, err := c.CompleteBatch(ctx, []Request{a, b, bad})
	if err != nil {
		t.Fatal(err)
	}
	if n := len(f.batches["msgbatch_2"]); f.batchCreates != 2 || n != 2 {
		t.Fatalf("second call: creates=%d items=%d, want 2 and 2 (only misses)", f.batchCreates, n)
	}
	if !res[0].Response.Cached || res[0].Response.Usage.CostUSD != 0 {
		t.Errorf("a should be a free cache hit: %+v", res[0].Response)
	}
	if res[1].Err != nil || res[1].Response.Cached {
		t.Errorf("b: %+v", res[1])
	}
	if res[2].Err == nil {
		t.Error("bad: want per-item error")
	}

	// Rerun: only the failed item is sent again.
	if _, err := c.CompleteBatch(ctx, []Request{a, b, bad}); err != nil {
		t.Fatal(err)
	}
	if n := len(f.batches["msgbatch_3"]); f.batchCreates != 3 || n != 1 {
		t.Errorf("third call: creates=%d items=%d, want 3 and 1", f.batchCreates, n)
	}

	// Complete hits the entry stored by the batch, under the key that includes the default effort.
	resp, err := c.Complete(ctx, b)
	if err != nil {
		t.Fatal(err)
	}
	if !resp.Cached || len(f.messageBodies) != 0 {
		t.Errorf("Complete after batch: cached=%v calls=%d", resp.Cached, len(f.messageBodies))
	}
	withDefaults := b
	withDefaults.Effort, withDefaults.MaxTokens = "low", DefaultAnthropicMaxTokens
	if _, err := cache.Get(b.Tag, CacheKey(withDefaults)); err != nil {
		t.Errorf("cache entry not under the defaulted key: %v", err)
	}
}

// TestCacheKeyStableForOldRequests pins the keys of requests that do not use Thinking / Effort.
// The expected values were computed on the code before those fields were added; a change here
// would invalidate every existing cache entry (and the numbers reported from it).
func TestCacheKeyStableForOldRequests(t *testing.T) {
	temp := 0.2
	seed := int64(7)
	full := Request{
		Model:       "gpt-5-mini",
		Messages:    []Message{System("You are a judge."), User("Rate this.")},
		Temperature: &temp,
		Seed:        &seed,
		MaxTokens:   100,
		Schema:      &Schema{Name: "verdict", JSON: []byte(`{"type":"object","properties":{"ok":{"type":"boolean"}},"required":["ok"],"additionalProperties":false}`), Strict: true},
		Tag:         "t",
	}
	bare := Request{Model: "gpt-5-mini", Messages: []Message{User("hi")}}
	if got, want := CacheKey(full), "4e6a2b14205b17837509afa3e20f18df51aa43d2bcf916dba3d68eb947728272"; got != want {
		t.Errorf("full key = %s, want %s", got, want)
	}
	if got, want := CacheKey(bare), "b1bd3ecd2490bd2457ed2e8808ccb3f012a550346e00d3c601fda22234faa29f"; got != want {
		t.Errorf("bare key = %s, want %s", got, want)
	}
	withEffort := bare
	withEffort.Effort = "low"
	if CacheKey(withEffort) == CacheKey(bare) {
		t.Error("Effort must be part of the key")
	}
	withThinking := bare
	withThinking.Thinking = ThinkingOff
	if CacheKey(withThinking) == CacheKey(bare) {
		t.Error("Thinking must be part of the key")
	}
}

func TestClaudePricing(t *testing.T) {
	p := DefaultPricing()
	u := Usage{InputTokens: 1_000_000, OutputTokens: 1_000_000}
	for model, want := range map[string]float64{"claude-sonnet-5-5": 12, "claude-haiku-5-5": 0.6, "claude-opus-5-5": 24} {
		if got := p.ChatCost(model, u); math.Abs(got-want) > 1e-9 {
			t.Errorf("%s list cost = %g, want %g", model, got, want)
		}
		if got := p.ChatCostBatch(model, u); math.Abs(got-want/2) > 1e-9 {
			t.Errorf("%s batch cost = %g, want %g", model, got, want/2)
		}
	}
	// Cache writes are billed at 1.25× input on top of the fresh tokens.
	w := Usage{InputTokens: 1_000_000, CacheWriteInputTokens: 1_000_000}
	if got := p.ChatCost("claude-sonnet-5-5", w); math.Abs(got-2.5) > 1e-9 {
		t.Errorf("cache write cost = %g, want 2.5", got)
	}
}

func TestNewClientForModel(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "test-key")
	t.Setenv("OPENAI_API_KEY", "test-key")
	c, err := NewClientForModel("claude-haiku-5-5", ClientConfig{})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := c.(*AnthropicClient); !ok {
		t.Errorf("claude model → %T", c)
	}
	if _, ok := c.(BatchClient); !ok {
		t.Error("Anthropic client must implement BatchClient")
	}
	c, err = NewClientForModel("gpt-5.6-luna", ClientConfig{})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := c.(*OpenAIClient); !ok {
		t.Errorf("gpt model → %T", c)
	}
	if _, err := NewClientForModel("llama-3", ClientConfig{}); err == nil {
		t.Error("unknown prefix: want error")
	}
	t.Setenv("ANTHROPIC_API_KEY", "")
	if c, err := NewClientForModel("claude-sonnet-5-5", ClientConfig{}); err == nil || c != nil {
		t.Errorf("missing key: got %v, %v", c, err)
	}
}

func readState(t *testing.T, path string) batchState {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var st batchState
	if err := json.Unmarshal(b, &st); err != nil {
		t.Fatal(err)
	}
	return st
}
