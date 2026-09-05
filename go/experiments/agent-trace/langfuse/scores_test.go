package agent_trace_langfuse

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestScoreClient(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/public/scores" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		user, password, ok := r.BasicAuth()
		if !ok || user != "pk-test" || password != "sk-test" {
			t.Fatalf("unexpected basic auth: %q %q %t", user, password, ok)
		}
		var score Score
		if err := json.NewDecoder(r.Body).Decode(&score); err != nil {
			t.Fatalf("decode score: %v", err)
		}
		if score.ID != "trace-1:task_success" || score.TraceID != "trace-1" || score.Name != "task_success" || score.DataType != "BOOLEAN" || score.Value != 1 {
			t.Fatalf("unexpected score: %+v", score)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"score-1"}`))
	}))
	defer server.Close()

	client, err := NewScoreClient(server.URL, "pk-test", "sk-test", time.Second)
	if err != nil {
		t.Fatalf("NewScoreClient() error = %v", err)
	}
	if err := client.WriteScore(context.Background(), "trace-1", "task_success", 1, "BOOLEAN", "passed"); err != nil {
		t.Fatalf("WriteScore() error = %v", err)
	}
}

func TestScoreClientRetriesRateLimit(t *testing.T) {
	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if requests == 1 {
			w.Header().Set("Retry-After", "0")
			http.Error(w, `{"message":"rate limited"}`, http.StatusTooManyRequests)
			return
		}
		_, _ = w.Write([]byte(`{"id":"score-1"}`))
	}))
	defer server.Close()

	client, err := NewScoreClient(server.URL, "pk-test", "sk-test", time.Second)
	if err != nil {
		t.Fatalf("NewScoreClient() error = %v", err)
	}
	if err := client.WriteScore(context.Background(), "trace-1", "task_success", 1, "BOOLEAN", "passed"); err != nil {
		t.Fatalf("WriteScore() error = %v", err)
	}
	if requests != 2 {
		t.Fatalf("requests = %d, want 2", requests)
	}
}

func TestScoreClientRetriesEOF(t *testing.T) {
	var requests int
	client, err := NewScoreClient("http://score.test", "pk-test", "sk-test", time.Second)
	if err != nil {
		t.Fatalf("NewScoreClient() error = %v", err)
	}
	client.httpClient = &http.Client{Transport: scoreRoundTripper(func(r *http.Request) (*http.Response, error) {
		requests++
		if requests == 1 {
			return nil, io.EOF
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{"id":"score-1"}`)),
			Header:     make(http.Header),
			Request:    r,
		}, nil
	})}

	if err := client.WriteScore(context.Background(), "trace-1", "task_success", 1, "BOOLEAN", "passed"); err != nil {
		t.Fatalf("WriteScore() error = %v", err)
	}
	if requests != 2 {
		t.Fatalf("requests = %d, want 2", requests)
	}
}

func TestScoreClientHonorsContextCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("request should not be sent after context cancellation")
	}))
	defer server.Close()

	client, err := NewScoreClient(server.URL, "pk-test", "sk-test", time.Second)
	if err != nil {
		t.Fatalf("NewScoreClient() error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err = client.WriteScore(ctx, "trace-1", "task_success", 1, "BOOLEAN", "passed")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("WriteScore() error = %v, want context.Canceled", err)
	}
}

type scoreRoundTripper func(*http.Request) (*http.Response, error)

func (f scoreRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}
