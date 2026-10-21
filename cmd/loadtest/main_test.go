package main

import (
	"encoding/json"
	"flag"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"sync/atomic"
	"testing"
)

// Run the real CLI in a child process so flag parsing and exit codes are tested.
func TestLoadtestProcess(t *testing.T) {
	if os.Getenv("LINKFORGE_LOADTEST_HELPER") != "1" {
		return
	}
	for i, arg := range os.Args {
		if arg == "--" {
			os.Args = append(os.Args[:1], os.Args[i+1:]...)
			break
		}
	}
	flag.CommandLine = flag.NewFlagSet("loadtest", flag.ExitOnError)
	main()
	os.Exit(0)
}

func runLoadtest(t *testing.T, base string) ([]byte, error) {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(executable, "-test.run=^TestLoadtestProcess$", "--",
		"-base", base, "-requests", "5", "-workers", "2")
	cmd.Env = append(os.Environ(), "LINKFORGE_LOADTEST_HELPER=1")
	return cmd.CombinedOutput()
}

func TestLoadtestAPIKeyAndRedirects(t *testing.T) {
	for _, key := range []string{"", "test-secret"} {
		t.Run("key="+key, func(t *testing.T) {
			t.Setenv("CREATE_LINK_API_KEY", key)
			var redirects, unexpected atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.Method == http.MethodPost && r.URL.Path == "/api/v1/links":
					if r.Header.Get("X-API-Key") != key {
						http.Error(w, "unauthorized", http.StatusUnauthorized)
						return
					}
					w.WriteHeader(http.StatusCreated)
					_, _ = w.Write([]byte(`{"code":"abc12345"}`))
				case r.Method == http.MethodGet && r.URL.Path == "/abc12345":
					redirects.Add(1)
					if r.Header.Get("X-API-Key") != "" {
						unexpected.Add(1)
					}
					http.Redirect(w, r, "/must-not-follow", http.StatusFound)
				default:
					unexpected.Add(1)
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			output, err := runLoadtest(t, server.URL)
			if err != nil {
				t.Fatalf("loadtest failed: %v\n%s", err, output)
			}
			var result report
			if err := json.Unmarshal(output, &result); err != nil {
				t.Fatalf("invalid report: %v\n%s", err, output)
			}
			if result.SuccessfulRedirects != 5 || result.NetworkErrors != 0 ||
				result.UnexpectedResponses != 0 || redirects.Load() != 5 || unexpected.Load() != 0 {
				t.Fatalf("incorrect load test: %+v, redirects=%d unexpected=%d", result, redirects.Load(), unexpected.Load())
			}
		})
	}
}

func TestLoadtestRejectsInvalidBase(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		http.Error(w, "unexpected request", http.StatusBadRequest)
	}))
	defer server.Close()
	for _, base := range []string{server.URL + "?", server.URL + "#", "http://127.0.0.1:0", "http://127.0.0.1:65536"} {
		t.Run(base, func(t *testing.T) {
			output, err := runLoadtest(t, base)
			if err == nil || strings.Contains(string(output), "create test link") || strings.Contains(string(output), "create link returned") {
				t.Fatalf("invalid base reached HTTP request: %v\n%s", err, output)
			}
		})
	}
	if calls.Load() != 0 {
		t.Fatalf("invalid base caused %d HTTP requests", calls.Load())
	}
}
