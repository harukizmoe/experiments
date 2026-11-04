package agent_trace_langfuse

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestDatasetClientSyncCreatesDatasetAndItems(t *testing.T) {
	var datasetCreates int
	var itemRequests []DatasetItem

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, password, ok := r.BasicAuth()
		if !ok || user != "pk-test" || password != "sk-test" {
			t.Fatalf("unexpected basic auth: %q %q %t", user, password, ok)
		}

		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/public/v2/datasets/phase3":
			http.NotFound(w, r)
		case r.Method == http.MethodPost && r.URL.Path == "/api/public/v2/datasets":
			datasetCreates++
			var dataset Dataset
			if err := json.NewDecoder(r.Body).Decode(&dataset); err != nil {
				t.Fatalf("decode dataset: %v", err)
			}
			if dataset.Name != "phase3" || dataset.Description == "" {
				t.Fatalf("unexpected dataset: %+v", dataset)
			}
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"id":"dataset-1","name":"phase3"}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/public/dataset-items":
			var item DatasetItem
			if err := json.NewDecoder(r.Body).Decode(&item); err != nil {
				t.Fatalf("decode dataset item: %v", err)
			}
			itemRequests = append(itemRequests, item)
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"id":"case-1"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client, err := NewDatasetClient(server.URL, "pk-test", "sk-test", time.Second)
	if err != nil {
		t.Fatalf("NewDatasetClient() error = %v", err)
	}

	dataset, err := client.Sync(
		context.Background(),
		"phase3",
		"deterministic evaluation",
		map[string]any{"phase": "3"},
		[]DatasetItem{{
			ID:    "case-1",
			Input: map[string]any{"text": "hello"},
			ExpectedOutput: map[string]any{
				"answer": "hello",
			},
			Metadata: map[string]any{"category": "smoke"},
		}},
	)
	if err != nil {
		t.Fatalf("Sync() error = %v", err)
	}
	if dataset.ID != "dataset-1" || dataset.Name != "phase3" {
		t.Fatalf("unexpected dataset response: %+v", dataset)
	}
	if datasetCreates != 1 {
		t.Fatalf("dataset create requests = %d, want 1", datasetCreates)
	}
	if len(itemRequests) != 1 {
		t.Fatalf("dataset item requests = %d, want 1", len(itemRequests))
	}
	if itemRequests[0].DatasetName != "phase3" || itemRequests[0].ID != "case-1" {
		t.Fatalf("unexpected dataset item: %+v", itemRequests[0])
	}
}

func TestDatasetClientSyncReusesExistingDataset(t *testing.T) {
	var datasetCreates int
	var itemRequests int

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/public/v2/datasets/phase3":
			_, _ = w.Write([]byte(`{"id":"dataset-1","name":"phase3"}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/public/v2/datasets":
			datasetCreates++
			http.Error(w, "dataset should not be created", http.StatusInternalServerError)
		case r.Method == http.MethodPost && r.URL.Path == "/api/public/dataset-items":
			itemRequests++
			w.WriteHeader(http.StatusCreated)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client, err := NewDatasetClient(server.URL, "pk-test", "sk-test", time.Second)
	if err != nil {
		t.Fatalf("NewDatasetClient() error = %v", err)
	}

	got, err := client.Sync(
		context.Background(),
		"phase3",
		"deterministic evaluation",
		nil,
		[]DatasetItem{{ID: "case-1"}},
	)
	if err != nil {
		t.Fatalf("Sync() error = %v", err)
	}
	if got.ID != "dataset-1" {
		t.Fatalf("dataset ID = %q, want dataset-1", got.ID)
	}
	if datasetCreates != 0 || itemRequests != 1 {
		t.Fatalf("create requests = %d, item requests = %d", datasetCreates, itemRequests)
	}
}

func TestDatasetClientUpsertItemPreservesAPIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"message":"rate limited"}`, http.StatusTooManyRequests)
	}))
	defer server.Close()

	client, err := NewDatasetClient(server.URL, "pk-test", "sk-test", time.Second)
	if err != nil {
		t.Fatalf("NewDatasetClient() error = %v", err)
	}

	err = client.UpsertItem(context.Background(), DatasetItem{
		DatasetName: "phase3",
		ID:          "case-1",
	})
	if err == nil {
		t.Fatal("UpsertItem() error = nil, want API error")
	}
	if !strings.Contains(err.Error(), "status=429") || !strings.Contains(err.Error(), "rate limited") {
		t.Fatalf("UpsertItem() error = %v", err)
	}
}
