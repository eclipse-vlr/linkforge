package main

import (
	"context"
	"sync"
)

type LinkStore struct {
	mu    sync.RWMutex
	links map[string]string
}

func NewLinkStore() *LinkStore {
	return &LinkStore{
		links: make(map[string]string),
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
