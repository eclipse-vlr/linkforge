package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/eclipse-vlr/linkforge/internal/link"
)

type invalidCodeRepository struct {
	link.Repository
	calls int
}

func (s *invalidCodeRepository) Get(context.Context, string) (string, error) {
	s.calls++
	return "", errors.New("unexpected repository lookup")
}

func (s *invalidCodeRepository) GetStats(context.Context, string) (link.Stats, error) {
	s.calls++
	return link.Stats{}, errors.New("unexpected repository lookup")
}

func TestInvalidCodesDoNotReachRepository(t *testing.T) {
	for _, tc := range []struct{ name, code string }{
		{"NUL", "%00"},
		{"invalid UTF-8", "%FF"},
		{"embedded NUL", "abc%00123"},
		{"embedded invalid UTF-8", "abc%FF123"},
		{"non-ASCII", "%C3%A9abcdef"},
		{"short", "short"},
		{"long", "toolong99"},
		{"underscore", "abc_1234"},
		{"hyphen", "abc-1234"},
		{"encoded slash", "abc%2F1234"},
		{"oversized", strings.Repeat("a", 4096)},
	} {
		for _, route := range []struct{ name, path string }{
			{"redirect", "/" + tc.code},
			{"stats", "/api/v1/links/" + tc.code + "/stats"},
		} {
			t.Run(tc.name+"/"+route.name, func(t *testing.T) {
				store := &invalidCodeRepository{}
				rec := httptest.NewRecorder()
				NewRouter(store, newTestLogger(), "https://sho.rt", "").ServeHTTP(
					rec, httptest.NewRequest(http.MethodGet, route.path, nil))
				if rec.Code != http.StatusNotFound {
					t.Errorf("status = %d, want 404", rec.Code)
				}
				if store.calls != 0 {
					t.Errorf("invalid code caused %d repository calls", store.calls)
				}
			})
		}
	}
}
