package agent_trace_config

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	LLM           LLMConfig
	Agent         AgentConfig
	Observability ObservabilityConfig
	Eval          EvalConfig
}

type LLMConfig struct {
	BaseURL     string
	APIKey      string
	Model       string
	Temperature float64
	MaxTokens   int
	Timeout     time.Duration
}

type AgentConfig struct {
	MaxTurns int
	Debug    bool
}

type ObservabilityConfig struct {
	Enabled            bool
	LangfuseBaseURL    string
	OTLPTracesEndpoint string
	PublicKey          string
	SecretKey          string
	Environment        string
	ServiceName        string
	ServiceVersion     string
	TraceName          string
	CaptureContent     bool
	ExportTimeout      time.Duration
}

// EvalConfig controls the local benchmark, Dataset sync, and score upload behavior.
type EvalConfig struct {
	DatasetPath   string
	DatasetName   string
	ReportPath    string
	RunName       string
	TraceName     string
	Environment   string
	UploadDataset bool
	UploadScores  bool
}

func Load() (Config, error) {
	// .env is optional. Existing environment variables always win.
	_ = loadDotEnv("experiments/agent-trace/.env")

	cfg := Config{
		LLM: LLMConfig{
			BaseURL:     strings.TrimSpace(os.Getenv("LLM_BASE_URL")),
			APIKey:      strings.TrimSpace(os.Getenv("LLM_API_KEY")),
			Model:       strings.TrimSpace(os.Getenv("LLM_MODEL")),
			Temperature: 0,
			MaxTokens:   2048,
			Timeout:     60 * time.Second,
		},
		Agent: AgentConfig{
			MaxTurns: 8,
			Debug:    false,
		},
		Observability: ObservabilityConfig{
			Enabled:            false,
			LangfuseBaseURL:    valueOrDefault("LANGFUSE_BASE_URL", "https://cloud.langfuse.com"),
			OTLPTracesEndpoint: strings.TrimSpace(os.Getenv("LANGFUSE_OTLP_TRACES_ENDPOINT")),
			PublicKey:          strings.TrimSpace(os.Getenv("LANGFUSE_PUBLIC_KEY")),
			SecretKey:          strings.TrimSpace(os.Getenv("LANGFUSE_SECRET_KEY")),
			Environment:        valueOrDefault("LANGFUSE_ENVIRONMENT", "development"),
			ServiceName:        valueOrDefault("OTEL_SERVICE_NAME", "go-agent-eval"),
			ServiceVersion:     valueOrDefault("OTEL_SERVICE_VERSION", "phase-3"),
			TraceName:          valueOrDefault("LANGFUSE_TRACE_NAME", "agent-chat"),
			CaptureContent:     true,
			ExportTimeout:      10 * time.Second,
		},
		Eval: EvalConfig{
			DatasetPath:   valueOrDefault("EVAL_DATASET_PATH", "experiments/agent-trace/evaldata/cases.jsonl"),
			DatasetName:   valueOrDefault("EVAL_DATASET_NAME", "go-agent-eval-phase3"),
			ReportPath:    valueOrDefault("EVAL_REPORT_PATH", "experiments/agent-trace/eval-results/latest.json"),
			RunName:       valueOrDefault("EVAL_RUN_NAME", "baseline-v1"),
			TraceName:     valueOrDefault("EVAL_TRACE_NAME", "agent-eval"),
			Environment:   valueOrDefault("EVAL_ENVIRONMENT", "experiment"),
			UploadDataset: true,
			UploadScores:  true,
		},
	}

	var err error

	if v := strings.TrimSpace(os.Getenv("LLM_TEMPERATURE")); v != "" {
		cfg.LLM.Temperature, err = strconv.ParseFloat(v, 64)
		if err != nil {
			return Config{}, fmt.Errorf("parse LLM_TEMPERATURE: %w", err)
		}
	}

	if v := strings.TrimSpace(os.Getenv("LLM_MAX_TOKENS")); v != "" {
		cfg.LLM.MaxTokens, err = strconv.Atoi(v)
		if err != nil || cfg.LLM.MaxTokens <= 0 {
			return Config{}, fmt.Errorf("LLM_MAX_TOKENS must be a positive integer")
		}
	}

	if v := strings.TrimSpace(os.Getenv("LLM_TIMEOUT_SECONDS")); v != "" {
		seconds, parseErr := strconv.Atoi(v)
		if parseErr != nil || seconds <= 0 {
			return Config{}, fmt.Errorf("LLM_TIMEOUT_SECONDS must be a positive integer")
		}

		cfg.LLM.Timeout = time.Duration(seconds) * time.Second
	}

	if v := strings.TrimSpace(os.Getenv("AGENT_MAX_TURNS")); v != "" {
		cfg.Agent.MaxTurns, err = strconv.Atoi(v)
		if err != nil || cfg.Agent.MaxTurns <= 0 {
			return Config{}, fmt.Errorf("AGENT_MAX_TURNS must be a positive integer")
		}
	}

	if v := strings.TrimSpace(os.Getenv("AGENT_DEBUG")); v != "" {
		cfg.Agent.Debug, err = strconv.ParseBool(v)
		if err != nil {
			return Config{}, fmt.Errorf("parse AGENT_DEBUG: %w", err)
		}
	}

	if v := strings.TrimSpace(os.Getenv("LANGFUSE_ENABLED")); v != "" {
		cfg.Observability.Enabled, err = strconv.ParseBool(v)
		if err != nil {
			return Config{}, fmt.Errorf("parse LANGFUSE_ENABLED: %w", err)
		}
	}

	if v := strings.TrimSpace(os.Getenv("LANGFUSE_CAPTURE_CONTENT")); v != "" {
		cfg.Observability.CaptureContent, err = strconv.ParseBool(v)
		if err != nil {
			return Config{}, fmt.Errorf("parse LANGFUSE_CAPTURE_CONTENT: %w", err)
		}
	}

	if v := strings.TrimSpace(os.Getenv("LANGFUSE_EXPORT_TIMEOUT_SECONDS")); v != "" {
		seconds, parseErr := strconv.Atoi(v)
		if parseErr != nil || seconds <= 0 {
			return Config{}, fmt.Errorf("LANGFUSE_EXPORT_TIMEOUT_SECONDS must be a positive integer")
		}

		cfg.Observability.ExportTimeout = time.Duration(seconds) * time.Second
	}
	if v := strings.TrimSpace(os.Getenv("EVAL_UPLOAD_SCORES")); v != "" {
		cfg.Eval.UploadScores, err = strconv.ParseBool(v)
		if err != nil {
			return Config{}, fmt.Errorf("parse EVAL_UPLOAD_SCORES: %w", err)
		}
	}

	if v := strings.TrimSpace(os.Getenv("EVAL_UPLOAD_DATASET")); v != "" {
		cfg.Eval.UploadDataset, err = strconv.ParseBool(v)
		if err != nil {
			return Config{}, fmt.Errorf("parse EVAL_UPLOAD_DATASET: %w", err)
		}
	}

	if cfg.LLM.BaseURL == "" {
		return Config{}, errors.New("LLM_BASE_URL is required")
	}

	if cfg.LLM.Model == "" {
		return Config{}, errors.New("LLM_MODEL is required")
	}

	if cfg.LLM.Temperature < 0 || cfg.LLM.Temperature > 2 {
		return Config{}, errors.New("LLM_TEMPERATURE must be between 0 and 2")
	}
	if strings.TrimSpace(cfg.Eval.DatasetName) == "" {
		return Config{}, errors.New("EVAL_DATASET_NAME must not be empty")
	}

	if strings.TrimSpace(cfg.Eval.RunName) == "" {
		return Config{}, errors.New("EVAL_RUN_NAME must not be empty")
	}

	if (cfg.Eval.UploadScores || cfg.Eval.UploadDataset) &&
		cfg.Observability.Enabled &&
		cfg.Observability.LangfuseBaseURL == "" {
		return Config{}, errors.New(
			"LANGFUSE_BASE_URL is required when Langfuse upload is enabled",
		)
	}

	if cfg.Observability.Enabled {
		if cfg.Observability.PublicKey == "" {
			return Config{}, errors.New(
				"LANGFUSE_PUBLIC_KEY is required when LANGFUSE_ENABLED=true",
			)
		}

		if cfg.Observability.SecretKey == "" {
			return Config{}, errors.New(
				"LANGFUSE_SECRET_KEY is required when LANGFUSE_ENABLED=true",
			)
		}

		if cfg.Observability.LangfuseBaseURL == "" &&
			cfg.Observability.OTLPTracesEndpoint == "" {

			return Config{}, errors.New(
				"LANGFUSE_BASE_URL or LANGFUSE_OTLP_TRACES_ENDPOINT is required",
			)
		}
	}

	cfg.LLM.BaseURL = strings.TrimRight(
		cfg.LLM.BaseURL,
		"/",
	)

	cfg.Observability.LangfuseBaseURL = strings.TrimRight(
		cfg.Observability.LangfuseBaseURL,
		"/",
	)

	return cfg, nil
}

func valueOrDefault(
	key string,
	fallback string,
) string {
	if value := strings.TrimSpace(
		os.Getenv(key),
	); value != "" {
		return value
	}

	return fallback
}

func loadDotEnv(path string) error {
	file, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}

		return err
	}

	defer file.Close()

	scanner := bufio.NewScanner(file)

	for scanner.Scan() {
		line := strings.TrimSpace(
			scanner.Text(),
		)

		if line == "" ||
			strings.HasPrefix(line, "#") {

			continue
		}

		if strings.HasPrefix(
			line,
			"export ",
		) {
			line = strings.TrimSpace(
				strings.TrimPrefix(
					line,
					"export ",
				),
			)
		}

		key, value, ok := strings.Cut(
			line,
			"=",
		)

		if !ok {
			continue
		}

		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)

		if key == "" {
			continue
		}

		if len(value) >= 2 {
			if (value[0] == '"' &&
				value[len(value)-1] == '"') ||
				(value[0] == '\'' &&
					value[len(value)-1] == '\'') {

				value = value[1 : len(value)-1]
			}
		}

		if _, exists := os.LookupEnv(
			key,
		); !exists {
			_ = os.Setenv(
				key,
				value,
			)
		}
	}

	return scanner.Err()
}
