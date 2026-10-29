// Package link defines short-link behavior and persistence contracts.
package link

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"math/big"
	"net/url"
	"strconv"
	"strings"
)

const (
	CodeLength  = 8
	MaxURLBytes = 4096
)

var (
	ErrNotFound     = errors.New("link not found")
	ErrCodeConflict = errors.New("short code already exists")
)

type Stats struct {
	Code       string `json:"code"`
	ClickCount int64  `json:"click_count"`
}

type Repository interface {
	Save(ctx context.Context, code, originalURL string) error
	Get(ctx context.Context, code string) (string, error)
	IncrementClicks(ctx context.Context, code string) error
	GetStats(ctx context.Context, code string) (Stats, error)
}

func GenerateCode() (string, error) {
	const alphabet = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"

	var code strings.Builder
	code.Grow(CodeLength)

	for i := 0; i < CodeLength; i++ {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(alphabet))))
		if err != nil {
			return "", err
		}

		code.WriteByte(alphabet[n.Int64()])
	}

	return code.String(), nil
}

func ValidCode(code string) bool {
	if len(code) != CodeLength {
		return false
	}
	for _, c := range code {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9') {
			return false
		}
	}
	return true
}

func ValidateURL(raw string) error {
	if len(raw) > MaxURLBytes {
		return fmt.Errorf("URL must not exceed %d bytes", MaxURLBytes)
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

func validPort(port string) bool {
	n, err := strconv.Atoi(port)
	return err == nil && n >= 1 && n <= 65535
}
