package llm

import (
	"context"
	"errors"
	"sync"
	"time"
)

// Coalescer is a Client that gathers concurrent Complete calls into Message Batches.
//
// The generation pipeline is written request by request (realize → verify → retry with the
// verifier's reasons, per triplet slot). Run with every slot in flight, each step of all slots
// arrives within a short time; the Coalescer sends what has queued up as one batch and hands every
// caller its own result, so realization and verification get the batch price without rewriting the
// pipeline as explicit rounds (plan §10.3: at list price the Anthropic share does not fit one
// credit cycle).
//
// Flushes are serialized: one batch is in flight at a time, and the requests that arrive meanwhile
// form the next one. The inner client is normally a CachingClient, so results are stored before a
// batch is forgotten and a rerun reads them from the cache (the money-safety rules of
// anthropic_batch.go apply unchanged).
type Coalescer struct {
	inner BatchClient
	// Quiet flushes the queue once no request has arrived for this long.
	Quiet time.Duration
	// MaxWait flushes the queue at the latest this long after its oldest request arrived, so a
	// steady trickle (e.g. verifications of a rate-limited other-family realizer) is not held back.
	MaxWait time.Duration
	// MaxRequests bounds one flush; the rest waits for the next.
	MaxRequests int
	// ItemRetries is how often a request whose batch item failed retryably (expired, canceled,
	// overloaded, ...) is queued again. The request is unchanged, so the caller sees what the
	// ThrottledClient's retries give on the request-by-request path, not a failed attempt.
	ItemRetries int
	// BeforeFlush, when set, is called with the requests of a flush before they are sent. An error
	// fails every one of them with that error (nothing is sent); otherwise done, when non-nil, is
	// called after the results were handed to the callers (used to hold a budget reservation).
	BeforeFlush func(reqs []Request) (done func(), err error)

	mu      sync.Mutex
	queue   []*coalesced
	newest  time.Time
	running bool
}

type coalesced struct {
	req   Request
	at    time.Time
	tries int
	ch    chan BatchResult
}

var (
	_ BatchClient      = (*Coalescer)(nil)
	_ RequestDefaulter = (*Coalescer)(nil)
)

// NewCoalescer wraps inner with the default timing: flush after 5 s without a new request or 2 min
// after the oldest one, at most 20,000 requests per batch, 3 resubmissions of a failed item.
func NewCoalescer(inner BatchClient) *Coalescer {
	return &Coalescer{inner: inner, Quiet: 5 * time.Second, MaxWait: 2 * time.Minute, MaxRequests: 20_000, ItemRetries: 3}
}

// ApplyDefaults forwards to the inner client (see RequestDefaulter).
func (c *Coalescer) ApplyDefaults(req Request) Request { return applyDefaults(c.inner, req) }

// CompleteBatch forwards to the inner client: the caller already has a batch.
func (c *Coalescer) CompleteBatch(ctx context.Context, reqs []Request) ([]BatchResult, error) {
	return c.inner.CompleteBatch(ctx, reqs)
}

// cacheLookup is implemented by CachingClient.
type cacheLookup interface {
	Lookup(req Request) (*Response, error)
}

// Complete queues req for the next batch and waits for its result. A cache hit is returned at once.
// If ctx ends first, ctx.Err() is returned; the request stays in its batch and its result is still
// stored by the inner client.
func (c *Coalescer) Complete(ctx context.Context, req Request) (*Response, error) {
	if err := req.Validate(); err != nil {
		return nil, err
	}
	if l, ok := c.inner.(cacheLookup); ok {
		if hit, err := l.Lookup(req); err != nil || hit != nil {
			return hit, err
		}
	}
	p := &coalesced{req: req}
	for {
		p.ch = make(chan BatchResult, 1)
		c.enqueue(p)
		var r BatchResult
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case r = <-p.ch:
		}
		if r.Response == nil && r.Err != nil && IsRetryable(r.Err) && p.tries < c.ItemRetries {
			p.tries++
			continue
		}
		if r.Response == nil && r.Err == nil {
			r.Err = errors.New("llm: batch returned no result for this request")
		}
		return r.Response, r.Err
	}
}

func (c *Coalescer) enqueue(p *coalesced) {
	c.mu.Lock()
	defer c.mu.Unlock()
	p.at = time.Now()
	c.queue = append(c.queue, p)
	c.newest = p.at
	if !c.running {
		c.running = true
		go c.dispatch()
	}
}

// dispatch flushes the queue whenever the flush rule holds and exits once the queue is empty.
func (c *Coalescer) dispatch() {
	for {
		c.mu.Lock()
		if len(c.queue) == 0 {
			c.running = false
			c.mu.Unlock()
			return
		}
		wait := min(time.Until(c.newest.Add(c.Quiet)), time.Until(c.queue[0].at.Add(c.MaxWait)))
		if wait > 0 && len(c.queue) < c.maxRequests() {
			c.mu.Unlock()
			time.Sleep(wait)
			continue
		}
		n := min(len(c.queue), c.maxRequests())
		batch := c.queue[:n:n]
		c.queue = append([]*coalesced(nil), c.queue[n:]...)
		c.mu.Unlock()
		c.flush(batch)
	}
}

func (c *Coalescer) maxRequests() int {
	if c.MaxRequests <= 0 {
		return anthropicBatchRequestLimit
	}
	return c.MaxRequests
}

func (c *Coalescer) flush(batch []*coalesced) {
	reqs := make([]Request, len(batch))
	for i, p := range batch {
		reqs[i] = p.req
	}
	var done func()
	if c.BeforeFlush != nil {
		d, err := c.BeforeFlush(reqs)
		if err != nil {
			for _, p := range batch {
				p.ch <- BatchResult{Err: err}
			}
			return
		}
		done = d
	}
	// The batch is not tied to any caller's context: a caller that gives up must not cancel the
	// others' requests, and an interrupted batch is resumed from its state file on the next run.
	res, err := c.inner.CompleteBatch(context.Background(), reqs)
	for i, p := range batch {
		r := BatchResult{Err: err}
		if i < len(res) && (res[i].Response != nil || res[i].Err != nil) {
			r = res[i]
		}
		p.ch <- r
	}
	if done != nil {
		done()
	}
}
