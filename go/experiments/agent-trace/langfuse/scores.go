package agent_trace_langfuse

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// ScoreClient writes trace-level scores to Langfuse's public API.
type ScoreClient struct {
	endpoint   string
	publicKey  string
	secretKey  string
	httpClient *http.Client
}

// Score is the payload accepted by Langfuse's score endpoint.
type Score struct {
	ID            string  `json:"id,omitempty"`
	TraceID       string  `json:"traceId"`
	ObservationID string  `json:"observationId,omitempty"`
	Name          string  `json:"name"`
	Value         float64 `json:"value"`
	DataType      string  `json:"dataType"`
	Comment       string  `json:"comment,omitempty"`
}

const (
	maxScoreAttempts       = 4
	initialScoreRetryDelay = 500 * time.Millisecond
)

// NewScoreClient creates a client for the Langfuse public score endpoint.
func NewScoreClient(baseURL, publicKey, secretKey string, timeout time.Duration) (*ScoreClient, error) {
	base := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if base == "" {
		return nil, errors.New("Langfuse base URL is required for score ingestion")
	}
	if strings.TrimSpace(publicKey) == "" || strings.TrimSpace(secretKey) == "" {
		return nil, errors.New("Langfuse public and secret keys are required for score ingestion")
	}
	if timeout <= 0 {
		timeout = 10 * time.Second
	}

	endpoint := base + "/api/public/scores"
	if strings.HasSuffix(base, "/api/public") {
		endpoint = base + "/scores"
	} else if strings.HasSuffix(base, "/api/public/scores") {
		endpoint = base
	}

	return &ScoreClient{
		endpoint:   endpoint,
		publicKey:  strings.TrimSpace(publicKey),
		secretKey:  strings.TrimSpace(secretKey),
		httpClient: &http.Client{Timeout: timeout},
	}, nil
}

// WriteScore writes one score using the common evaluator sink contract.
func (c *ScoreClient) WriteScore(
	ctx context.Context,
	traceID string,
	name string,
	value float64,
	dataType string,
	comment string,
) error {
	return c.CreateScore(ctx, Score{
		TraceID:  traceID,
		Name:     name,
		Value:    value,
		DataType: dataType,
		Comment:  comment,
	})
}

// CreateScore 向 Langfuse 写入一个 trace 级分数，并对瞬时 API 错误重试。
func (c *ScoreClient) CreateScore(ctx context.Context, score Score) error {
	if strings.TrimSpace(score.TraceID) == "" {
		return errors.New("score traceId is required")
	}
	if strings.TrimSpace(score.Name) == "" {
		return errors.New("score name is required")
	}
	if score.DataType == "" {
		score.DataType = "NUMERIC"
	}
	if score.ID == "" {
		// 稳定 ID 让响应丢失后的重试不会为同一 trace 和指标创建重复分数。
		score.ID = score.TraceID + ":" + score.Name
	}

	body, err := json.Marshal(score)
	if err != nil {
		return fmt.Errorf("marshal Langfuse score: %w", err)
	}

	for attempt := range maxScoreAttempts {
		request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(body))
		if err != nil {
			return fmt.Errorf("create Langfuse score request: %w", err)
		}
		request.Header.Set("Content-Type", "application/json")
		request.SetBasicAuth(c.publicKey, c.secretKey)

		response, err := c.httpClient.Do(request)
		if err != nil {
			if !retryableScoreError(err) || attempt == maxScoreAttempts-1 {
				return fmt.Errorf("send Langfuse score: %w", err)
			}
			if err := waitForScoreRetry(ctx, scoreRetryDelay("", attempt)); err != nil {
				return fmt.Errorf("wait before retrying Langfuse score: %w", err)
			}
			continue
		}

		responseBody, readErr := io.ReadAll(io.LimitReader(response.Body, 1<<20))
		closeErr := response.Body.Close()
		if readErr != nil {
			if !retryableScoreError(readErr) || attempt == maxScoreAttempts-1 {
				return fmt.Errorf("read Langfuse score response: %w", readErr)
			}
			if err := waitForScoreRetry(ctx, scoreRetryDelay("", attempt)); err != nil {
				return fmt.Errorf("wait before retrying Langfuse score: %w", err)
			}
			continue
		}
		if closeErr != nil {
			return fmt.Errorf("close Langfuse score response: %w", closeErr)
		}
		if response.StatusCode >= 200 && response.StatusCode < 300 {
			return nil
		}

		if !retryableScoreStatus(response.StatusCode) || attempt == maxScoreAttempts-1 {
			return fmt.Errorf("Langfuse score API status=%d body=%s", response.StatusCode, truncate(string(responseBody), 1000))
		}
		if err := waitForScoreRetry(ctx, scoreRetryDelay(response.Header.Get("Retry-After"), attempt)); err != nil {
			return fmt.Errorf("wait before retrying Langfuse score: %w", err)
		}
	}

	return errors.New("Langfuse score retries exhausted")
}

func retryableScoreError(err error) bool {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}
	var networkErr net.Error
	return errors.As(err, &networkErr)
}

func retryableScoreStatus(statusCode int) bool {
	return statusCode == http.StatusTooManyRequests || statusCode >= http.StatusInternalServerError
}

func scoreRetryDelay(retryAfter string, attempt int) time.Duration {
	if seconds, err := strconv.Atoi(strings.TrimSpace(retryAfter)); err == nil && seconds >= 0 {
		return time.Duration(seconds) * time.Second
	}
	if retryAt, err := http.ParseTime(strings.TrimSpace(retryAfter)); err == nil {
		if delay := time.Until(retryAt); delay > 0 {
			return delay
		}
		return 0
	}

	return initialScoreRetryDelay * time.Duration(1<<attempt)
}

func waitForScoreRetry(ctx context.Context, delay time.Duration) error {
	if delay <= 0 {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			return nil
		}
	}

	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func truncate(value string, max int) string {
	if len(value) <= max {
		return value
	}
	return value[:max] + "..."
}
