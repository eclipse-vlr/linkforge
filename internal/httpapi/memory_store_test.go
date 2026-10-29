package httpapi

import (
	"context"
	"sync"

	"github.com/eclipse-vlr/linkforge/internal/link"
)

type LinkStore struct {
	mu     sync.RWMutex
	links  map[string]string
	clicks map[string]int64
}

func NewLinkStore() *LinkStore {
	return &LinkStore{
		links:  make(map[string]string),
		clicks: make(map[string]int64),
	}
}

func (s *LinkStore) Save(ctx context.Context, code, originalURL string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.links[code] = originalURL
	return nil
}

func (s *LinkStore) Get(ctx context.Context, code string) (string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	originalURL, ok := s.links[code]
	if !ok {
		return "", link.ErrNotFound
	}

	return originalURL, nil
}

func (s *LinkStore) IncrementClicks(ctx context.Context, code string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.links[code]; !ok {
		return link.ErrNotFound
	}
	s.clicks[code]++
	return nil
}

func (s *LinkStore) GetStats(ctx context.Context, code string) (link.Stats, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if _, ok := s.links[code]; !ok {
		return link.Stats{}, link.ErrNotFound
	}
	return link.Stats{Code: code, ClickCount: s.clicks[code]}, nil
}
