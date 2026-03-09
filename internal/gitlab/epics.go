package gitlab

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// Epic represents a GitLab group epic (requires Premium/Ultimate).
type Epic struct {
	ID          int        `json:"id"`
	IID         int        `json:"iid"`
	GroupID     int        `json:"group_id"`
	Title       string     `json:"title"`
	Description string     `json:"description"`
	State       string     `json:"state"` // "opened", "closed"
	CreatedAt   *time.Time `json:"created_at,omitempty"`
	UpdatedAt   *time.Time `json:"updated_at,omitempty"`
	ClosedAt    *time.Time `json:"closed_at,omitempty"`
	StartDate   string     `json:"start_date,omitempty"`
	DueDate     string     `json:"due_date,omitempty"`
	WebURL      string     `json:"web_url"`
	Labels      []string   `json:"labels"`
	Author      *User      `json:"author,omitempty"`
	Confidential bool      `json:"confidential"`
}

// EpicIssueLink represents a link between a GitLab epic and an issue.
type EpicIssueLink struct {
	ID        int    `json:"id"`
	Epic      *Epic  `json:"epic,omitempty"`
	Issue     *Issue `json:"issue,omitempty"`
	RelativePosition int `json:"relative_position"`
}

// ListEpics retrieves epics from a GitLab group.
// state can be: "opened", "closed", or "" for all.
// Requires gitlab.group_id to be configured.
func (c *Client) ListEpics(ctx context.Context, groupID, state string) ([]Epic, error) {
	var all []Epic
	page := 1
	encodedGroup := url.PathEscape(groupID)

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

		urlStr := c.buildURL("/groups/"+encodedGroup+"/epics", params)
		respBody, headers, err := c.doRequest(ctx, http.MethodGet, urlStr, nil)
		if err != nil {
			return nil, fmt.Errorf("failed to fetch epics: %w", err)
		}

		var epics []Epic
		if err := json.Unmarshal(respBody, &epics); err != nil {
			return nil, fmt.Errorf("failed to parse epics response: %w", err)
		}

		all = append(all, epics...)

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

// GetEpic retrieves a single epic by its IID within a group.
func (c *Client) GetEpic(ctx context.Context, groupID string, epicIID int) (*Epic, error) {
	encodedGroup := url.PathEscape(groupID)
	urlStr := c.buildURL("/groups/"+encodedGroup+"/epics/"+strconv.Itoa(epicIID), nil)
	respBody, _, err := c.doRequest(ctx, http.MethodGet, urlStr, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch epic %d: %w", epicIID, err)
	}

	var epic Epic
	if err := json.Unmarshal(respBody, &epic); err != nil {
		return nil, fmt.Errorf("failed to parse epic response: %w", err)
	}
	return &epic, nil
}

// CreateEpic creates a new epic in a GitLab group.
func (c *Client) CreateEpic(ctx context.Context, groupID, title, description string, labels []string) (*Epic, error) {
	encodedGroup := url.PathEscape(groupID)
	body := map[string]any{
		"title": title,
	}
	if description != "" {
		body["description"] = description
	}
	if len(labels) > 0 {
		body["labels"] = labels
	}

	urlStr := c.buildURL("/groups/"+encodedGroup+"/epics", nil)
	respBody, _, err := c.doRequest(ctx, http.MethodPost, urlStr, body)
	if err != nil {
		return nil, fmt.Errorf("failed to create epic: %w", err)
	}

	var epic Epic
	if err := json.Unmarshal(respBody, &epic); err != nil {
		return nil, fmt.Errorf("failed to parse create epic response: %w", err)
	}
	return &epic, nil
}

// UpdateEpic updates an existing epic in a GitLab group.
func (c *Client) UpdateEpic(ctx context.Context, groupID string, epicIID int, updates map[string]any) (*Epic, error) {
	encodedGroup := url.PathEscape(groupID)
	urlStr := c.buildURL("/groups/"+encodedGroup+"/epics/"+strconv.Itoa(epicIID), nil)
	respBody, _, err := c.doRequest(ctx, http.MethodPut, urlStr, updates)
	if err != nil {
		return nil, fmt.Errorf("failed to update epic %d: %w", epicIID, err)
	}

	var epic Epic
	if err := json.Unmarshal(respBody, &epic); err != nil {
		return nil, fmt.Errorf("failed to parse update epic response: %w", err)
	}
	return &epic, nil
}

// ListEpicIssues retrieves issues linked to a specific epic.
func (c *Client) ListEpicIssues(ctx context.Context, groupID string, epicIID int) ([]Issue, error) {
	var all []Issue
	page := 1
	encodedGroup := url.PathEscape(groupID)

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

		urlStr := c.buildURL("/groups/"+encodedGroup+"/epics/"+strconv.Itoa(epicIID)+"/issues", params)
		respBody, headers, err := c.doRequest(ctx, http.MethodGet, urlStr, nil)
		if err != nil {
			return nil, fmt.Errorf("failed to fetch epic issues: %w", err)
		}

		var links []EpicIssueLink
		if err := json.Unmarshal(respBody, &links); err != nil {
			return nil, fmt.Errorf("failed to parse epic issues response: %w", err)
		}

		for _, link := range links {
			if link.Issue != nil {
				all = append(all, *link.Issue)
			}
		}

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

// AddIssueToEpic links an issue to an epic.
func (c *Client) AddIssueToEpic(ctx context.Context, groupID string, epicIID, issueID int) error {
	encodedGroup := url.PathEscape(groupID)
	body := map[string]any{}
	urlStr := c.buildURL("/groups/"+encodedGroup+"/epics/"+strconv.Itoa(epicIID)+"/issues/"+strconv.Itoa(issueID), nil)
	_, _, err := c.doRequest(ctx, http.MethodPost, urlStr, body)
	if err != nil {
		return fmt.Errorf("failed to add issue %d to epic %d: %w", issueID, epicIID, err)
	}
	return nil
}
