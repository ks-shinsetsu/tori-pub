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
type CachingClient struct {
	inner Client
	cache *FileCache
}

var _ Client = (*CachingClient)(nil)

func NewCachingClient(inner Client, cache *FileCache) *CachingClient {
	return &CachingClient{inner: inner, cache: cache}
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
	if err := c.cache.Put(req.Tag, key, resp); err != nil {
		return nil, err
	}
	return resp, nil
}

var _ BatchClient = (*CachingClient)(nil)

// CompleteBatch serves cached requests from the FileCache and sends only the misses to the inner
// client, in a single CompleteBatch call when the inner client is a BatchClient (otherwise the
// misses are completed one by one with Complete). Successful responses are stored; failed items
// are not cached, so a rerun retries exactly those.
func (c *CachingClient) CompleteBatch(ctx context.Context, reqs []Request) ([]BatchResult, error) {
	out := make([]BatchResult, len(reqs))
	norm := make([]Request, len(reqs))
	keys := make([]string, len(reqs))
	var missIdx []int
	for i, req := range reqs {
		norm[i] = c.ApplyDefaults(req)
		keys[i] = CacheKey(norm[i])
		hit, err := c.lookup(norm[i].Tag, keys[i])
		switch {
		case err != nil:
			out[i].Err = err
		case hit != nil:
			out[i].Response = hit
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

	var results []BatchResult
	if bc, ok := c.inner.(BatchClient); ok {
		var err error
		results, err = bc.CompleteBatch(ctx, misses)
		if err != nil {
			return nil, err
		}
		if len(results) != len(misses) {
			return nil, fmt.Errorf("llm: batch returned %d results for %d requests", len(results), len(misses))
		}
	} else {
		results = make([]BatchResult, len(misses))
		for j, req := range misses {
			start := time.Now()
			resp, err := c.inner.Complete(ctx, req)
			if ctxErr := ctx.Err(); ctxErr != nil {
				return nil, ctxErr
			}
			if err == nil && resp.LatencyMS == 0 {
				resp.LatencyMS = time.Since(start).Milliseconds()
			}
			results[j] = BatchResult{Response: resp, Err: err}
		}
	}

	for j, i := range missIdx {
		r := results[j]
		if r.Err == nil && r.Response != nil {
			if err := c.cache.Put(norm[i].Tag, keys[i], r.Response); err != nil {
				// Keep the (paid) response; the caller decides whether a cache write failure is fatal.
				r.Err = err
			}
		}
		out[i] = r
	}
	return out, nil
}
