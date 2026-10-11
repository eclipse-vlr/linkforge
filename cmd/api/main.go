package main

import (
	"crypto/rand"
	"encoding/json"
	"log"
	"math/big"
	"net/http"
	"strings"
	"sync"
)

type CreateLinkRequest struct {
	URL string `json:"url"`
}

type CreateLinkResponse struct {
	Code        string `json:"code"`
	ShortURL    string `json:"short_url"`
	OriginalURL string `json:"original_url"`
}

type LinkStore struct {
	mu    sync.RWMutex
	links map[string]string
}

func NewLinkStore() *LinkStore {
	return &LinkStore{
		links: make(map[string]string),
	}
}

func (s *LinkStore) Save(code, url string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.links[code] = url
}

func (s *LinkStore) Get(code string) (string, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	url, ok := s.links[code]
	return url, ok
}

func generateCode() (string, error) {
	const alphabet = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	const codeLength = 8

	var code strings.Builder
	code.Grow(codeLength)

	for i := 0; i < codeLength; i++ {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(alphabet))))
		if err != nil {
			return "", err
		}

		code.WriteByte(alphabet[n.Int64()])
	}

	return code.String(), nil
}

func newRouter(store *LinkStore) http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{
			"status": "ok",
		})
	})

	mux.HandleFunc("/api/v1/links", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		var req CreateLinkRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid JSON", http.StatusBadRequest)
			return
		}

		req.URL = strings.TrimSpace(req.URL)
		if req.URL == "" {
			http.Error(w, "url is required", http.StatusBadRequest)
			return
		}

		code, err := generateCode()
		if err != nil {
			log.Printf("failed to generate code: %v", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}

		store.Save(code, req.URL)

		response := CreateLinkResponse{
			Code:        code,
			ShortURL:    "http://localhost:8080/" + code,
			OriginalURL: req.URL,
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)

		if err := json.NewEncoder(w).Encode(response); err != nil {
			log.Printf("failed to write response: %v", err)
		}
	})

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		code := strings.TrimPrefix(r.URL.Path, "/")

		if code == "" || strings.Contains(code, "/") {
			http.NotFound(w, r)
			return
		}

		originalURL, ok := store.Get(code)
		if !ok {
			http.NotFound(w, r)
			return
		}

		http.Redirect(w, r, originalURL, http.StatusFound)
	})

	return mux
}

func main() {
	store := NewLinkStore()

	log.Println("LinkForge started on :8080")
	if err := http.ListenAndServe(":8080", newRouter(store)); err != nil {
		log.Fatal(err)
	}
}
