package contract

import (
	"encoding/json"
	"github.com/santhosh-tekuri/jsonschema/v5"
	"testing"
)

func TestCoverageAndClassification(t *testing.T) {
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Operations) != 143 {
		t.Fatalf("review API coverage change: got %d operations", len(c.Operations))
	}
	for _, op := range c.Operations {
		t.Run(op.ID, func(t *testing.T) {
			want := "write"
			if op.Method == "GET" || op.ID == "queryStories" {
				want = "read"
			}
			if op.Safety != want {
				t.Fatalf("unsafe classification %s", op.Safety)
			}
			// Compile every schema, including referenced request bodies and nullable fields.
			err := c.Validate(op, map[string]any{})
			if err != nil {
				if _, ok := err.(*jsonschema.ValidationError); !ok {
					t.Fatalf("schema cannot compile: %v", err)
				}
			}
		})
	}
}
func TestValidateBodies(t *testing.T) {
	c, _ := Load()
	for _, tc := range []struct {
		id, body string
		valid    bool
	}{
		{"createStory", `{"name":"hello","workflow_state_id":123}`, true},
		{"createStory", `{"name":"hello"}`, false},
		{"createStory", `{"name":"hello","workflow_state_id":"123"}`, false},
		{"createStory", `{"name":"hello","workflow_state_id":123,"typo":1}`, false},
		{"updateStory", `{"estimate":null}`, true},
		{"createStoryReaction", `{"emoji":"thumbsup"}`, true},
	} {
		t.Run(tc.id+tc.body, func(t *testing.T) {
			op, _ := c.Find(tc.id)
			var body any
			json.Unmarshal([]byte(tc.body), &body)
			input := map[string]any{"body": body}
			for _, p := range op.Parameters {
				if p.In == "path" {
					input[p.Name] = 123
				}
			}
			err := c.Validate(op, input)
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v, error=%v", tc.valid, err)
			}
		})
	}
}
