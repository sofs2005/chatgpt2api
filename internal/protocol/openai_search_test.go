package protocol

import "testing"

func TestBuildSearchPayloadUsesModelAndQuery(t *testing.T) {
	payload := BuildOpenAISearchPayload("find debug traces", "auto")
	if payload["query"] != "find debug traces" {
		t.Fatalf("query = %#v", payload["query"])
	}
	if payload["model"] != "auto" {
		t.Fatalf("model = %#v", payload["model"])
	}
}
