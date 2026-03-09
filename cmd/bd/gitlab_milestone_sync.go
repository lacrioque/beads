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

// milestoneSyncOptions holds options for milestone synchronization.
type milestoneSyncOptions struct {
	DryRun   bool
	PullOnly bool
	PushOnly bool
}

// syncGitLabMilestones synchronizes GitLab milestones with beads epics.
// Milestones are mapped to beads issues with type=epic.
// Issue-milestone assignments become parent-child dependencies.
func syncGitLabMilestones(ctx context.Context, client *gitlab.Client, st storage.Storage, act string, out io.Writer, opts milestoneSyncOptions) error {
	pull := !opts.PushOnly
	push := !opts.PullOnly

	if pull {
		if err := pullMilestones(ctx, client, st, act, out, opts.DryRun); err != nil {
			return fmt.Errorf("milestone pull: %w", err)
		}
	}

	if push {
		if err := pushMilestones(ctx, client, st, act, out, opts.DryRun); err != nil {
			return fmt.Errorf("milestone push: %w", err)
		}
	}

	return nil
}

// pullMilestones imports GitLab milestones as beads epics.
func pullMilestones(ctx context.Context, client *gitlab.Client, st storage.Storage, act string, out io.Writer, dryRun bool) error {
	milestones, err := client.ListMilestones(ctx, "")
	if err != nil {
		return err
	}

	_, _ = fmt.Fprintf(out, "  Fetched %d milestones from GitLab\n", len(milestones))

	prefix := "bd"
	if p, err := st.GetConfig(ctx, "issue_prefix"); err == nil && p != "" {
		prefix = p
	}

	created, updated, skipped := 0, 0, 0

	for _, ms := range milestones {
		ref := milestoneExternalRef(ms)

		if dryRun {
			existing, _ := st.GetIssueByExternalRef(ctx, ref)
			if existing != nil {
				_, _ = fmt.Fprintf(out, "  [dry-run] Would update milestone: %s\n", ms.Title)
				updated++
			} else {
				_, _ = fmt.Fprintf(out, "  [dry-run] Would import milestone: %s\n", ms.Title)
				created++
			}
			continue
		}

		existing, _ := st.GetIssueByExternalRef(ctx, ref)
		if existing != nil {
			// Update existing epic from milestone
			updates := milestoneToUpdates(ms)
			if err := st.UpdateIssue(ctx, existing.ID, updates, act); err != nil {
				_, _ = fmt.Fprintf(out, "  Warning: failed to update milestone %q: %v\n", ms.Title, err)
				continue
			}
			updated++

			// Sync issue-milestone assignments as parent-child deps
			if err := syncMilestoneChildren(ctx, client, st, act, ms, existing.ID); err != nil {
				_, _ = fmt.Fprintf(out, "  Warning: failed to sync milestone children for %q: %v\n", ms.Title, err)
			}
		} else {
			// Create new epic from milestone
			issue := milestoneToEpic(ms, prefix)
			issue.ExternalRef = strPtrLocal(ref)

			if err := st.CreateIssue(ctx, issue, act); err != nil {
				_, _ = fmt.Fprintf(out, "  Warning: failed to create epic for milestone %q: %v\n", ms.Title, err)
				continue
			}
			created++

			// Sync issue-milestone assignments as parent-child deps
			if err := syncMilestoneChildren(ctx, client, st, act, ms, issue.ID); err != nil {
				_, _ = fmt.Fprintf(out, "  Warning: failed to sync milestone children for %q: %v\n", ms.Title, err)
			}
		}
	}

	if !dryRun {
		if created > 0 || updated > 0 {
			_, _ = fmt.Fprintf(out, "  ✓ Milestones: %d created, %d updated, %d skipped\n", created, updated, skipped)
		}
	}
	return nil
}

// pushMilestones exports beads epics as GitLab milestones.
// Only pushes epics that don't already have a gitlab-epic external ref
// (to avoid pushing GitLab-epic-backed epics as milestones).
func pushMilestones(ctx context.Context, client *gitlab.Client, st storage.Storage, act string, out io.Writer, dryRun bool) error {
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

		// Skip epics backed by GitLab epics (not milestones)
		if strings.Contains(extRef, "/-/epics/") {
			skipped++
			continue
		}

		if dryRun {
			if isMilestoneRef(extRef) {
				_, _ = fmt.Fprintf(out, "  [dry-run] Would update milestone: %s\n", issue.Title)
				updated++
			} else if extRef == "" {
				_, _ = fmt.Fprintf(out, "  [dry-run] Would create milestone: %s\n", issue.Title)
				created++
			} else {
				skipped++
			}
			continue
		}

		if isMilestoneRef(extRef) {
			// Update existing milestone
			msID := extractMilestoneID(extRef)
			if msID == 0 {
				skipped++
				continue
			}

			updates := epicToMilestoneFields(issue)
			if _, err := client.UpdateMilestone(ctx, msID, updates); err != nil {
				_, _ = fmt.Fprintf(out, "  Warning: failed to update milestone for %s: %v\n", issue.ID, err)
				continue
			}
			updated++

			// Link child issues to milestone
			if err := pushMilestoneChildren(ctx, client, st, issue.ID, msID); err != nil {
				_, _ = fmt.Fprintf(out, "  Warning: failed to link children for %s: %v\n", issue.ID, err)
			}
		} else if extRef == "" {
			// Create new milestone
			ms, err := client.CreateMilestone(ctx, issue.Title, issue.Description, "", "")
			if err != nil {
				_, _ = fmt.Fprintf(out, "  Warning: failed to create milestone for %s: %v\n", issue.ID, err)
				continue
			}

			// Store external ref
			ref := milestoneExternalRef(*ms)
			updateMap := map[string]any{"external_ref": ref}
			if err := st.UpdateIssue(ctx, issue.ID, updateMap, act); err != nil {
				_, _ = fmt.Fprintf(out, "  Warning: failed to update external_ref for %s: %v\n", issue.ID, err)
			}
			created++

			// Link child issues to milestone
			if err := pushMilestoneChildren(ctx, client, st, issue.ID, ms.ID); err != nil {
				_, _ = fmt.Fprintf(out, "  Warning: failed to link children for %s: %v\n", issue.ID, err)
			}
		} else {
			skipped++
		}
	}

	if !dryRun && (created > 0 || updated > 0) {
		_, _ = fmt.Fprintf(out, "  ✓ Milestones pushed: %d created, %d updated, %d skipped\n", created, updated, skipped)
	}
	return nil
}

// syncMilestoneChildren creates parent-child dependencies between a beads epic
// and issues that are assigned to the corresponding GitLab milestone.
func syncMilestoneChildren(ctx context.Context, client *gitlab.Client, st storage.Storage, act string, ms gitlab.Milestone, epicID string) error {
	issues, err := client.ListMilestoneIssues(ctx, ms.ID)
	if err != nil {
		return err
	}

	for _, glIssue := range issues {
		if glIssue.WebURL == "" {
			continue
		}

		// Find the corresponding beads issue by external ref
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

// pushMilestoneChildren assigns beads child issues to the GitLab milestone.
func pushMilestoneChildren(ctx context.Context, client *gitlab.Client, st storage.Storage, epicID string, milestoneID int) error {
	// Get children of this epic (issues that depend on this epic)
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

		// Extract GitLab issue IID from external ref
		iid := extractIssueIID(extRef)
		if iid == 0 {
			continue
		}

		// Set the milestone on the GitLab issue
		updates := map[string]any{
			"milestone_id": milestoneID,
		}
		if _, err := client.UpdateIssue(ctx, iid, updates); err != nil {
			continue // Non-fatal
		}
	}

	return nil
}

// milestoneToEpic converts a GitLab Milestone to a beads Issue with type=epic.
func milestoneToEpic(ms gitlab.Milestone, prefix string) *types.Issue {
	issue := &types.Issue{
		ID:           generateIssueID(prefix),
		Title:        ms.Title,
		Description:  ms.Description,
		IssueType:    types.TypeEpic,
		Priority:     2, // Default medium
		SourceSystem: fmt.Sprintf("gitlab-milestone:%d:%d", ms.ProjectID, ms.IID),
	}

	if ms.State == "closed" {
		issue.Status = types.StatusClosed
	} else {
		issue.Status = types.StatusOpen
	}

	if ms.CreatedAt != nil {
		issue.CreatedAt = *ms.CreatedAt
	}
	if ms.UpdatedAt != nil {
		issue.UpdatedAt = *ms.UpdatedAt
	}

	return issue
}

// milestoneToUpdates creates an update map from a GitLab Milestone.
func milestoneToUpdates(ms gitlab.Milestone) map[string]any {
	updates := map[string]any{
		"title":       ms.Title,
		"description": ms.Description,
	}
	if ms.State == "closed" {
		updates["status"] = "closed"
	} else {
		updates["status"] = "open"
	}
	return updates
}

// epicToMilestoneFields converts a beads epic to GitLab milestone update fields.
func epicToMilestoneFields(issue *types.Issue) map[string]any {
	fields := map[string]any{
		"title":       issue.Title,
		"description": issue.Description,
	}
	if issue.Status == types.StatusClosed {
		fields["state_event"] = "close"
	} else {
		fields["state_event"] = "activate"
	}
	return fields
}

// milestoneExternalRef builds the external ref string for a milestone.
// Uses the web URL if available, falls back to a constructed ref.
func milestoneExternalRef(ms gitlab.Milestone) string {
	if ms.WebURL != "" {
		return ms.WebURL
	}
	return fmt.Sprintf("gitlab-milestone:%d:%d", ms.ProjectID, ms.IID)
}

// isMilestoneRef checks if an external ref points to a GitLab milestone.
func isMilestoneRef(ref string) bool {
	return strings.Contains(ref, "/-/milestones/") || strings.HasPrefix(ref, "gitlab-milestone:")
}

// extractMilestoneID extracts the milestone ID from an external ref.
// Handles both web URL format (.../milestones/42) and source system format (gitlab-milestone:pid:iid).
func extractMilestoneID(ref string) int {
	// Try web URL format: .../milestones/42
	if idx := strings.LastIndex(ref, "/-/milestones/"); idx >= 0 {
		idStr := ref[idx+len("/-/milestones/"):]
		if id, err := strconv.Atoi(idStr); err == nil {
			return id
		}
	}

	// Try source system format: gitlab-milestone:pid:iid
	if strings.HasPrefix(ref, "gitlab-milestone:") {
		parts := strings.Split(ref, ":")
		if len(parts) >= 3 {
			if id, err := strconv.Atoi(parts[2]); err == nil {
				return id
			}
		}
	}

	return 0
}

// extractIssueIID extracts the issue IID from a GitLab issue web URL.
func extractIssueIID(ref string) int {
	if idx := strings.LastIndex(ref, "/issues/"); idx >= 0 {
		idStr := ref[idx+len("/issues/"):]
		if id, err := strconv.Atoi(idStr); err == nil {
			return id
		}
	}
	return 0
}

// derefStrLocal safely dereferences a *string, returning "" for nil.
func derefStrLocal(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// strPtrLocal returns a pointer to the given string.
func strPtrLocal(s string) *string { return &s }
