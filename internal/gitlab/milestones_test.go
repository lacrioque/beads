package gitlab

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestListMilestones(t *testing.T) {
	milestones := []Milestone{
		{ID: 1, IID: 1, Title: "v1.0", State: "active"},
		{ID: 2, IID: 2, Title: "v2.0", State: "closed"},
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v4/projects/123/milestones" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.Header.Get("PRIVATE-TOKEN") != "test-token" {
			t.Errorf("missing or wrong auth token")
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(milestones)
	}))
	defer server.Close()

	client := NewClient("test-token", server.URL, "123")
	result, err := client.ListMilestones(context.Background(), "")
	if err != nil {
		t.Fatalf("ListMilestones failed: %v", err)
	}

	if len(result) != 2 {
		t.Fatalf("got %d milestones, want 2", len(result))
	}
	if result[0].Title != "v1.0" {
		t.Errorf("first milestone title = %q, want %q", result[0].Title, "v1.0")
	}
	if result[1].State != "closed" {
		t.Errorf("second milestone state = %q, want %q", result[1].State, "closed")
	}
}

func TestListMilestones_WithStateFilter(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("state") != "active" {
			t.Errorf("expected state=active, got %q", r.URL.Query().Get("state"))
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode([]Milestone{})
	}))
	defer server.Close()

	client := NewClient("test-token", server.URL, "123")
	_, err := client.ListMilestones(context.Background(), "active")
	if err != nil {
		t.Fatalf("ListMilestones failed: %v", err)
	}
}

func TestGetMilestone(t *testing.T) {
	ms := Milestone{ID: 42, IID: 5, Title: "Sprint 5", State: "active"}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v4/projects/123/milestones/42" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(ms)
	}))
	defer server.Close()

	client := NewClient("test-token", server.URL, "123")
	result, err := client.GetMilestone(context.Background(), 42)
	if err != nil {
		t.Fatalf("GetMilestone failed: %v", err)
	}
	if result.Title != "Sprint 5" {
		t.Errorf("title = %q, want %q", result.Title, "Sprint 5")
	}
}

func TestCreateMilestone(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if r.URL.Path != "/api/v4/projects/123/milestones" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}

		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		if body["title"] != "v3.0" {
			t.Errorf("title = %v, want v3.0", body["title"])
		}
		if body["description"] != "Third release" {
			t.Errorf("description = %v, want 'Third release'", body["description"])
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(Milestone{
			ID: 10, IID: 10, Title: "v3.0", State: "active",
		})
	}))
	defer server.Close()

	client := NewClient("test-token", server.URL, "123")
	result, err := client.CreateMilestone(context.Background(), "v3.0", "Third release", "", "")
	if err != nil {
		t.Fatalf("CreateMilestone failed: %v", err)
	}
	if result.ID != 10 {
		t.Errorf("ID = %d, want 10", result.ID)
	}
}

func TestUpdateMilestone(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			t.Errorf("expected PUT, got %s", r.Method)
		}
		if r.URL.Path != "/api/v4/projects/123/milestones/5" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(Milestone{
			ID: 5, IID: 5, Title: "Updated", State: "closed",
		})
	}))
	defer server.Close()

	client := NewClient("test-token", server.URL, "123")
	result, err := client.UpdateMilestone(context.Background(), 5, map[string]any{
		"title":       "Updated",
		"state_event": "close",
	})
	if err != nil {
		t.Fatalf("UpdateMilestone failed: %v", err)
	}
	if result.Title != "Updated" {
		t.Errorf("title = %q, want %q", result.Title, "Updated")
	}
}

func TestListMilestoneIssues(t *testing.T) {
	issues := []Issue{
		{ID: 100, IID: 1, Title: "Fix bug", State: "opened"},
		{ID: 101, IID: 2, Title: "Add feature", State: "closed"},
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v4/projects/123/milestones/5/issues" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(issues)
	}))
	defer server.Close()

	client := NewClient("test-token", server.URL, "123")
	result, err := client.ListMilestoneIssues(context.Background(), 5)
	if err != nil {
		t.Fatalf("ListMilestoneIssues failed: %v", err)
	}
	if len(result) != 2 {
		t.Fatalf("got %d issues, want 2", len(result))
	}
}
