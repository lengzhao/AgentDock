package catalog

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/lengzhao/AgentDock/config"
)

// RegistrySource fetches the ACP registry over HTTP and caches it on disk.
//
// A stale cache is better than a hard failure: setup and doctor must still work
// when the CDN is unreachable, and the cache is only advisory (adapters are
// pinned by version in config once chosen).
type RegistrySource struct {
	// URL is the index URL.
	URL string
	// CacheFile is where the last successful fetch is stored.
	CacheFile string
	// TTL is how long a cached copy is considered fresh.
	TTL time.Duration
	// Client is the HTTP client; nil uses a 20s-timeout default.
	Client *http.Client
	// Now is injectable for tests.
	Now func() time.Time
}

// NewRegistrySource returns a source writing to cacheDir.
func NewRegistrySource(indexURL, cacheDir string) *RegistrySource {
	if indexURL == "" {
		indexURL = DefaultIndexURL
	}
	return &RegistrySource{
		URL:       indexURL,
		CacheFile: filepath.Join(cacheDir, "acp-registry.json"),
		TTL:       24 * time.Hour,
	}
}

type cachedIndex struct {
	URL       string    `json:"url"`
	FetchedAt time.Time `json:"fetchedAt"`
	Body      []byte    `json:"body"`
}

func (s *RegistrySource) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// Fetch implements Source.
func (s *RegistrySource) Fetch(ctx context.Context) (Index, error) {
	if idx, ok := s.readCache(false); ok {
		return idx, nil
	}
	idx, err := s.fetchRemote(ctx)
	if err != nil {
		if stale, ok := s.readCache(true); ok {
			return stale, nil
		}
		return Index{}, err
	}
	s.writeCache(idx)
	return idx, nil
}

func (s *RegistrySource) fetchRemote(ctx context.Context) (Index, error) {
	client := s.Client
	if client == nil {
		client = &http.Client{Timeout: 20 * time.Second}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.URL, nil)
	if err != nil {
		return Index{}, fmt.Errorf("build catalog request: %w", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return Index{}, fmt.Errorf("fetch catalog: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Index{}, fmt.Errorf("fetch catalog: HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return Index{}, fmt.Errorf("read catalog: %w", err)
	}
	var idx Index
	if err := json.Unmarshal(body, &idx); err != nil {
		return Index{}, fmt.Errorf("parse catalog: %w", err)
	}
	idx.FetchedAt = s.now()
	return idx, nil
}

func (s *RegistrySource) readCache(allowStale bool) (Index, bool) {
	if s.CacheFile == "" {
		return Index{}, false
	}
	raw, err := os.ReadFile(s.CacheFile)
	if err != nil {
		return Index{}, false
	}
	var cached cachedIndex
	if err := json.Unmarshal(raw, &cached); err != nil {
		return Index{}, false
	}
	if cached.URL != "" && cached.URL != s.URL {
		return Index{}, false
	}
	if !allowStale && s.now().Sub(cached.FetchedAt) > s.TTL {
		return Index{}, false
	}
	var idx Index
	if err := json.Unmarshal(cached.Body, &idx); err != nil {
		return Index{}, false
	}
	idx.FetchedAt = cached.FetchedAt
	return idx, true
}

func (s *RegistrySource) writeCache(idx Index) {
	if s.CacheFile == "" {
		return
	}
	body, err := json.Marshal(idx)
	if err != nil {
		return
	}
	cached := cachedIndex{URL: s.URL, FetchedAt: idx.FetchedAt, Body: body}
	out, err := json.Marshal(cached)
	if err != nil {
		return
	}
	// Cache failures are not fatal: the catalog is advisory.
	_ = config.WriteFileAtomic(s.CacheFile, out)
}
