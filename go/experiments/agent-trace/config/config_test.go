package agent_trace_config

import "testing"

func setMinimumConfigEnvironment(t *testing.T) {
	t.Helper()
	t.Setenv("LLM_BASE_URL", "http://localhost:8000/v1")
	t.Setenv("LLM_MODEL", "test-model")
	t.Setenv("LANGFUSE_ENABLED", "false")
	t.Setenv("EVAL_UPLOAD_DATASET", "")
	t.Setenv("EVAL_UPLOAD_SCORES", "")
}

func TestLoadDefaultsToLocalEvaluation(t *testing.T) {
	setMinimumConfigEnvironment(t)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Eval.UploadDataset || cfg.Eval.UploadScores {
		t.Fatalf("evaluation uploads = dataset:%t scores:%t, want both false", cfg.Eval.UploadDataset, cfg.Eval.UploadScores)
	}
}

func TestLoadAllowsExplicitCloudUploadOptIn(t *testing.T) {
	setMinimumConfigEnvironment(t)
	t.Setenv("EVAL_UPLOAD_DATASET", "true")
	t.Setenv("EVAL_UPLOAD_SCORES", "true")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !cfg.Eval.UploadDataset || !cfg.Eval.UploadScores {
		t.Fatalf("evaluation uploads = dataset:%t scores:%t, want both true", cfg.Eval.UploadDataset, cfg.Eval.UploadScores)
	}
}
