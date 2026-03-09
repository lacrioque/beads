package main

import (
	"testing"

	"github.com/steveyegge/beads/internal/gitlab"
	"github.com/steveyegge/beads/internal/types"
)

func TestIsMilestoneRef(t *testing.T) {
	tests := []struct {
		ref  string
		want bool
	}{
		{"https://gitlab.com/group/project/-/milestones/5", true},
		{"gitlab-milestone:123:5", true},
		{"https://gitlab.com/group/project/-/issues/42", false},
		{"https://gitlab.com/groups/group/-/epics/3", false},
		{"", false},
	}

	for _, tt := range tests {
		if got := isMilestoneRef(tt.ref); got != tt.want {
			t.Errorf("isMilestoneRef(%q) = %v, want %v", tt.ref, got, tt.want)
		}
	}
}

func TestIsEpicRef(t *testing.T) {
	tests := []struct {
		ref  string
		want bool
	}{
		{"https://gitlab.com/groups/group/-/epics/3", true},
		{"gitlab-epic:5:3", true},
		{"https://gitlab.com/group/project/-/milestones/5", false},
		{"https://gitlab.com/group/project/-/issues/42", false},
		{"", false},
	}

	for _, tt := range tests {
		if got := isEpicRef(tt.ref); got != tt.want {
			t.Errorf("isEpicRef(%q) = %v, want %v", tt.ref, got, tt.want)
		}
	}
}

func TestExtractMilestoneID(t *testing.T) {
	tests := []struct {
		ref  string
		want int
	}{
		{"https://gitlab.com/group/project/-/milestones/5", 5},
		{"https://gitlab.com/group/project/-/milestones/42", 42},
		{"gitlab-milestone:123:7", 7},
		{"https://gitlab.com/group/project/-/issues/42", 0},
		{"", 0},
	}

	for _, tt := range tests {
		if got := extractMilestoneID(tt.ref); got != tt.want {
			t.Errorf("extractMilestoneID(%q) = %d, want %d", tt.ref, got, tt.want)
		}
	}
}

func TestExtractEpicIID(t *testing.T) {
	tests := []struct {
		ref  string
		want int
	}{
		{"https://gitlab.com/groups/group/-/epics/3", 3},
		{"https://gitlab.com/groups/group/-/epics/42", 42},
		{"gitlab-epic:5:7", 7},
		{"https://gitlab.com/group/project/-/milestones/5", 0},
		{"", 0},
	}

	for _, tt := range tests {
		if got := extractEpicIID(tt.ref); got != tt.want {
			t.Errorf("extractEpicIID(%q) = %d, want %d", tt.ref, got, tt.want)
		}
	}
}

func TestExtractIssueIID(t *testing.T) {
	tests := []struct {
		ref  string
		want int
	}{
		{"https://gitlab.com/group/project/-/issues/42", 42},
		{"https://gitlab.com/group/project/-/issues/1", 1},
		{"https://gitlab.com/group/project/-/milestones/5", 0},
		{"", 0},
	}

	for _, tt := range tests {
		if got := extractIssueIID(tt.ref); got != tt.want {
			t.Errorf("extractIssueIID(%q) = %d, want %d", tt.ref, got, tt.want)
		}
	}
}

func TestMilestoneToEpic(t *testing.T) {
	ms := gitlab.Milestone{
		ID:        1,
		IID:       1,
		ProjectID: 123,
		Title:     "v1.0 Release",
		Description: "First major release",
		State:     "active",
	}

	issue := milestoneToEpic(ms, "test")

	if issue.Title != "v1.0 Release" {
		t.Errorf("Title = %q, want %q", issue.Title, "v1.0 Release")
	}
	if issue.Description != "First major release" {
		t.Errorf("Description = %q, want %q", issue.Description, "First major release")
	}
	if issue.IssueType != types.TypeEpic {
		t.Errorf("IssueType = %q, want %q", issue.IssueType, types.TypeEpic)
	}
	if issue.Status != types.StatusOpen {
		t.Errorf("Status = %q, want %q", issue.Status, types.StatusOpen)
	}
	if issue.SourceSystem != "gitlab-milestone:123:1" {
		t.Errorf("SourceSystem = %q, want %q", issue.SourceSystem, "gitlab-milestone:123:1")
	}
}

func TestMilestoneToEpic_Closed(t *testing.T) {
	ms := gitlab.Milestone{
		ID:    2,
		IID:   2,
		Title: "Closed Sprint",
		State: "closed",
	}

	issue := milestoneToEpic(ms, "test")

	if issue.Status != types.StatusClosed {
		t.Errorf("Status = %q, want %q", issue.Status, types.StatusClosed)
	}
}

func TestGitlabEpicToBeadsEpic(t *testing.T) {
	epic := gitlab.Epic{
		ID:          42,
		IID:         3,
		GroupID:     5,
		Title:       "Platform Refactor",
		Description: "Major refactoring effort",
		State:       "opened",
		Labels:      []string{"team::platform"},
	}

	issue := gitlabEpicToBeadsEpic(epic, "test")

	if issue.Title != "Platform Refactor" {
		t.Errorf("Title = %q, want %q", issue.Title, "Platform Refactor")
	}
	if issue.IssueType != types.TypeEpic {
		t.Errorf("IssueType = %q, want %q", issue.IssueType, types.TypeEpic)
	}
	if issue.Status != types.StatusOpen {
		t.Errorf("Status = %q, want %q", issue.Status, types.StatusOpen)
	}
	if issue.SourceSystem != "gitlab-epic:5:3" {
		t.Errorf("SourceSystem = %q, want %q", issue.SourceSystem, "gitlab-epic:5:3")
	}
	if len(issue.Labels) != 1 || issue.Labels[0] != "team::platform" {
		t.Errorf("Labels = %v, want [team::platform]", issue.Labels)
	}
}

func TestEpicToMilestoneFields(t *testing.T) {
	issue := &types.Issue{
		Title:       "v2.0",
		Description: "Second release",
		Status:      types.StatusClosed,
	}

	fields := epicToMilestoneFields(issue)

	if fields["title"] != "v2.0" {
		t.Errorf("title = %v, want v2.0", fields["title"])
	}
	if fields["state_event"] != "close" {
		t.Errorf("state_event = %v, want close", fields["state_event"])
	}
}

func TestEpicToMilestoneFields_Open(t *testing.T) {
	issue := &types.Issue{
		Title:  "v3.0",
		Status: types.StatusOpen,
	}

	fields := epicToMilestoneFields(issue)
	if fields["state_event"] != "activate" {
		t.Errorf("state_event = %v, want activate", fields["state_event"])
	}
}

func TestBeadsEpicToGitLabEpicFields(t *testing.T) {
	issue := &types.Issue{
		Title:       "Platform Refactor",
		Description: "Big effort",
		Labels:      []string{"team::platform"},
		Status:      types.StatusClosed,
	}

	fields := beadsEpicToGitLabEpicFields(issue)

	if fields["title"] != "Platform Refactor" {
		t.Errorf("title = %v, want 'Platform Refactor'", fields["title"])
	}
	if fields["state_event"] != "close" {
		t.Errorf("state_event = %v, want close", fields["state_event"])
	}
	labels, ok := fields["labels"].([]string)
	if !ok || len(labels) != 1 {
		t.Errorf("labels = %v, want [team::platform]", fields["labels"])
	}
}

func TestMilestoneExternalRef(t *testing.T) {
	ms := gitlab.Milestone{
		WebURL: "https://gitlab.com/group/project/-/milestones/5",
	}
	if got := milestoneExternalRef(ms); got != "https://gitlab.com/group/project/-/milestones/5" {
		t.Errorf("milestoneExternalRef with WebURL = %q", got)
	}

	ms2 := gitlab.Milestone{ProjectID: 123, IID: 7}
	if got := milestoneExternalRef(ms2); got != "gitlab-milestone:123:7" {
		t.Errorf("milestoneExternalRef without WebURL = %q, want gitlab-milestone:123:7", got)
	}
}

func TestEpicExternalRef(t *testing.T) {
	epic := gitlab.Epic{
		WebURL: "https://gitlab.com/groups/group/-/epics/3",
	}
	if got := epicExternalRef(epic); got != "https://gitlab.com/groups/group/-/epics/3" {
		t.Errorf("epicExternalRef with WebURL = %q", got)
	}

	epic2 := gitlab.Epic{GroupID: 5, IID: 7}
	if got := epicExternalRef(epic2); got != "gitlab-epic:5:7" {
		t.Errorf("epicExternalRef without WebURL = %q, want gitlab-epic:5:7", got)
	}
}
