package agent_trace_eval

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// Dataset is the ordered set of cases used by one evaluation run.
type Dataset struct {
	Cases []Case `json:"cases"`
}

// Case describes one user input and its observable expectations.
type Case struct {
	ID       string   `json:"id"`
	Input    string   `json:"input"`
	Expected Expected `json:"expected"`
	Metadata Metadata `json:"metadata"`
}

// Expected contains answer, tool-trajectory, and resource-limit expectations.
type Expected struct {
	AnswerRules    []AnswerRule      `json:"answer_rules,omitempty"`
	RequiredTools  []ToolExpectation `json:"required_tools,omitempty"`
	OptionalTools  []string          `json:"optional_tools,omitempty"`
	ForbiddenTools []string          `json:"forbidden_tools,omitempty"`
	MaxToolCalls   int               `json:"max_tool_calls,omitempty"`
	MaxTurns       int               `json:"max_turns,omitempty"`
}

// AnswerRule requires at least one alternative to appear in the final answer.
type AnswerRule struct {
	AnyOf []string `json:"any_of"`
}

// ToolExpectation describes one required tool call and expected arguments.
type ToolExpectation struct {
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments,omitempty"`
}

// Metadata 用于分类、汇总和记录评估契约说明。
type Metadata struct {
	Category       string `json:"category"`
	Difficulty     string `json:"difficulty,omitempty"`
	EvaluationNote string `json:"evaluation_note,omitempty"`
}

// LoadDataset reads and validates one JSONL case per non-empty line.
func LoadDataset(path string) (Dataset, error) {
	file, err := os.Open(path)
	if err != nil {
		return Dataset{}, fmt.Errorf("open dataset: %w", err)
	}
	defer file.Close()

	var dataset Dataset
	seen := map[string]struct{}{}
	scanner := bufio.NewScanner(file)
	// Allow larger future cases without silently hitting Scanner's 64 KiB default.
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)

	line := 0
	for scanner.Scan() {
		line++
		text := strings.TrimSpace(scanner.Text())
		if text == "" || strings.HasPrefix(text, "#") {
			continue
		}

		var item Case
		if err := json.Unmarshal([]byte(text), &item); err != nil {
			return Dataset{}, fmt.Errorf("decode dataset line %d: %w", line, err)
		}
		if err := validateCase(item); err != nil {
			return Dataset{}, fmt.Errorf("validate dataset line %d: %w", line, err)
		}
		if _, exists := seen[item.ID]; exists {
			return Dataset{}, fmt.Errorf("duplicate case id %q", item.ID)
		}
		seen[item.ID] = struct{}{}
		dataset.Cases = append(dataset.Cases, item)
	}
	if err := scanner.Err(); err != nil {
		return Dataset{}, fmt.Errorf("read dataset: %w", err)
	}
	if len(dataset.Cases) == 0 {
		return Dataset{}, fmt.Errorf("dataset %s contains no cases", path)
	}
	return dataset, nil
}

func validateCase(item Case) error {
	if strings.TrimSpace(item.ID) == "" {
		return fmt.Errorf("case id is required")
	}
	if strings.TrimSpace(item.Input) == "" {
		return fmt.Errorf("case %s input is required", item.ID)
	}
	if item.Expected.MaxToolCalls < 0 || item.Expected.MaxTurns < 0 {
		return fmt.Errorf("case %s max limits must not be negative", item.ID)
	}
	for i, rule := range item.Expected.AnswerRules {
		if len(rule.AnyOf) == 0 {
			return fmt.Errorf("case %s answer_rules[%d].any_of must not be empty", item.ID, i)
		}
	}
	for i, tool := range item.Expected.RequiredTools {
		if strings.TrimSpace(tool.Name) == "" {
			return fmt.Errorf("case %s required_tools[%d].name is required", item.ID, i)
		}
	}
	for i, name := range item.Expected.OptionalTools {
		if strings.TrimSpace(name) == "" {
			return fmt.Errorf("case %s optional_tools[%d] must not be empty", item.ID, i)
		}
	}
	for i, name := range item.Expected.ForbiddenTools {
		if strings.TrimSpace(name) == "" {
			return fmt.Errorf("case %s forbidden_tools[%d] must not be empty", item.ID, i)
		}
	}
	return nil
}
