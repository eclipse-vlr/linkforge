// Command loadtest runs a bounded redirect load test against a local LinkForge instance.
// It never follows redirects to external websites.
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

type createResponse struct {
	Code string `json:"code"`
}
type measurement struct {
	Milliseconds float64
	Status       int
	Err          error
}
type report struct {
	CreatedCode         string      `json:"created_code"`
	TotalRequests       int         `json:"total_requests"`
	Concurrency         int         `json:"concurrency"`
	SuccessfulRedirects int         `json:"successful_redirects"`
	UnexpectedResponses int         `json:"unexpected_responses"`
	NetworkErrors       int         `json:"network_errors"`
	DurationSeconds     float64     `json:"duration_seconds"`
	RequestsPerSecond   float64     `json:"requests_per_second"`
	P50Milliseconds     float64     `json:"p50_milliseconds"`
	P95Milliseconds     float64     `json:"p95_milliseconds"`
	P99Milliseconds     float64     `json:"p99_milliseconds"`
	HTTPStatuses        map[int]int `json:"http_statuses"`
}

func main() {
	base := flag.String("base", "http://127.0.0.1:8080", "local LinkForge URL")
	count := flag.Int("requests", 500, "GET requests (1..10000)")
	workers := flag.Int("workers", 10, "concurrent clients (1..50)")
	output := flag.String("out", "", "optional JSON report path")
	flag.Parse()
	if *count < 1 || *count > 10000 || *workers < 1 || *workers > 50 {
		fail("requests must be 1..10000 and workers must be 1..50")
	}
	parsed, err := url.Parse(*base)
	if err != nil || parsed.Scheme != "http" || parsed.User != nil ||
		parsed.RawQuery != "" || parsed.ForceQuery || strings.Contains(*base, "#") ||
		(parsed.Path != "" && parsed.Path != "/") {
		fail("base must be a local http URL without path, query or credentials")
	}
	port, portErr := strconv.Atoi(parsed.Port())
	if !isLoopback(parsed.Hostname()) || portErr != nil || port < 1 || port > 65535 {
		fail("for safety, only localhost/127.0.0.1/[::1] with a port from 1 to 65535 is supported")
	}
	baseURL := strings.TrimRight(parsed.String(), "/")
	tr := &http.Transport{
		MaxIdleConns: *workers, MaxIdleConnsPerHost: *workers,
		MaxConnsPerHost: *workers, IdleConnTimeout: 30 * time.Second,
	}
	defer tr.CloseIdleConnections()
	client := &http.Client{
		Transport: tr, Timeout: 5 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	body := bytes.NewBufferString("{\"url\":\"https://example.com/load-test\"}")
	req, err := http.NewRequest(http.MethodPost, baseURL+"/api/v1/links", body)
	if err != nil {
		fail("create test link request: " + err.Error())
	}
	req.Header.Set("Content-Type", "application/json")
	if key := os.Getenv("CREATE_LINK_API_KEY"); key != "" {
		req.Header.Set("X-API-Key", key)
	}
	resp, err := client.Do(req)
	if err != nil {
		fail("create test link: " + err.Error())
	}
	payload, readErr := io.ReadAll(io.LimitReader(resp.Body, 4096))
	resp.Body.Close()
	if readErr != nil || resp.StatusCode != http.StatusCreated {
		fail(fmt.Sprintf("create link returned HTTP %d, body %q, read error %v", resp.StatusCode, payload, readErr))
	}
	var created createResponse
	if err := json.Unmarshal(payload, &created); err != nil || len(created.Code) != 8 {
		fail("invalid create-link response")
	}
	jobs := make(chan struct{})
	results := make(chan measurement, *count)
	var wg sync.WaitGroup
	start := time.Now()
	for i := 0; i < *workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range jobs {
				started := time.Now()
				r, err := client.Get(baseURL + "/" + created.Code)
				m := measurement{Err: err}
				if err == nil {
					m.Status = r.StatusCode
					_, readErr := io.Copy(io.Discard, io.LimitReader(r.Body, 4096))
					r.Body.Close()
					if readErr != nil {
						m.Err = readErr
					}
				}
				m.Milliseconds = float64(time.Since(started).Microseconds()) / 1000
				results <- m
			}
		}()
	}
	for i := 0; i < *count; i++ {
		jobs <- struct{}{}
	}
	close(jobs)
	wg.Wait()
	close(results)
	duration := time.Since(start).Seconds()
	result := report{
		CreatedCode: created.Code, TotalRequests: *count, Concurrency: *workers,
		DurationSeconds: duration, HTTPStatuses: map[int]int{},
	}
	timings := make([]float64, 0, *count)
	for m := range results {
		if m.Err != nil {
			result.NetworkErrors++
			continue
		}
		result.HTTPStatuses[m.Status]++
		if m.Status == http.StatusFound {
			result.SuccessfulRedirects++
			timings = append(timings, m.Milliseconds)
		} else {
			result.UnexpectedResponses++
		}
	}
	if len(timings) > 0 {
		sort.Float64s(timings)
		result.P50Milliseconds = percentile(timings, .50)
		result.P95Milliseconds = percentile(timings, .95)
		result.P99Milliseconds = percentile(timings, .99)
		result.RequestsPerSecond = float64(result.SuccessfulRedirects) / duration
	}
	reportJSON, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		fail(err.Error())
	}
	fmt.Println(string(reportJSON))
	if *output != "" {
		if err := os.WriteFile(*output, append(reportJSON, '\n'), 0600); err != nil {
			fail("write report: " + err.Error())
		}
	}
	if result.NetworkErrors > 0 || result.UnexpectedResponses > 0 {
		fail(fmt.Sprintf("load test errors: network=%d unexpected_http=%d", result.NetworkErrors, result.UnexpectedResponses))
	}
}
func isLoopback(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
func percentile(values []float64, p float64) float64 {
	if len(values) == 0 {
		return 0
	}
	i := int(math.Ceil(float64(len(values))*p)) - 1
	if i < 0 {
		i = 0
	}
	return values[i]
}
func fail(msg string) { fmt.Fprintln(os.Stderr, "loadtest:", msg); os.Exit(1) }
