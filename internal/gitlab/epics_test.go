package gitlab

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestListEpics(t *testing.T) {
	epics := []Epic{
		{ID: 1, IID: 1, GroupID: 5, Title: "Platform Refactor", State: "opened"},
		{ID: 2, IID: 2, GroupID: 5, Title: "Security Hardening", State: "closed"},
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v4/groups/5/epics" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.Header.Get("PRIVATE-TOKEN") != "test-token" {
			t.Errorf("missing or wrong auth token")
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(epics)
	}))
	defer server.Close()

	client := NewClient("test-token", server.URL, "123")
	result, err := client.ListEpics(context.Background(), "5", "")
	if err != nil {
		t.Fatalf("ListEpics failed: %v", err)
	}

	if len(result) != 2 {
		t.Fatalf("got %d epics, want 2", len(result))
	}
	if result[0].Title != "Platform Refactor" {
		t.Errorf("first epic title = %q, want %q", result[0].Title, "Platform Refactor")
	}
}

func TestListEpics_WithStateFilter(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("state") != "opened" {
			t.Errorf("expected state=opened, got %q", r.URL.Query().Get("state"))
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode([]Epic{})
	}))
	defer server.Close()

	client := NewClient("test-token", server.URL, "123")
	_, err := client.ListEpics(context.Background(), "5", "opened")
	if err != nil {
		t.Fatalf("ListEpics failed: %v", err)
	}
}

func TestGetEpic(t *testing.T) {
	epic := Epic{ID: 42, IID: 3, GroupID: 5, Title: "Q1 Goals", State: "opened"}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v4/groups/5/epics/3" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(epic)
	}))
	defer server.Close()

	client := NewClient("test-token", server.URL, "123")
	result, err := client.GetEpic(context.Background(), "5", 3)
	if err != nil {
		t.Fatalf("GetEpic failed: %v", err)
	}
	if result.Title != "Q1 Goals" {
		t.Errorf("title = %q, want %q", result.Title, "Q1 Goals")
	}
}

func TestCreateEpic(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if r.URL.Path != "/api/v4/groups/5/epics" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}

		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		if body["title"] != "New Epic" {
			t.Errorf("title = %v, want 'New Epic'", body["title"])
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(Epic{
			ID: 10, IID: 10, GroupID: 5, Title: "New Epic", State: "opened",
		})
	}))
	defer server.Close()

	client := NewClient("test-token", server.URL, "123")
	result, err := client.CreateEpic(context.Background(), "5", "New Epic", "Description", nil)
	if err != nil {
		t.Fatalf("CreateEpic failed: %v", err)
	}
	if result.ID != 10 {
		t.Errorf("ID = %d, want 10", result.ID)
	}
}

func TestUpdateEpic(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			t.Errorf("expected PUT, got %s", r.Method)
		}
		if r.URL.Path != "/api/v4/groups/5/epics/3" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(Epic{
			ID: 42, IID: 3, GroupID: 5, Title: "Updated Epic", State: "closed",
		})
	}))
	defer server.Close()

	client := NewClient("test-token", server.URL, "123")
	result, err := client.UpdateEpic(context.Background(), "5", 3, map[string]any{
		"title":       "Updated Epic",
		"state_event": "close",
	})
	if err != nil {
		t.Fatalf("UpdateEpic failed: %v", err)
	}
	if result.Title != "Updated Epic" {
		t.Errorf("title = %q, want %q", result.Title, "Updated Epic")
	}
}

func TestListEpicIssues(t *testing.T) {
	links := []EpicIssueLink{
		{ID: 1, Issue: &Issue{ID: 100, IID: 1, Title: "Task A"}},
		{ID: 2, Issue: &Issue{ID: 101, IID: 2, Title: "Task B"}},
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v4/groups/5/epics/3/issues" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(links)
	}))
	defer server.Close()

	client := NewClient("test-token", server.URL, "123")
	result, err := client.ListEpicIssues(context.Background(), "5", 3)
	if err != nil {
		t.Fatalf("ListEpicIssues failed: %v", err)
	}
	if len(result) != 2 {
		t.Fatalf("got %d issues, want 2", len(result))
	}
	if result[0].Title != "Task A" {
		t.Errorf("first issue title = %q, want %q", result[0].Title, "Task A")
	}
}

func TestAddIssueToEpic(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if r.URL.Path != "/api/v4/groups/5/epics/3/issues/100" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(EpicIssueLink{ID: 1})
	}))
	defer server.Close()

	client := NewClient("test-token", server.URL, "123")
	err := client.AddIssueToEpic(context.Background(), "5", 3, 100)
	if err != nil {
		t.Fatalf("AddIssueToEpic failed: %v", err)
	}
}

func TestListEpics_EncodesGroupPath(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// r.URL.RawPath preserves the percent-encoding; r.URL.Path is decoded.
		// url.PathEscape("my-org/sub-group") produces "my-org%2Fsub-group".
		wantRaw := "/api/v4/groups/my-org%2Fsub-group/epics"
		if r.URL.RawPath != wantRaw {
			t.Errorf("RawPath = %q, want %q", r.URL.RawPath, wantRaw)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode([]Epic{})
	}))
	defer server.Close()

	client := NewClient("test-token", server.URL, "123")
	_, err := client.ListEpics(context.Background(), "my-org/sub-group", "")
	if err != nil {
		t.Fatalf("ListEpics with path-based group failed: %v", err)
	}
}
