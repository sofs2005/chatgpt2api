package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSub2APIListRemoteGroupsUsesActiveOpenAIGroupsEndpoint(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/admin/groups/all" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		if r.URL.Query().Get("platform") != "openai" {
			t.Fatalf("platform query = %q, want openai", r.URL.Query().Get("platform"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":0,"message":"success","data":[{"id":12,"name":"Team","description":"Main","platform":"openai","status":"active","account_count":3,"active_account_count":2}]}`))
	}))
	defer server.Close()

	service := NewSub2APIService(NewSub2APIConfig(t.TempDir()), nil)
	groups, err := service.ListRemoteGroups(context.Background(), map[string]any{
		"base_url": server.URL,
		"api_key":  "test-key",
	})
	if err != nil {
		t.Fatalf("ListRemoteGroups() error = %v", err)
	}
	if len(groups) != 1 {
		t.Fatalf("ListRemoteGroups() length = %d, want 1: %#v", len(groups), groups)
	}
	if groups[0]["id"] != "12" || groups[0]["name"] != "Team" {
		t.Fatalf("ListRemoteGroups() group = %#v", groups[0])
	}
}

func TestSub2APIListRemoteGroupsReturnsEmptyArrayForNullItems(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/admin/groups/all" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":0,"message":"success","data":null}`))
	}))
	defer server.Close()

	service := NewSub2APIService(NewSub2APIConfig(t.TempDir()), nil)
	groups, err := service.ListRemoteGroups(context.Background(), map[string]any{
		"base_url": server.URL,
		"api_key":  "test-key",
	})
	if err != nil {
		t.Fatalf("ListRemoteGroups() error = %v", err)
	}
	if groups == nil {
		t.Fatal("ListRemoteGroups() = nil, want empty slice")
	}
	if len(groups) != 0 {
		t.Fatalf("ListRemoteGroups() length = %d, want 0", len(groups))
	}
}
