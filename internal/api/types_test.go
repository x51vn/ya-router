package api

import (
	"encoding/json"
	"testing"
)

func TestModelListJSONContract(t *testing.T) {
	payload, err := json.Marshal(ModelList{Object: "list", Data: []Model{{ID: "github/gpt-test", Object: "model", OwnedBy: "github-copilot"}}})
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]interface{}
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded["object"] != "list" {
		t.Fatalf("object=%v", decoded["object"])
	}
}
