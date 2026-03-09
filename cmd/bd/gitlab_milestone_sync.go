package main

import (
	"context"
	"encoding/json"
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
	total := len(milestones)

	for i, ms := range milestones {
		_, _ = fmt.Fprintf(out, "\r  Pulling milestones %d/%d...", i+1, total)
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

	_, _ = fmt.Fprint(out, "\n")
	if !dryRun {
		_, _ = fmt.Fprintf(out, "  ✓ Milestones: %d created, %d updated, %d skipped\n", created, updated, skipped)
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

	// Count epic-type issues first for diagnostics.
	var epics []*types.Issue
	for _, issue := range issues {
		if issue.IssueType == types.TypeEpic {
			epics = append(epics, issue)
		}
	}

	if len(epics) == 0 {
		_, _ = fmt.Fprintf(out, "  No epic-type issues found to push as milestones\n")
		return nil
	}
	_, _ = fmt.Fprintf(out, "  Found %d epic-type issues\n", len(epics))

	created, updated, skipped := 0, 0, 0
	total := len(epics)

	for i, issue := range epics {
		_, _ = fmt.Fprintf(out, "\r  Pushing milestones %d/%d...", i+1, total)

		extRef := derefStrLocal(issue.ExternalRef)

		// Skip epics backed by GitLab epics (not milestones)
		if isEpicRef(extRef) {
			skipped++
			continue
		}

		// Determine milestone ref: check ExternalRef first, then metadata
		// (metadata stores the milestone ref for epics with non-GitLab ExternalRef)
		msRef := ""
		if isMilestoneRef(extRef) {
			msRef = extRef
		} else if ref := extractMetadataMilestoneRef(issue); ref != "" {
			msRef = ref
		}

		if dryRun {
			if msRef != "" {
				_, _ = fmt.Fprintf(out, "\n  [dry-run] Would update milestone: %s\n", issue.Title)
				updated++
			} else {
				_, _ = fmt.Fprintf(out, "\n  [dry-run] Would create milestone: %s\n", issue.Title)
				created++
			}
			continue
		}

		if msRef != "" {
			// Update existing milestone
			msID := extractMilestoneID(msRef)
			if msID == 0 {
				skipped++
				continue
			}

			updates := epicToMilestoneFields(issue)
			if _, err := client.UpdateMilestone(ctx, msID, updates); err != nil {
				_, _ = fmt.Fprintf(out, "\n  Warning: failed to update milestone for %s: %v\n", issue.ID, err)
				continue
			}
			updated++

			// Link child issues to milestone
			if err := pushMilestoneChildren(ctx, client, st, issue.ID, msID); err != nil {
				_, _ = fmt.Fprintf(out, "\n  Warning: failed to link children for %s: %v\n", issue.ID, err)
			}
		} else {
			// Create new milestone — if it already exists (title conflict),
			// find the existing one by title and adopt it instead.
			ms, err := client.CreateMilestone(ctx, issue.Title, issue.Description, "", "")
			if err != nil {
				// Title already taken — search for the existing milestone
				existing, searchErr := client.SearchMilestoneByTitle(ctx, issue.Title)
				if searchErr != nil || existing == nil {
					_, _ = fmt.Fprintf(out, "\n  Warning: failed to create milestone for %s: %v\n", issue.ID, err)
					continue
				}
				ms = existing
			}

			ref := milestoneExternalRef(*ms)
			if extRef == "" {
				// No existing ref — use milestone URL as the ExternalRef
				updateMap := map[string]any{"external_ref": ref}
				if err := st.UpdateIssue(ctx, issue.ID, updateMap, act); err != nil {
					_, _ = fmt.Fprintf(out, "\n  Warning: failed to update external_ref for %s: %v\n", issue.ID, err)
				}
			} else {
				// Has a non-GitLab ref (e.g., GitHub) — store milestone mapping in metadata
				md := makeMilestoneMetadata(issue, ref)
				updateMap := map[string]any{"metadata": md}
				if err := st.UpdateIssue(ctx, issue.ID, updateMap, act); err != nil {
					_, _ = fmt.Fprintf(out, "\n  Warning: failed to store milestone ref for %s: %v\n", issue.ID, err)
				}
			}
			created++

			// Link child issues to milestone
			if err := pushMilestoneChildren(ctx, client, st, issue.ID, ms.ID); err != nil {
				_, _ = fmt.Fprintf(out, "\n  Warning: failed to link children for %s: %v\n", issue.ID, err)
			}
		}
	}

	_, _ = fmt.Fprint(out, "\n")
	if !dryRun {
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

// extractMetadataMilestoneRef extracts a milestone ref stored in issue metadata.
func extractMetadataMilestoneRef(issue *types.Issue) string {
	if len(issue.Metadata) == 0 {
		return ""
	}
	var md map[string]interface{}
	if err := json.Unmarshal(issue.Metadata, &md); err != nil {
		return ""
	}
	ref, _ := md["gitlab_milestone_ref"].(string)
	if isMilestoneRef(ref) {
		return ref
	}
	return ""
}

// makeMilestoneMetadata merges a milestone ref into the issue's existing metadata.
func makeMilestoneMetadata(issue *types.Issue, ref string) json.RawMessage {
	md := make(map[string]interface{})
	if len(issue.Metadata) > 0 {
		_ = json.Unmarshal(issue.Metadata, &md)
	}
	md["gitlab_milestone_ref"] = ref
	raw, _ := json.Marshal(md)
	return json.RawMessage(raw)
}
