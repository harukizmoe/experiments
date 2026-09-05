package agent_trace_eval

import "testing"

func TestPhase3Dataset(t *testing.T) {
	dataset, err := LoadDataset("../evaldata/cases.jsonl")
	if err != nil {
		t.Fatalf("LoadDataset() error = %v", err)
	}
	if len(dataset.Cases) != 20 {
		t.Fatalf("cases = %d, want 20", len(dataset.Cases))
	}

	counts := map[string]int{}
	for _, item := range dataset.Cases {
		counts[item.Metadata.Category]++
	}
	want := map[string]int{
		"no_tool":             1,
		"single_tool":         4,
		"argument_extraction": 5,
		"multi_tool":          5,
		"edge_adversarial":    5,
	}
	for category, n := range want {
		if counts[category] != n {
			t.Fatalf("category %s = %d, want %d", category, counts[category], n)
		}
	}

	for _, item := range dataset.Cases {
		if (item.ID == "edge-001" || item.ID == "edge-005") && item.Metadata.EvaluationNote == "" {
			t.Fatalf("case %s must document its tool-policy expectation", item.ID)
		}
	}
}
