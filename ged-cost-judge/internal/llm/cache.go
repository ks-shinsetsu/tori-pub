package llm

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"time"
)

// ErrCacheMiss is returned when the entry is not in the cache.
var ErrCacheMiss = errors.New("llm: cache miss")

// CacheKey builds a deterministic key from a Request.
//
// Only values that affect the result are included. Tag is not included (it separates the storage directory).
// The schema body is included because changing the schema changes the output.
// **Changing this invalidates every existing cache entry, making reported numbers irreproducible.**
func CacheKey(req Request) string {
	type keyShape struct {
		Model       string    `json:"model"`
		Messages    []Message `json:"messages"`
		Temperature *float64  `json:"temperature,omitempty"`
		Seed        *int64    `json:"seed,omitempty"`
		MaxTokens   int64     `json:"max_tokens,omitempty"`
		SchemaName  string    `json:"schema_name,omitempty"`
		SchemaJSON  string    `json:"schema_json,omitempty"`
		Strict      bool      `json:"strict,omitempty"`
		// Added with the Anthropic path. omitempty keeps the keys of requests that leave them
		// empty (all OpenAI requests made before) byte-identical.
		Thinking string `json:"thinking,omitempty"`
		Effort   string `json:"effort,omitempty"`
	}
	k := keyShape{
		Model:       req.Model,
		Messages:    req.Messages,
		Temperature: req.Temperature,
		Seed:        req.Seed,
		MaxTokens:   req.MaxTokens,
		Thinking:    req.Thinking,
		Effort:      req.Effort,
	}
	if req.Schema != nil {
		k.SchemaName = req.Schema.Name
		k.SchemaJSON = string(req.Schema.JSON)
		k.Strict = req.Schema.Strict
	}
	// encoding/json fixes struct field order and map key order, so this is deterministic.
	b, err := json.Marshal(k)
	if err != nil {
		// Message/float can always be marshaled, so reaching here is an implementation bug.
		panic(fmt.Sprintf("llm: cache key marshal: %v", err))
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

var safeTag = regexp.MustCompile(`[^a-zA-Z0-9_.-]+`)

// FileCache is a filesystem cache of completion results.
type FileCache struct {
	dir string
}

func NewFileCache(dir string) *FileCache { return &FileCache{dir: dir} }

func (c *FileCache) path(tag, key string) string {
	t := safeTag.ReplaceAllString(tag, "_")
	if t == "" {
		t = "default"
	}
	return filepath.Join(c.dir, t, key+".json")
}

// Get returns the stored response, or ErrCacheMiss if there is none.
func (c *FileCache) Get(tag, key string) (*Response, error) {
	b, err := os.ReadFile(c.path(tag, key))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ErrCacheMiss
		}
		return nil, fmt.Errorf("llm: read cache: %w", err)
	}
	var resp Response
	if err := json.Unmarshal(b, &resp); err != nil {
		return nil, fmt.Errorf("llm: parse cache %s: %w", c.path(tag, key), err)
	}
	return &resp, nil
}

func (c *FileCache) Put(tag, key string, resp *Response) error {
	b, err := json.MarshalIndent(resp, "", "  ")
	if err != nil {
		return fmt.Errorf("llm: marshal cache entry: %w", err)
	}
	return writeFileAtomic(c.path(tag, key), b)
}

// writeFileAtomic replaces path via a temporary file,
// so concurrent runs or interruptions do not leave broken JSON in the cache.
func writeFileAtomic(path string, b []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("llm: create cache dir: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return fmt.Errorf("llm: create temp cache file: %w", err)
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := tmp.Write(b); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("llm: write cache: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("llm: close cache: %w", err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("llm: commit cache: %w", err)
	}
	return nil
}

// CachingClient is a caching decorator for Client. On a hit, inner is not called.
//
// Truncated responses (finish reason max_tokens / length) are never stored: a truncated structured
// output would otherwise fail to decode forever under the same key. Refusals are stored.
type CachingClient struct {
	inner Client
	cache *FileCache
	// BatchRetry bounds the retries of CompleteBatch after a whole-call failure (default
	// DefaultRetryConfig). Retrying is money-safe: results already stored are cache hits on the
	// next attempt, and batches not yet stored are resumed, not resubmitted.
	BatchRetry RetryConfig
}

var _ Client = (*CachingClient)(nil)

func NewCachingClient(inner Client, cache *FileCache) *CachingClient {
	return &CachingClient{inner: inner, cache: cache, BatchRetry: DefaultRetryConfig()}
}

// ApplyDefaults forwards to the inner client (see RequestDefaulter).
func (c *CachingClient) ApplyDefaults(req Request) Request { return applyDefaults(c.inner, req) }

// lookup returns the cached response marked as a hit, or (nil, nil) on a miss.
func (c *CachingClient) lookup(tag, key string) (*Response, error) {
	cached, err := c.cache.Get(tag, key)
	switch {
	case err == nil:
		hit := *cached
		hit.Cached = true
		// Cache hits are not billed, so they are not mixed into cost totals.
		hit.Usage.CostUSD = 0
		return &hit, nil
	case errors.Is(err, ErrCacheMiss):
		return nil, nil
	default:
		return nil, err
	}
}

// StoreBatchResult implements BatchStore: it writes resp to the cache unless it is truncated.
func (c *CachingClient) StoreBatchResult(tag, key string, resp *Response) error {
	if resp == nil || resp.Truncated() {
		return nil
	}
	return c.cache.Put(tag, key, resp)
}

func (c *CachingClient) Complete(ctx context.Context, req Request) (*Response, error) {
	// Client-level defaults (e.g. the Anthropic default effort) must be part of the key.
	req = c.ApplyDefaults(req)
	key := CacheKey(req)
	if hit, err := c.lookup(req.Tag, key); err != nil || hit != nil {
		return hit, err
	}
	start := time.Now()
	resp, err := c.inner.Complete(ctx, req)
	if err != nil {
		return nil, err
	}
	if resp.LatencyMS == 0 {
		resp.LatencyMS = time.Since(start).Milliseconds()
	}
	if err := c.StoreBatchResult(req.Tag, key, resp); err != nil {
		return nil, err
	}
	return resp, nil
}

var (
	_ BatchClient = (*CachingClient)(nil)
	_ BatchStore  = (*CachingClient)(nil)
)

// CompleteBatch serves cached requests from the FileCache and sends only the misses to the inner
// client: through CompleteBatchStore when the inner client is a StoringBatchClient (results are
// stored before the batch is forgotten, including results of resumed batches for requests outside
// this call), through CompleteBatch for a plain BatchClient, otherwise one by one with Complete.
// Failed items are not cached, so a rerun retries exactly those.
//
// A whole-call failure that IsRetryable is retried (BatchRetry) after re-reading the cache, so
// what was already stored is not requested again. On a final failure, the results obtained so far
// are returned together with the error.
func (c *CachingClient) CompleteBatch(ctx context.Context, reqs []Request) ([]BatchResult, error) {
	out := make([]BatchResult, len(reqs))
	norm := make([]Request, len(reqs))
	keys := make([]string, len(reqs))
	done := make([]bool, len(reqs))
	for i, req := range reqs {
		norm[i] = c.ApplyDefaults(req)
		keys[i] = CacheKey(norm[i])
	}
	retry := c.BatchRetry
	if retry.MaxAttempts <= 0 {
		retry = DefaultRetryConfig()
	}
	for attempt := 0; ; attempt++ {
		var missIdx []int
		for i := range reqs {
			if done[i] {
				continue
			}
			hit, err := c.lookup(norm[i].Tag, keys[i])
			switch {
			case err != nil:
				out[i], done[i] = BatchResult{Err: err}, true
			case hit != nil:
				out[i], done[i] = BatchResult{Response: hit}, true
			default:
				missIdx = append(missIdx, i)
			}
		}
		if len(missIdx) == 0 {
			return out, nil
		}
		misses := make([]Request, len(missIdx))
		for j, i := range missIdx {
			misses[j] = norm[i]
		}

		results, stored, err := c.completeMisses(ctx, misses)
		last := err == nil || !IsRetryable(err) || attempt >= retry.MaxAttempts-1 || ctx.Err() != nil
		for j, i := range missIdx {
			if j >= len(results) {
				break
			}
			r := results[j]
			if r.Response == nil && !last && err != nil {
				continue // no answer yet: try again on the next attempt
			}
			if r.Response != nil && !stored {
				if perr := c.StoreBatchResult(norm[i].Tag, keys[i], r.Response); perr != nil {
					// Keep the (paid) response; the caller decides whether a cache write failure is fatal.
					r.Err = perr
				}
			}
			out[i], done[i] = r, true
		}
		if err == nil {
			return out, nil
		}
		if last {
			for i := range reqs {
				if !done[i] {
					out[i].Err = err
				}
			}
			return out, err
		}
		if serr := sleepBackoff(ctx, retry, attempt); serr != nil {
			return out, errors.Join(err, serr)
		}
	}
}

// completeMisses sends the misses to the inner client. stored reports whether the inner client
// already wrote the successful results to the cache (StoringBatchClient).
func (c *CachingClient) completeMisses(ctx context.Context, misses []Request) (results []BatchResult, stored bool, err error) {
	if sc, ok := c.inner.(StoringBatchClient); ok {
		results, err = sc.CompleteBatchStore(ctx, misses, c)
		return results, true, checkLen(results, misses, err)
	}
	if bc, ok := c.inner.(BatchClient); ok {
		results, err = bc.CompleteBatch(ctx, misses)
		return results, false, checkLen(results, misses, err)
	}
	results = make([]BatchResult, len(misses))
	for j, req := range misses {
		start := time.Now()
		resp, err := c.inner.Complete(ctx, req)
		if ctxErr := ctx.Err(); ctxErr != nil {
			return results[:j], false, ctxErr
		}
		if err == nil && resp.LatencyMS == 0 {
			resp.LatencyMS = time.Since(start).Milliseconds()
		}
		results[j] = BatchResult{Response: resp, Err: err}
	}
	return results, false, nil
}

func checkLen(results []BatchResult, reqs []Request, err error) error {
	if err == nil && len(results) != len(reqs) {
		return fmt.Errorf("llm: batch returned %d results for %d requests", len(results), len(reqs))
	}
	return err
}
