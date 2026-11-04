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
		"single_tool":         1,
		"after_sales":         4,
		"argument_extraction": 5,
		"multi_tool":          4,
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
		if item.Metadata.Category == "after_sales" {
			if item.Expected.ExpectedBusinessOutcome == nil {
				t.Fatalf("case %s must define expected business outcome", item.ID)
			}
			if len(item.Expected.ToolOrder) == 0 {
				t.Fatalf("case %s must define tool order", item.ID)
			}
		}
	}
	var foundMultiTurn bool
	for _, item := range dataset.Cases {
		if item.ID != "after-sales-001" {
			continue
		}
		foundMultiTurn = true
		if len(item.FollowUps) != 1 || len(item.Expected.TurnExpectations) != 2 {
			t.Fatalf("after-sales-001 multi-turn contract = follow_ups=%d turn_expectations=%d", len(item.FollowUps), len(item.Expected.TurnExpectations))
		}
		if len(item.Expected.TurnExpectations[0].RequiredTools) != 0 || len(item.Expected.TurnExpectations[1].RequiredTools) != 4 {
			t.Fatalf("after-sales-001 turn tool contract = %+v", item.Expected.TurnExpectations)
		}
	}
	if !foundMultiTurn {
		t.Fatal("after-sales-001 case not found")
	}
}
