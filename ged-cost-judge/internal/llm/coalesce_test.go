package llm

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

// recordingBatch is a BatchClient that answers every request with its last user message and records
// the size of each batch. failOnce lists request texts whose first submission fails retryably.
type recordingBatch struct {
	mu       sync.Mutex
	sizes    []int
	failOnce map[string]bool
}

func (f *recordingBatch) Complete(context.Context, Request) (*Response, error) {
	return nil, errors.New("Complete must not be called: the Coalescer sends batches")
}

func (f *recordingBatch) CompleteBatch(_ context.Context, reqs []Request) ([]BatchResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sizes = append(f.sizes, len(reqs))
	out := make([]BatchResult, len(reqs))
	for i, r := range reqs {
		text := r.Messages[len(r.Messages)-1].Content
		if f.failOnce[text] {
			delete(f.failOnce, text)
			out[i].Err = &APIError{Type: BatchItemExpired, Message: "expired"}
			continue
		}
		out[i].Response = &Response{Text: text, Model: r.Model, Usage: Usage{CostUSD: 0.5}}
	}
	return out, nil
}

func (f *recordingBatch) batches() []int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]int(nil), f.sizes...)
}

func testCoalescer(inner BatchClient) *Coalescer {
	c := NewCoalescer(inner)
	c.Quiet, c.MaxWait = 20*time.Millisecond, time.Second
	return c
}

func req(text string) Request {
	return Request{Model: "claude-sonnet-5-5", Messages: []Message{User(text)}, Tag: "realize"}
}

// completeAll calls Complete concurrently and returns the texts in call order.
func completeAll(t *testing.T, c *Coalescer, texts []string) []string {
	t.Helper()
	out := make([]string, len(texts))
	errs := make([]error, len(texts))
	var wg sync.WaitGroup
	for i, s := range texts {
		wg.Add(1)
		go func() {
			defer wg.Done()
			resp, err := c.Complete(context.Background(), req(s))
			if err == nil {
				out[i] = resp.Text
			}
			errs[i] = err
		}()
	}
	wg.Wait()
	if err := errors.Join(errs...); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestCoalescerGathersConcurrentCalls(t *testing.T) {
	f := &recordingBatch{}
	c := testCoalescer(f)
	var texts []string
	for i := range 50 {
		texts = append(texts, fmt.Sprintf("r%d", i))
	}
	got := completeAll(t, c, texts)
	for i := range texts {
		if got[i] != texts[i] {
			t.Fatalf("call %d got %q, want its own result %q", i, got[i], texts[i])
		}
	}
	if b := f.batches(); len(b) != 1 || b[0] != 50 {
		t.Fatalf("batches %v, want one batch of 50", b)
	}
}

func TestCoalescerMaxRequestsSplits(t *testing.T) {
	f := &recordingBatch{}
	c := testCoalescer(f)
	c.MaxRequests = 20
	var texts []string
	for i := range 50 {
		texts = append(texts, fmt.Sprintf("r%d", i))
	}
	completeAll(t, c, texts)
	total := 0
	for _, n := range f.batches() {
		if n > 20 {
			t.Fatalf("batch of %d exceeds MaxRequests 20", n)
		}
		total += n
	}
	if total != 50 {
		t.Fatalf("%d requests sent, want 50", total)
	}
}

func TestCoalescerResubmitsRetryableItem(t *testing.T) {
	f := &recordingBatch{failOnce: map[string]bool{"r1": true}}
	c := testCoalescer(f)
	got := completeAll(t, c, []string{"r0", "r1", "r2"})
	if got[1] != "r1" {
		t.Fatalf("expired item was not resubmitted: %q", got[1])
	}
	if b := f.batches(); len(b) != 2 || b[1] != 1 {
		t.Fatalf("batches %v, want the 3 requests and then the expired one alone", b)
	}
}

func TestCoalescerBeforeFlush(t *testing.T) {
	f := &recordingBatch{}
	c := testCoalescer(f)
	budget := errors.New("budget")
	c.BeforeFlush = func([]Request) (func(), error) { return nil, budget }
	if _, err := c.Complete(context.Background(), req("x")); !errors.Is(err, budget) {
		t.Fatalf("err = %v, want the BeforeFlush error", err)
	}
	if len(f.batches()) != 0 {
		t.Fatal("a refused flush was sent")
	}

	// done runs after the results were handed over, so the test waits for it.
	var seen int
	released := make(chan struct{}, 2)
	c.BeforeFlush = func(reqs []Request) (func(), error) {
		seen += len(reqs)
		return func() { released <- struct{}{} }, nil
	}
	completeAll(t, c, []string{"a", "b"})
	select {
	case <-released:
	case <-time.After(5 * time.Second):
		t.Fatal("done was not called")
	}
	if seen != 2 || len(released) != 0 {
		t.Fatalf("BeforeFlush saw %d requests, done called %d extra times; want 2 and 0", seen, len(released))
	}
}

func TestCoalescerServesCacheHits(t *testing.T) {
	f := &recordingBatch{}
	cc := NewCachingClient(f, NewFileCache(t.TempDir()))
	c := testCoalescer(cc)
	completeAll(t, c, []string{"a"})
	resp, err := c.Complete(context.Background(), req("a"))
	if err != nil {
		t.Fatal(err)
	}
	if !resp.Cached || resp.Usage.CostUSD != 0 {
		t.Fatalf("second call not served from the cache: %+v", resp)
	}
	if b := f.batches(); len(b) != 1 {
		t.Fatalf("batches %v, want only the first call's batch", b)
	}
}
