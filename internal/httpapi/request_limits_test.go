package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/eclipse-vlr/linkforge/internal/link"
)

func TestCreateLinkRequestLimits(t *testing.T) {
	urlBody := func(size int) string {
		value := "https://example.com/" + strings.Repeat("a", size-len("https://example.com/"))
		body, err := json.Marshal(CreateLinkRequest{URL: value})
		if err != nil {
			t.Fatal(err)
		}
		return string(body)
	}
	valid := `{"url":"https://example.com"}`
	for _, tc := range []struct {
		name, body string
		status     int
	}{
		{"URL at limit", urlBody(link.MaxURLBytes), http.StatusCreated},
		{"URL over limit", urlBody(link.MaxURLBytes + 1), http.StatusBadRequest},
		{"body at limit", valid + strings.Repeat(" ", maxCreateLinkBodyBytes-len(valid)), http.StatusCreated},
		{"body over limit", urlBody(maxCreateLinkBodyBytes), http.StatusRequestEntityTooLarge},
		{"excess trailing whitespace", valid + strings.Repeat(" ", maxCreateLinkBodyBytes), http.StatusRequestEntityTooLarge},
		{"second object", valid + `{}`, http.StatusBadRequest},
		{"second null", valid + `null`, http.StatusBadRequest},
		{"trailing junk", valid + `not-json`, http.StatusBadRequest},
		{"unknown field", `{"url":"https://example.com","extra":1}`, http.StatusBadRequest},
		{"duplicate URL", `{"url":"https://example.com","url":"https://other.example"}`, http.StatusBadRequest},
		{"duplicate null URL", `{"url":"https://example.com","url":null}`, http.StatusBadRequest},
		{"escaped duplicate URL", `{"url":"https://example.com","\u0075rl":"https://other.example"}`, http.StatusBadRequest},
		{"wrong field case", `{"URL":"https://example.com"}`, http.StatusBadRequest},
		{"null", `null`, http.StatusBadRequest},
		{"array", `[]`, http.StatusBadRequest},
		{"empty", ``, http.StatusBadRequest},
		{"whitespace allowed", " \n" + valid + "\t\r\n", http.StatusCreated},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := NewLinkStore()
			req := httptest.NewRequest(http.MethodPost, "/api/v1/links", strings.NewReader(tc.body))
			// The limit must work even without a trustworthy Content-Length.
			req.ContentLength = -1
			rec := httptest.NewRecorder()
			NewRouter(store, newTestLogger(), "https://sho.rt", "").ServeHTTP(rec, req)
			if rec.Code != tc.status {
				t.Fatalf("status = %d, want %d: %s", rec.Code, tc.status, rec.Body.String())
			}
			wantSaved := 0
			if tc.status == http.StatusCreated {
				wantSaved = 1
			}
			if len(store.links) != wantSaved {
				t.Fatalf("saved %d links, want %d", len(store.links), wantSaved)
			}
		})
	}
}
