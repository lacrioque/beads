package gitlab

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/steveyegge/beads/internal/tracker"
	"github.com/steveyegge/beads/internal/types"
)

// newTestServer creates a test HTTP server with a handler that dispatches
// based on method and path. The handler func receives the parsed JSON body
// and returns a status code and response object to marshal.
func newTestServer(t *testing.T, handler func(method, path string, body map[string]interface{}) (int, interface{})) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]interface{}
		if r.Body != nil {
			data, _ := io.ReadAll(r.Body)
			if len(data) > 0 {
				_ = json.Unmarshal(data, &body)
			}
		}
		status, resp := handler(r.Method, r.URL.Path, body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		if resp != nil {
			_ = json.NewEncoder(w).Encode(resp)
		}
	}))
}

func TestRegistered(t *testing.T) {
	factory := tracker.Get("gitlab")
	if factory == nil {
		t.Fatal("gitlab tracker not registered")
	}
	tr := factory()
	if tr.Name() != "gitlab" {
		t.Errorf("Name() = %q, want %q", tr.Name(), "gitlab")
	}
	if tr.DisplayName() != "GitLab" {
		t.Errorf("DisplayName() = %q, want %q", tr.DisplayName(), "GitLab")
	}
	if tr.ConfigPrefix() != "gitlab" {
		t.Errorf("ConfigPrefix() = %q, want %q", tr.ConfigPrefix(), "gitlab")
	}
}

func TestIsExternalRef(t *testing.T) {
	tr := &Tracker{}
	tests := []struct {
		ref  string
		want bool
	}{
		{"https://gitlab.com/group/project/-/issues/42", true},
		{"https://my-gitlab.example.com/team/repo/-/issues/123", true},
		{"https://linear.app/team/issue/PROJ-123", false},
		{"https://github.com/org/repo/issues/1", false},
		{"", false},
	}
	for _, tt := range tests {
		if got := tr.IsExternalRef(tt.ref); got != tt.want {
			t.Errorf("IsExternalRef(%q) = %v, want %v", tt.ref, got, tt.want)
		}
	}
}

func TestExtractIdentifier(t *testing.T) {
	tr := &Tracker{}
	tests := []struct {
		ref  string
		want string
	}{
		{"https://gitlab.com/group/project/-/issues/42", "42"},
		{"https://gitlab.example.com/team/repo/-/issues/123", "123"},
		{"not-a-url", ""},
	}
	for _, tt := range tests {
		if got := tr.ExtractIdentifier(tt.ref); got != tt.want {
			t.Errorf("ExtractIdentifier(%q) = %q, want %q", tt.ref, got, tt.want)
		}
	}
}

func TestBuildExternalRef(t *testing.T) {
	tr := &Tracker{}
	ti := &tracker.TrackerIssue{
		URL:        "https://gitlab.com/group/project/-/issues/42",
		Identifier: "42",
	}
	ref := tr.BuildExternalRef(ti)
	if ref != ti.URL {
		t.Errorf("BuildExternalRef() = %q, want %q", ref, ti.URL)
	}
}

func TestFieldMapperStatus(t *testing.T) {
	m := &gitlabFieldMapper{config: DefaultMappingConfig()}

	if got := m.StatusToBeads("opened"); got != types.StatusOpen {
		t.Errorf("StatusToBeads(opened) = %q, want %q", got, types.StatusOpen)
	}
	if got := m.StatusToBeads("closed"); got != types.StatusClosed {
		t.Errorf("StatusToBeads(closed) = %q, want %q", got, types.StatusClosed)
	}
	if got := m.StatusToBeads("reopened"); got != types.StatusOpen {
		t.Errorf("StatusToBeads(reopened) = %q, want %q", got, types.StatusOpen)
	}
}

func TestFieldMapperPriority(t *testing.T) {
	m := &gitlabFieldMapper{config: DefaultMappingConfig()}

	if got := m.PriorityToBeads("critical"); got != 0 {
		t.Errorf("PriorityToBeads(critical) = %d, want 0", got)
	}
	if got := m.PriorityToBeads("high"); got != 1 {
		t.Errorf("PriorityToBeads(high) = %d, want 1", got)
	}
	if got := m.PriorityToBeads("low"); got != 3 {
		t.Errorf("PriorityToBeads(low) = %d, want 3", got)
	}
}

func TestCreateIssueClosesIfLocalClosed(t *testing.T) {
	// Verify that CreateIssue calls UpdateIssue with state_event:"close"
	// when the local issue is closed. We test this via a mock HTTP server.
	now := time.Now()
	createCalled := false
	updateCalled := false
	var updateBody map[string]interface{}

	ts := newTestServer(t, func(method, path string, body map[string]interface{}) (int, interface{}) {
		if method == "POST" && strings.Contains(path, "/issues") && !strings.Contains(path, "/links") {
			createCalled = true
			return 201, &Issue{
				ID:        1,
				IID:       42,
				Title:     "Test",
				State:     "opened",
				WebURL:    "https://gitlab.com/test/-/issues/42",
				CreatedAt: &now,
				UpdatedAt: &now,
			}
		}
		if method == "PUT" && strings.Contains(path, "/issues/42") {
			updateCalled = true
			updateBody = body
			closed := Issue{
				ID:        1,
				IID:       42,
				Title:     "Test",
				State:     "closed",
				WebURL:    "https://gitlab.com/test/-/issues/42",
				CreatedAt: &now,
				UpdatedAt: &now,
				ClosedAt:  &now,
			}
			return 200, &closed
		}
		return 404, map[string]string{"error": "not found"}
	})
	defer ts.Close()

	tr := &Tracker{
		client: NewClient("test-token", ts.URL, "123"),
		config: DefaultMappingConfig(),
	}

	issue := &types.Issue{
		ID:        "bd-closed1",
		Title:     "Test",
		Status:    types.StatusClosed,
		IssueType: types.TypeTask,
		Priority:  2,
	}

	result, err := tr.CreateIssue(context.Background(), issue)
	if err != nil {
		t.Fatalf("CreateIssue() error: %v", err)
	}
	if !createCalled {
		t.Error("expected POST /issues to be called")
	}
	if !updateCalled {
		t.Error("expected PUT /issues/42 to be called for state_event:close")
	}
	if updateBody != nil {
		if se, ok := updateBody["state_event"]; !ok || se != "close" {
			t.Errorf("state_event = %v, want %q", se, "close")
		}
	}
	if result == nil {
		t.Fatal("CreateIssue returned nil")
	}
}

func TestCreateIssueDoesNotCloseIfOpen(t *testing.T) {
	now := time.Now()
	updateCalled := false

	ts := newTestServer(t, func(method, path string, body map[string]interface{}) (int, interface{}) {
		if method == "POST" && strings.Contains(path, "/issues") {
			return 201, &Issue{
				ID:        1,
				IID:       42,
				Title:     "Test",
				State:     "opened",
				WebURL:    "https://gitlab.com/test/-/issues/42",
				CreatedAt: &now,
				UpdatedAt: &now,
			}
		}
		if method == "PUT" {
			updateCalled = true
			return 200, &Issue{}
		}
		return 404, map[string]string{"error": "not found"}
	})
	defer ts.Close()

	tr := &Tracker{
		client: NewClient("test-token", ts.URL, "123"),
		config: DefaultMappingConfig(),
	}

	issue := &types.Issue{
		ID:        "bd-open1",
		Title:     "Test",
		Status:    types.StatusOpen,
		IssueType: types.TypeTask,
		Priority:  2,
	}

	_, err := tr.CreateIssue(context.Background(), issue)
	if err != nil {
		t.Fatalf("CreateIssue() error: %v", err)
	}
	if updateCalled {
		t.Error("UpdateIssue should NOT be called for open issues")
	}
}

func TestGitLabToTrackerIssue(t *testing.T) {
	now := time.Now()
	gl := &Issue{
		ID:          100,
		IID:         42,
		Title:       "Fix pipeline",
		Description: "CI is broken",
		State:       "opened",
		WebURL:      "https://gitlab.com/group/project/-/issues/42",
		Labels:      []string{"bug", "priority::high"},
		CreatedAt:   &now,
		UpdatedAt:   &now,
		Assignee:    &User{ID: 5, Username: "bob"},
	}

	ti := gitlabToTrackerIssue(gl)

	if ti.ID != "100" {
		t.Errorf("ID = %q, want %q", ti.ID, "100")
	}
	if ti.Identifier != "42" {
		t.Errorf("Identifier = %q, want %q", ti.Identifier, "42")
	}
	if ti.Assignee != "bob" {
		t.Errorf("Assignee = %q, want %q", ti.Assignee, "bob")
	}
	if ti.AssigneeID != strconv.Itoa(5) {
		t.Errorf("AssigneeID = %q, want %q", ti.AssigneeID, "5")
	}
	if ti.Raw != gl {
		t.Error("Raw should reference original gitlab.Issue")
	}
	if len(ti.Labels) != 2 {
		t.Errorf("Labels count = %d, want 2", len(ti.Labels))
	}
}
