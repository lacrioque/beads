package gitlab

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
)

// ListMilestones retrieves milestones from the GitLab project.
// state can be: "active", "closed", or "" for all.
func (c *Client) ListMilestones(ctx context.Context, state string) ([]Milestone, error) {
	var all []Milestone
	page := 1

	for {
		select {
		case <-ctx.Done():
			return all, ctx.Err()
		default:
		}

		params := map[string]string{
			"per_page": strconv.Itoa(MaxPageSize),
			"page":     strconv.Itoa(page),
		}
		if state != "" {
			params["state"] = state
		}

		urlStr := c.buildURL("/projects/"+c.projectPath()+"/milestones", params)
		respBody, headers, err := c.doRequest(ctx, http.MethodGet, urlStr, nil)
		if err != nil {
			return nil, fmt.Errorf("failed to fetch milestones: %w", err)
		}

		var milestones []Milestone
		if err := json.Unmarshal(respBody, &milestones); err != nil {
			return nil, fmt.Errorf("failed to parse milestones response: %w", err)
		}

		all = append(all, milestones...)

		nextPage := headers.Get("X-Next-Page")
		if nextPage == "" {
			break
		}
		page++
		if page > MaxPages {
			return nil, fmt.Errorf("pagination limit exceeded: stopped after %d pages", MaxPages)
		}
	}

	return all, nil
}

// GetMilestone retrieves a single milestone by its ID.
func (c *Client) GetMilestone(ctx context.Context, milestoneID int) (*Milestone, error) {
	urlStr := c.buildURL("/projects/"+c.projectPath()+"/milestones/"+strconv.Itoa(milestoneID), nil)
	respBody, _, err := c.doRequest(ctx, http.MethodGet, urlStr, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch milestone %d: %w", milestoneID, err)
	}

	var ms Milestone
	if err := json.Unmarshal(respBody, &ms); err != nil {
		return nil, fmt.Errorf("failed to parse milestone response: %w", err)
	}
	return &ms, nil
}

// SearchMilestoneByTitle finds a milestone by its exact title.
// Returns nil, nil if no milestone matches.
func (c *Client) SearchMilestoneByTitle(ctx context.Context, title string) (*Milestone, error) {
	params := map[string]string{
		"title": title,
	}
	urlStr := c.buildURL("/projects/"+c.projectPath()+"/milestones", params)
	respBody, _, err := c.doRequest(ctx, http.MethodGet, urlStr, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to search milestones: %w", err)
	}

	var milestones []Milestone
	if err := json.Unmarshal(respBody, &milestones); err != nil {
		return nil, fmt.Errorf("failed to parse milestone search response: %w", err)
	}
	if len(milestones) == 0 {
		return nil, nil
	}
	return &milestones[0], nil
}

// CreateMilestone creates a new milestone in the GitLab project.
func (c *Client) CreateMilestone(ctx context.Context, title, description, dueDate, startDate string) (*Milestone, error) {
	body := map[string]any{
		"title": title,
	}
	if description != "" {
		body["description"] = description
	}
	if dueDate != "" {
		body["due_date"] = dueDate
	}
	if startDate != "" {
		body["start_date"] = startDate
	}

	urlStr := c.buildURL("/projects/"+c.projectPath()+"/milestones", nil)
	respBody, _, err := c.doRequest(ctx, http.MethodPost, urlStr, body)
	if err != nil {
		return nil, fmt.Errorf("failed to create milestone: %w", err)
	}

	var ms Milestone
	if err := json.Unmarshal(respBody, &ms); err != nil {
		return nil, fmt.Errorf("failed to parse create milestone response: %w", err)
	}
	return &ms, nil
}

// UpdateMilestone updates an existing milestone in the GitLab project.
func (c *Client) UpdateMilestone(ctx context.Context, milestoneID int, updates map[string]any) (*Milestone, error) {
	urlStr := c.buildURL("/projects/"+c.projectPath()+"/milestones/"+strconv.Itoa(milestoneID), nil)
	respBody, _, err := c.doRequest(ctx, http.MethodPut, urlStr, updates)
	if err != nil {
		return nil, fmt.Errorf("failed to update milestone %d: %w", milestoneID, err)
	}

	var ms Milestone
	if err := json.Unmarshal(respBody, &ms); err != nil {
		return nil, fmt.Errorf("failed to parse update milestone response: %w", err)
	}
	return &ms, nil
}

// ListMilestoneIssues retrieves issues assigned to a specific milestone.
func (c *Client) ListMilestoneIssues(ctx context.Context, milestoneID int) ([]Issue, error) {
	var all []Issue
	page := 1

	for {
		select {
		case <-ctx.Done():
			return all, ctx.Err()
		default:
		}

		params := map[string]string{
			"per_page": strconv.Itoa(MaxPageSize),
			"page":     strconv.Itoa(page),
		}

		urlStr := c.buildURL("/projects/"+c.projectPath()+"/milestones/"+strconv.Itoa(milestoneID)+"/issues", params)
		respBody, headers, err := c.doRequest(ctx, http.MethodGet, urlStr, nil)
		if err != nil {
			return nil, fmt.Errorf("failed to fetch milestone issues: %w", err)
		}

		var issues []Issue
		if err := json.Unmarshal(respBody, &issues); err != nil {
			return nil, fmt.Errorf("failed to parse milestone issues response: %w", err)
		}

		all = append(all, issues...)

		nextPage := headers.Get("X-Next-Page")
		if nextPage == "" {
			break
		}
		page++
		if page > MaxPages {
			return nil, fmt.Errorf("pagination limit exceeded: stopped after %d pages", MaxPages)
		}
	}

	return all, nil
}
