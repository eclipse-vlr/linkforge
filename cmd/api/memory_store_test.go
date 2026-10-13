package main

import (
	"context"
	"sync"
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
		return "", ErrLinkNotFound
	}

	return originalURL, nil
}

func (s *LinkStore) IncrementClicks(ctx context.Context, code string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.links[code]; !ok {
		return ErrLinkNotFound
	}
	s.clicks[code]++
	return nil
}

func (s *LinkStore) GetStats(ctx context.Context, code string) (LinkStats, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if _, ok := s.links[code]; !ok {
		return LinkStats{}, ErrLinkNotFound
	}
	return LinkStats{Code: code, ClickCount: s.clicks[code]}, nil
}
