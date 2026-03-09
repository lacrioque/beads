package main

import (
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/steveyegge/beads/internal/gitlab"
	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/types"
)

// epicSyncOptions holds options for GitLab epic synchronization.
type epicSyncOptions struct {
	DryRun   bool
	PullOnly bool
	PushOnly bool
	GroupID  string // GitLab group ID (required for epics)
}

// syncGitLabEpics synchronizes GitLab group epics with beads epics.
// GitLab epics are group-level objects (requires Premium/Ultimate).
// They map to beads issues with type=epic, with epic-issue links
// becoming parent-child dependencies.
func syncGitLabEpics(ctx context.Context, client *gitlab.Client, st storage.Storage, act string, out io.Writer, opts epicSyncOptions) error {
	if opts.GroupID == "" {
		return fmt.Errorf("gitlab.group_id is required for epic sync. Set via 'bd config gitlab.group_id <id>' or GITLAB_GROUP_ID")
	}

	pull := !opts.PushOnly
	push := !opts.PullOnly

	if pull {
		if err := pullEpics(ctx, client, st, act, out, opts); err != nil {
			return fmt.Errorf("epic pull: %w", err)
		}
	}

	if push {
		if err := pushEpics(ctx, client, st, act, out, opts); err != nil {
			return fmt.Errorf("epic push: %w", err)
		}
	}

	return nil
}

// pullEpics imports GitLab group epics as beads epics.
func pullEpics(ctx context.Context, client *gitlab.Client, st storage.Storage, act string, out io.Writer, opts epicSyncOptions) error {
	epics, err := client.ListEpics(ctx, opts.GroupID, "")
	if err != nil {
		return err
	}

	_, _ = fmt.Fprintf(out, "  Fetched %d epics from GitLab\n", len(epics))

	prefix := "bd"
	if p, err := st.GetConfig(ctx, "issue_prefix"); err == nil && p != "" {
		prefix = p
	}

	created, updated := 0, 0

	for _, epic := range epics {
		ref := epicExternalRef(epic)

		if opts.DryRun {
			existing, _ := st.GetIssueByExternalRef(ctx, ref)
			if existing != nil {
				_, _ = fmt.Fprintf(out, "  [dry-run] Would update epic: %s\n", epic.Title)
				updated++
			} else {
				_, _ = fmt.Fprintf(out, "  [dry-run] Would import epic: %s\n", epic.Title)
				created++
			}
			continue
		}

		existing, _ := st.GetIssueByExternalRef(ctx, ref)
		if existing != nil {
			updates := gitlabEpicToUpdates(epic)
			if err := st.UpdateIssue(ctx, existing.ID, updates, act); err != nil {
				_, _ = fmt.Fprintf(out, "  Warning: failed to update epic %q: %v\n", epic.Title, err)
				continue
			}
			updated++

			// Sync epic-issue links as parent-child deps
			if err := syncEpicChildren(ctx, client, st, act, epic, existing.ID, opts.GroupID); err != nil {
				_, _ = fmt.Fprintf(out, "  Warning: failed to sync epic children for %q: %v\n", epic.Title, err)
			}
		} else {
			issue := gitlabEpicToBeadsEpic(epic, prefix)
			issue.ExternalRef = strPtrLocal(ref)

			if err := st.CreateIssue(ctx, issue, act); err != nil {
				_, _ = fmt.Fprintf(out, "  Warning: failed to create epic for %q: %v\n", epic.Title, err)
				continue
			}
			created++

			// Sync epic-issue links as parent-child deps
			if err := syncEpicChildren(ctx, client, st, act, epic, issue.ID, opts.GroupID); err != nil {
				_, _ = fmt.Fprintf(out, "  Warning: failed to sync epic children for %q: %v\n", epic.Title, err)
			}
		}
	}

	if !opts.DryRun && (created > 0 || updated > 0) {
		_, _ = fmt.Fprintf(out, "  ✓ Epics: %d created, %d updated\n", created, updated)
	}
	return nil
}

// pushEpics exports beads epics as GitLab group epics.
// Only pushes epics that don't already have a milestone external ref.
func pushEpics(ctx context.Context, client *gitlab.Client, st storage.Storage, act string, out io.Writer, opts epicSyncOptions) error {
	filter := types.IssueFilter{}
	issues, err := st.SearchIssues(ctx, "", filter)
	if err != nil {
		return fmt.Errorf("searching epics: %w", err)
	}

	created, updated, skipped := 0, 0, 0

	for _, issue := range issues {
		if issue.IssueType != types.TypeEpic {
			continue
		}

		extRef := derefStrLocal(issue.ExternalRef)

		// Skip epics backed by milestones
		if isMilestoneRef(extRef) {
			skipped++
			continue
		}

		if opts.DryRun {
			if isEpicRef(extRef) {
				_, _ = fmt.Fprintf(out, "  [dry-run] Would update epic: %s\n", issue.Title)
				updated++
			} else if extRef == "" {
				_, _ = fmt.Fprintf(out, "  [dry-run] Would create epic: %s\n", issue.Title)
				created++
			} else {
				skipped++
			}
			continue
		}

		if isEpicRef(extRef) {
			// Update existing GitLab epic
			epicIID := extractEpicIID(extRef)
			if epicIID == 0 {
				skipped++
				continue
			}

			updates := beadsEpicToGitLabEpicFields(issue)
			if _, err := client.UpdateEpic(ctx, opts.GroupID, epicIID, updates); err != nil {
				_, _ = fmt.Fprintf(out, "  Warning: failed to update epic for %s: %v\n", issue.ID, err)
				continue
			}
			updated++

			// Link child issues to epic
			if err := pushEpicChildren(ctx, client, st, issue.ID, opts.GroupID, epicIID); err != nil {
				_, _ = fmt.Fprintf(out, "  Warning: failed to link children for %s: %v\n", issue.ID, err)
			}
		} else if extRef == "" {
			// Create new GitLab epic
			epic, err := client.CreateEpic(ctx, opts.GroupID, issue.Title, issue.Description, issue.Labels)
			if err != nil {
				_, _ = fmt.Fprintf(out, "  Warning: failed to create epic for %s: %v\n", issue.ID, err)
				continue
			}

			ref := epicExternalRef(*epic)
			updateMap := map[string]any{"external_ref": ref}
			if err := st.UpdateIssue(ctx, issue.ID, updateMap, act); err != nil {
				_, _ = fmt.Fprintf(out, "  Warning: failed to update external_ref for %s: %v\n", issue.ID, err)
			}
			created++

			// Link child issues to epic
			if err := pushEpicChildren(ctx, client, st, issue.ID, opts.GroupID, epic.IID); err != nil {
				_, _ = fmt.Fprintf(out, "  Warning: failed to link children for %s: %v\n", issue.ID, err)
			}
		} else {
			skipped++
		}
	}

	if !opts.DryRun && (created > 0 || updated > 0) {
		_, _ = fmt.Fprintf(out, "  ✓ Epics pushed: %d created, %d updated, %d skipped\n", created, updated, skipped)
	}
	return nil
}

// syncEpicChildren creates parent-child dependencies between a beads epic
// and issues linked to the corresponding GitLab epic.
func syncEpicChildren(ctx context.Context, client *gitlab.Client, st storage.Storage, act string, epic gitlab.Epic, epicID, groupID string) error {
	issues, err := client.ListEpicIssues(ctx, groupID, epic.IID)
	if err != nil {
		return err
	}

	for _, glIssue := range issues {
		if glIssue.WebURL == "" {
			continue
		}

		child, _ := st.GetIssueByExternalRef(ctx, glIssue.WebURL)
		if child == nil {
			continue
		}

		// Check if dependency already exists
		depsWithMeta, _ := st.GetDependenciesWithMetadata(ctx, child.ID)
		alreadyLinked := false
		for _, d := range depsWithMeta {
			if d.ID == epicID && d.DependencyType == types.DepParentChild {
				alreadyLinked = true
				break
			}
		}
		if alreadyLinked {
			continue
		}

		dep := &types.Dependency{
			IssueID:     child.ID,
			DependsOnID: epicID,
			Type:        types.DepParentChild,
		}
		_ = st.AddDependency(ctx, dep, act) // Non-fatal
	}

	return nil
}

// pushEpicChildren links beads child issues to the GitLab epic.
func pushEpicChildren(ctx context.Context, client *gitlab.Client, st storage.Storage, epicID, groupID string, epicIID int) error {
	children, err := st.GetDependentsWithMetadata(ctx, epicID)
	if err != nil {
		return err
	}

	for _, child := range children {
		if child.DependencyType != types.DepParentChild {
			continue
		}

		extRef := derefStrLocal(child.ExternalRef)
		if extRef == "" || !strings.Contains(extRef, "/issues/") {
			continue
		}

		// We need the global issue ID (not IID) for the epic-issue link API.
		iid := extractIssueIID(extRef)
		if iid == 0 {
			continue
		}

		glIssue, err := client.FetchIssueByIID(ctx, iid)
		if err != nil || glIssue == nil {
			continue
		}

		_ = client.AddIssueToEpic(ctx, groupID, epicIID, glIssue.ID) // Non-fatal
	}

	return nil
}

// gitlabEpicToBeadsEpic converts a GitLab Epic to a beads Issue with type=epic.
func gitlabEpicToBeadsEpic(epic gitlab.Epic, prefix string) *types.Issue {
	issue := &types.Issue{
		ID:           generateIssueID(prefix),
		Title:        epic.Title,
		Description:  epic.Description,
		IssueType:    types.TypeEpic,
		Priority:     2, // Default medium
		Labels:       epic.Labels,
		SourceSystem: fmt.Sprintf("gitlab-epic:%d:%d", epic.GroupID, epic.IID),
	}

	if epic.State == "closed" {
		issue.Status = types.StatusClosed
	} else {
		issue.Status = types.StatusOpen
	}

	if epic.CreatedAt != nil {
		issue.CreatedAt = *epic.CreatedAt
	}
	if epic.UpdatedAt != nil {
		issue.UpdatedAt = *epic.UpdatedAt
	}

	return issue
}

// gitlabEpicToUpdates creates an update map from a GitLab Epic.
func gitlabEpicToUpdates(epic gitlab.Epic) map[string]any {
	updates := map[string]any{
		"title":       epic.Title,
		"description": epic.Description,
	}
	if epic.State == "closed" {
		updates["status"] = "closed"
	} else {
		updates["status"] = "open"
	}
	return updates
}

// beadsEpicToGitLabEpicFields converts a beads epic to GitLab epic update fields.
func beadsEpicToGitLabEpicFields(issue *types.Issue) map[string]any {
	fields := map[string]any{
		"title":       issue.Title,
		"description": issue.Description,
	}
	if len(issue.Labels) > 0 {
		fields["labels"] = issue.Labels
	}
	if issue.Status == types.StatusClosed {
		fields["state_event"] = "close"
	}
	return fields
}

// epicExternalRef builds the external ref string for a GitLab epic.
func epicExternalRef(epic gitlab.Epic) string {
	if epic.WebURL != "" {
		return epic.WebURL
	}
	return fmt.Sprintf("gitlab-epic:%d:%d", epic.GroupID, epic.IID)
}

// isEpicRef checks if an external ref points to a GitLab epic.
func isEpicRef(ref string) bool {
	return strings.Contains(ref, "/-/epics/") || strings.HasPrefix(ref, "gitlab-epic:")
}

// extractEpicIID extracts the epic IID from an external ref.
func extractEpicIID(ref string) int {
	// Try web URL format: .../epics/42
	if idx := strings.LastIndex(ref, "/-/epics/"); idx >= 0 {
		idStr := ref[idx+len("/-/epics/"):]
		if id, err := strconv.Atoi(idStr); err == nil {
			return id
		}
	}

	// Try source system format: gitlab-epic:gid:iid
	if strings.HasPrefix(ref, "gitlab-epic:") {
		parts := strings.Split(ref, ":")
		if len(parts) >= 3 {
			if id, err := strconv.Atoi(parts[2]); err == nil {
				return id
			}
		}
	}

	return 0
}
