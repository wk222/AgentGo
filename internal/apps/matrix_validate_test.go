package apps

import "testing"

func TestValidateMatrixOrchestration(t *testing.T) {
	err := ValidateMatrixOrchestration(MatrixOrchestration{
		Name:  "demo",
		Nodes: []MatrixNode{{ID: "n1", AppID: "demo"}},
		Edges: []MatrixEdge{{From: "n1", To: ""}},
	})
	if err != nil {
		t.Fatal(err)
	}

	err = ValidateMatrixOrchestration(MatrixOrchestration{
		Name:  "bad",
		Nodes: []MatrixNode{{ID: "n1", AppID: ""}},
	})
	if err == nil {
		t.Fatal("expected error for missing app_id")
	}
}

func TestValidateMatrixCycles(t *testing.T) {
	// A circular cycle: n1 -> n2 -> n3 -> n1
	circular := MatrixOrchestration{
		Name: "circular_flow",
		Nodes: []MatrixNode{
			{ID: "n1", AppID: "app1"},
			{ID: "n2", AppID: "app2"},
			{ID: "n3", AppID: "app3"},
		},
		Edges: []MatrixEdge{
			{From: "n1", To: "n2"},
			{From: "n2", To: "n3"},
			{From: "n3", To: "n1"},
		},
	}
	err := ValidateMatrixOrchestration(circular)
	if err == nil {
		t.Fatal("expected error for circular topology cycle")
	}
}

func TestValidateMatrixRequiredPorts(t *testing.T) {
	// Node n2 has a required input port 'input_data'
	required := MatrixOrchestration{
		Name: "required_ports_flow",
		Nodes: []MatrixNode{
			{ID: "n1", AppID: "app1"},
			{
				ID:    "n2",
				AppID: "app2",
				InputPorts: []Port{
					{Name: "input_data", DataType: "json", Required: true},
				},
			},
		},
		Edges: []MatrixEdge{
			{From: "n1", To: "n2"}, // connected generally, but not to the required port
		},
	}
	err := ValidateMatrixOrchestration(required)
	if err == nil {
		t.Fatal("expected error since required port 'input_data' is not bound")
	}

	// Correct binding
	requiredCorrect := required
	requiredCorrect.Edges = []MatrixEdge{
		{From: "n1", To: "n2", FromPort: "out", ToPort: "input_data"},
	}
	// We need to add 'out' to node 1's outputs to avoid legacy mismatch checks
	requiredCorrect.Nodes[0].OutputPorts = []Port{{Name: "out", DataType: "json"}}

	err = ValidateMatrixOrchestration(requiredCorrect)
	if err != nil {
		t.Fatalf("expected clean validation for correct required port binding: %v", err)
	}
}

func TestValidateMatrixPortTypes(t *testing.T) {
	// Mismatching data types: string -> json
	mismatched := MatrixOrchestration{
		Name: "type_mismatch_flow",
		Nodes: []MatrixNode{
			{
				ID:          "n1",
				AppID:       "app1",
				OutputPorts: []Port{{Name: "out_str", DataType: "string"}},
			},
			{
				ID:         "n2",
				AppID:      "app2",
				InputPorts: []Port{{Name: "in_json", DataType: "json"}},
			},
		},
		Edges: []MatrixEdge{
			{From: "n1", To: "n2", FromPort: "out_str", ToPort: "in_json"},
		},
	}
	err := ValidateMatrixOrchestration(mismatched)
	if err == nil {
		t.Fatal("expected error for port data type mismatch (string -> json)")
	}
}
