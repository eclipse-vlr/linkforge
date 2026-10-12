package main

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math/big"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

type CreateLinkRequest struct {
	URL string `json:"url"`
}

type CreateLinkResponse struct {
	Code        string `json:"code"`
	ShortURL    string `json:"short_url"`
	OriginalURL string `json:"original_url"`
}

type LinkRepository interface {
	Save(ctx context.Context, code, originalURL string) error
	Get(ctx context.Context, code string) (string, error)
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

func validateURL(raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil {
		return err
	}

	if !parsed.IsAbs() {
		return fmt.Errorf("URL must be absolute")
	}

	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return fmt.Errorf("only http and https schemes are allowed")
	}

	if parsed.Host == "" || parsed.Hostname() == "" {
		return fmt.Errorf("URL must contain a hostname")
	}

	return nil
}

func newRouter(store LinkRepository) http.Handler {
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

		if err := validateURL(req.URL); err != nil {
			http.Error(w, "invalid URL: "+err.Error(), http.StatusBadRequest)
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()

		var code string
		saved := false

		const maxAttempts = 5

		for attempt := 0; attempt < maxAttempts; attempt++ {
			generatedCode, err := generateCode()
			if err != nil {
				log.Printf("failed to generate code: %v", err)
				http.Error(w, "internal server error", http.StatusInternalServerError)
				return
			}

			err = store.Save(ctx, generatedCode, req.URL)
			if err == nil {
				code = generatedCode
				saved = true
				break
			}

			if errors.Is(err, ErrCodeConflict) {
				continue
			}

			log.Printf("failed to save link: %v", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}

		if !saved {
			http.Error(w, "failed to generate unique code", http.StatusInternalServerError)
			return
		}

		if code == "" {
			http.Error(
				w,
				"failed to generate unique code",
				http.StatusInternalServerError,
			)
			return
		}

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

		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()

		originalURL, err := store.Get(ctx, code)
		if err != nil {
			if errors.Is(err, ErrLinkNotFound) {
				http.NotFound(w, r)
				return
			}

			log.Printf("failed to get link: %v", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}

		http.Redirect(w, r, originalURL, http.StatusFound)
	})

	return mux
}

func main() {
	connString := os.Getenv("DATABASE_URL")
	if connString == "" {
		connString = "postgres://linkforge:devpassword@localhost:5432/linkforge?sslmode=disable"
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	store, err := NewPostgresStore(ctx, connString)
	if err != nil {
		log.Fatalf("failed to connect to PostgreSQL: %v", err)
	}
	defer store.Close()

	log.Println("LinkForge started on :8080")
	if err := http.ListenAndServe(":8080", newRouter(store)); err != nil {
		log.Fatal(err)
	}
}
