package main

import (
	"context"
	"fmt"
	"io"
	"strconv"

	"github.com/steveyegge/beads/internal/gitlab"
	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/types"
)

// syncGitLabDependencies synchronizes beads dependencies with GitLab issue links.
// Pull: fetches GitLab issue links and creates beads dependencies.
// Push: creates GitLab issue links from beads dependencies.
func syncGitLabDependencies(ctx context.Context, client *gitlab.Client, st storage.Storage, act string, out io.Writer, dryRun, pullOnly, pushOnly bool) error {
	pull := !pushOnly
	push := !pullOnly

	if pull {
		if err := pullGitLabDependencies(ctx, client, st, act, out, dryRun); err != nil {
			return fmt.Errorf("dependency pull: %w", err)
		}
	}

	if push {
		if err := pushGitLabDependencies(ctx, client, st, out, dryRun); err != nil {
			return fmt.Errorf("dependency push: %w", err)
		}
	}

	return nil
}

// pullGitLabDependencies fetches issue links from GitLab and creates beads dependencies.
func pullGitLabDependencies(ctx context.Context, client *gitlab.Client, st storage.Storage, act string, out io.Writer, dryRun bool) error {
	// Get all issues with GitLab external refs
	filter := types.IssueFilter{}
	issues, err := st.SearchIssues(ctx, "", filter)
	if err != nil {
		return fmt.Errorf("searching issues: %w", err)
	}

	created := 0
	for _, issue := range issues {
		extRef := derefStrLocal(issue.ExternalRef)
		if extRef == "" {
			continue
		}

		// Extract GitLab IID from external ref
		iid := extractGitLabIID(extRef)
		if iid == 0 {
			continue
		}

		links, err := client.GetIssueLinks(ctx, iid)
		if err != nil {
			_, _ = fmt.Fprintf(out, "  Warning: failed to fetch links for %s (IID %d): %v\n", issue.ID, iid, err)
			continue
		}

		for _, link := range links {
			dep, err := linkToDependency(ctx, st, issue, iid, link)
			if err != nil || dep == nil {
				continue
			}

			// Check if dependency already exists
			existing, _ := st.GetDependenciesWithMetadata(ctx, dep.IssueID)
			alreadyExists := false
			for _, e := range existing {
				if e.ID == dep.DependsOnID && e.DependencyType == dep.Type {
					alreadyExists = true
					break
				}
			}
			if alreadyExists {
				continue
			}

			if dryRun {
				_, _ = fmt.Fprintf(out, "  [dry-run] Would create dependency: %s -[%s]-> %s\n",
					dep.IssueID, dep.Type, dep.DependsOnID)
				created++
				continue
			}

			if err := st.AddDependency(ctx, dep, act); err != nil {
				_, _ = fmt.Fprintf(out, "  Warning: failed to create dependency %s -> %s: %v\n",
					dep.IssueID, dep.DependsOnID, err)
				continue
			}
			created++
		}
	}

	if created > 0 {
		if dryRun {
			_, _ = fmt.Fprintf(out, "  [dry-run] Would create %d dependencies\n", created)
		} else {
			_, _ = fmt.Fprintf(out, "  ✓ Created %d dependencies from GitLab links\n", created)
		}
	}

	return nil
}

// pushGitLabDependencies creates GitLab issue links from beads dependencies.
func pushGitLabDependencies(ctx context.Context, client *gitlab.Client, st storage.Storage, out io.Writer, dryRun bool) error {
	// Get all issues with GitLab external refs
	filter := types.IssueFilter{}
	issues, err := st.SearchIssues(ctx, "", filter)
	if err != nil {
		return fmt.Errorf("searching issues: %w", err)
	}

	// Build a map of beads ID → GitLab IID for quick lookup
	beadsToIID := make(map[string]int)
	for _, issue := range issues {
		extRef := derefStrLocal(issue.ExternalRef)
		if iid := extractGitLabIID(extRef); iid != 0 {
			beadsToIID[issue.ID] = iid
		}
	}

	created := 0
	for _, issue := range issues {
		sourceIID, ok := beadsToIID[issue.ID]
		if !ok {
			continue
		}

		// Get local dependencies for this issue
		deps, err := st.GetDependenciesWithMetadata(ctx, issue.ID)
		if err != nil {
			continue
		}

		// Get existing GitLab links to avoid duplicates
		existingLinks, err := client.GetIssueLinks(ctx, sourceIID)
		if err != nil {
			_, _ = fmt.Fprintf(out, "  Warning: failed to fetch existing links for %s: %v\n", issue.ID, err)
			continue
		}

		existingSet := make(map[string]bool)
		for _, link := range existingLinks {
			if link.TargetIssue != nil {
				existingSet[fmt.Sprintf("%d:%s", link.TargetIssue.IID, link.LinkType)] = true
			}
			if link.SourceIssue != nil && link.SourceIssue.IID != sourceIID {
				existingSet[fmt.Sprintf("%d:%s", link.SourceIssue.IID, link.LinkType)] = true
			}
		}

		for _, dep := range deps {
			targetIID, ok := beadsToIID[dep.ID]
			if !ok {
				continue // Target not synced to GitLab
			}

			linkType := beadsDepToGitLabLink(dep.DependencyType)
			if linkType == "" {
				continue
			}

			// Skip if link already exists
			key := fmt.Sprintf("%d:%s", targetIID, linkType)
			if existingSet[key] {
				continue
			}

			if dryRun {
				_, _ = fmt.Fprintf(out, "  [dry-run] Would create GitLab link: #%d -[%s]-> #%d\n",
					sourceIID, linkType, targetIID)
				created++
				continue
			}

			if _, err := client.CreateIssueLink(ctx, sourceIID, targetIID, linkType); err != nil {
				_, _ = fmt.Fprintf(out, "  Warning: failed to create link #%d -> #%d: %v\n",
					sourceIID, targetIID, err)
				continue
			}
			created++
		}
	}

	if created > 0 {
		if dryRun {
			_, _ = fmt.Fprintf(out, "  [dry-run] Would create %d GitLab issue links\n", created)
		} else {
			_, _ = fmt.Fprintf(out, "  ✓ Created %d GitLab issue links\n", created)
		}
	}

	return nil
}

// linkToDependency converts a GitLab IssueLink to a beads Dependency.
// Returns nil if the linked issue is not found in beads.
//
// Direction semantics:
//   - beads Dependency{IssueID: A, DependsOnID: B, Type: blocks} means "A is blocked by B"
//   - GitLab "blocks": source blocks target → target depends on source
//   - GitLab "is_blocked_by": source is blocked by target → source depends on target
func linkToDependency(ctx context.Context, st storage.Storage, localIssue *types.Issue, localIID int, link gitlab.IssueLink) (*types.Dependency, error) {
	var otherIssue *gitlab.Issue
	weAreSource := false

	// Determine the other issue and our role in the link
	if link.SourceIssue != nil && link.SourceIssue.IID == localIID {
		otherIssue = link.TargetIssue
		weAreSource = true
	} else if link.TargetIssue != nil && link.TargetIssue.IID == localIID {
		otherIssue = link.SourceIssue
	}

	if otherIssue == nil || otherIssue.IID == 0 {
		return nil, nil
	}

	// Find the beads issue for the other side
	var otherBeads *types.Issue
	if otherIssue.WebURL != "" {
		otherBeads, _ = st.GetIssueByExternalRef(ctx, otherIssue.WebURL)
	}
	if otherBeads == nil {
		return nil, nil // Other issue not in beads
	}

	// Map link type + direction to beads dependency
	issueID := localIssue.ID
	dependsOnID := otherBeads.ID
	var depType types.DependencyType

	switch link.LinkType {
	case "blocks":
		if weAreSource {
			// We block other → other depends on us → flip direction
			issueID = otherBeads.ID
			dependsOnID = localIssue.ID
		}
		// else: other blocks us → we depend on other (default direction)
		depType = types.DepBlocks
	case "is_blocked_by":
		if weAreSource {
			// We are blocked by other → we depend on other (default direction)
		} else {
			// Other is blocked by us → other depends on us → flip
			issueID = otherBeads.ID
			dependsOnID = localIssue.ID
		}
		depType = types.DepBlocks
	case "relates_to":
		depType = types.DepRelated
	default:
		depType = types.DepRelated
	}

	return &types.Dependency{
		IssueID:     issueID,
		DependsOnID: dependsOnID,
		Type:        depType,
	}, nil
}

// beadsDepToGitLabLink maps beads dependency types to GitLab link types.
func beadsDepToGitLabLink(depType types.DependencyType) string {
	switch depType {
	case types.DepBlocks:
		return "blocks"
	case types.DepRelated:
		return "relates_to"
	case types.DepParentChild:
		return "" // Handled by milestone/epic sync, not issue links
	default:
		return "relates_to"
	}
}

// extractGitLabIID extracts the GitLab issue IID from an external ref URL.
// E.g., "https://gitlab.com/group/project/-/issues/42" → 42
func extractGitLabIID(ref string) int {
	matches := gitlab.IssueIIDPattern.FindStringSubmatch(ref)
	if len(matches) < 2 {
		return 0
	}
	iid, err := strconv.Atoi(matches[1])
	if err != nil {
		return 0
	}
	return iid
}
