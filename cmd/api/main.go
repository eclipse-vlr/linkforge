package main

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

const (
	maxCreateLinkBodyBytes = 16 * 1024
	maxURLBytes            = 4096
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
	IncrementClicks(ctx context.Context, code string) error
	GetStats(ctx context.Context, code string) (LinkStats, error)
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
	if len(raw) > maxURLBytes {
		return fmt.Errorf("URL must not exceed %d bytes", maxURLBytes)
	}
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
	if (parsed.Port() != "" && !validPort(parsed.Port())) || strings.HasSuffix(parsed.Host, ":") {
		return fmt.Errorf("URL port must be from 1 to 65535")
	}

	return nil
}

func newRouter(store LinkRepository, logger *slog.Logger, baseURL string) http.Handler {
	mux := http.NewServeMux()
	createLimiter := newCreateLinkLimiter()

	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", http.MethodGet)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{
			"status": "ok",
		})
	})

	mux.Handle("/api/v1/links", createLimiter.middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", http.MethodPost)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		// Optional authentication for public deployments. Local development
		// remains unchanged when CREATE_LINK_API_KEY is not set.
		if expectedKey := os.Getenv("CREATE_LINK_API_KEY"); expectedKey != "" {
			providedKey := r.Header.Get("X-API-Key")
			if subtle.ConstantTimeCompare([]byte(providedKey), []byte(expectedKey)) != 1 {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
		}

		r.Body = http.MaxBytesReader(w, r.Body, maxCreateLinkBodyBytes)
		req, err := decodeCreateLinkRequest(r.Body)
		if err != nil {
			writeJSONRequestError(w, err)
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
				logger.ErrorContext(r.Context(), "failed to generate code", "error", err)
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

			logger.ErrorContext(r.Context(), "failed to save link", "error", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}

		if !saved {
			logger.ErrorContext(r.Context(), "failed to generate unique code", "attempts", maxAttempts)
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
			ShortURL:    baseURL + "/" + code,
			OriginalURL: req.URL,
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)

		if err := json.NewEncoder(w).Encode(response); err != nil {
			logger.ErrorContext(r.Context(), "failed to write response", "error", err)
		}
	})))

	mux.HandleFunc("/api/v1/links/{code}/stats", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", http.MethodGet)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		stats, err := store.GetStats(ctx, r.PathValue("code"))
		if err != nil {
			if errors.Is(err, ErrLinkNotFound) {
				http.NotFound(w, r)
				return
			}
			logger.ErrorContext(r.Context(), "failed to get link stats", "error", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(stats); err != nil {
			logger.ErrorContext(r.Context(), "failed to write stats response", "error", err)
		}
	})

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", http.MethodGet)
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

			logger.ErrorContext(r.Context(), "failed to get link", "error", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}

		statsCtx, cancelStats := context.WithTimeout(r.Context(), 500*time.Millisecond)
		err = store.IncrementClicks(statsCtx, code)
		cancelStats()
		if err != nil {
			logger.ErrorContext(r.Context(), "failed to increment link clicks", "code", code, "error", err)
		}

		http.Redirect(w, r, originalURL, http.StatusFound)
	})

	return mux
}

func decodeCreateLinkRequest(body io.Reader) (CreateLinkRequest, error) {
	decoder := json.NewDecoder(body)
	invalid := errors.New("expected an object containing exactly one url string")
	opening, err := decoder.Token()
	if err != nil {
		return CreateLinkRequest{}, err
	}
	if opening != json.Delim('{') {
		return CreateLinkRequest{}, invalid
	}
	name, err := decoder.Token()
	if err != nil {
		return CreateLinkRequest{}, err
	}
	if name != "url" {
		return CreateLinkRequest{}, invalid
	}
	var value *string
	if err := decoder.Decode(&value); err != nil {
		return CreateLinkRequest{}, err
	}
	if value == nil {
		return CreateLinkRequest{}, invalid
	}
	// Inspect the closing token: struct decoding accepts duplicate and folded keys.
	closing, err := decoder.Token()
	if err != nil {
		return CreateLinkRequest{}, err
	}
	if closing != json.Delim('}') {
		return CreateLinkRequest{}, invalid
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		if err == nil {
			err = invalid
		}
		return CreateLinkRequest{}, err
	}
	return CreateLinkRequest{URL: *value}, nil
}

func writeJSONRequestError(w http.ResponseWriter, err error) {
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
		return
	}
	http.Error(w, "body must contain exactly one JSON object with a url field", http.StatusBadRequest)
}

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if err := run(logger); err != nil {
		logger.Error("application failed", "error", err)
		os.Exit(1)
	}
}

func run(logger *slog.Logger) error {
	cfg, err := loadConfig()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := runMigrations(ctx, cfg.DatabaseURL); err != nil {
		return fmt.Errorf("run migrations: %w", err)
	}
	logger.Info("database migrations applied successfully")

	dbCtx, cancel := context.WithTimeout(ctx, 5*time.Second)

	store, err := NewPostgresStore(dbCtx, cfg.DatabaseURL)
	cancel()
	if err != nil {
		return fmt.Errorf("connect to PostgreSQL: %w", err)
	}
	defer store.Close()
	var repository LinkRepository = store

	if cfg.RedisURL != "" {
		redisClient, err := newRedisClient(cfg.RedisURL, cfg.RedisTimeout)
		if err != nil {
			return err
		}
		defer redisClient.Close()
		redisCtx, cancelRedis := context.WithTimeout(ctx, cfg.RedisTimeout)
		err = redisClient.Ping(redisCtx).Err()
		cancelRedis()
		if err != nil {
			logger.Warn("Redis unavailable; continuing with PostgreSQL", "error", err)
		} else {
			logger.Info("Redis connection ready")
		}
		repository = &CachedLinkRepository{
			LinkRepository: store, cache: &RedisLinkCache{client: redisClient},
			logger: logger, ttl: cfg.RedisCacheTTL, timeout: cfg.RedisTimeout,
		}
	}

	handler := loggingMiddleware(logger, newRouter(repository, logger, cfg.BaseURL))
	server := newHTTPServer(cfg.HTTPAddr, handler)
	listener, err := net.Listen("tcp", server.Addr)
	if err != nil {
		return fmt.Errorf("listen HTTP: %w", err)
	}
	return serveHTTP(ctx, server, listener, logger, cfg.ShutdownTimeout)
}
