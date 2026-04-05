package identity

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

// SlackClient is the port interface for fetching Slack workspace members.
// Implementations can use the real Slack API or be mocked for testing.
type SlackClient interface {
	// ListMembers fetches all active members from the Slack workspace.
	// Returns only non-bot, non-deleted users.
	ListMembers() ([]SlackMember, error)
}

// slackAPIClient implements SlackClient using the Slack Web API.
// No external dependencies — uses net/http directly.
type slackAPIClient struct {
	botToken   string
	httpClient *http.Client
	baseURL    string // for testing, defaults to "https://slack.com/api"
}

// SlackClientConfig configures the Slack API client.
type SlackClientConfig struct {
	// BotToken is the Slack Bot User OAuth Token (xoxb-...).
	// Requires the users:read scope. For emails, also needs users:read.email.
	BotToken string

	// Timeout for HTTP requests (default: 15s).
	Timeout time.Duration

	// BaseURL overrides the Slack API base URL (for testing).
	BaseURL string
}

// NewSlackClient creates a Slack API client for fetching workspace members.
// Returns nil if no bot token is provided.
func NewSlackClient(cfg SlackClientConfig) SlackClient {
	if cfg.BotToken == "" {
		return nil
	}
	timeout := cfg.Timeout
	if timeout == 0 {
		timeout = 15 * time.Second
	}
	baseURL := cfg.BaseURL
	if baseURL == "" {
		baseURL = "https://slack.com/api"
	}
	return &slackAPIClient{
		botToken:   cfg.BotToken,
		httpClient: &http.Client{Timeout: timeout},
		baseURL:    baseURL,
	}
}

// slackUsersListResponse matches the Slack users.list API response shape.
type slackUsersListResponse struct {
	OK      bool   `json:"ok"`
	Error   string `json:"error,omitempty"`
	Members []struct {
		ID      string `json:"id"`
		Name    string `json:"name"`
		Deleted bool   `json:"deleted"`
		IsBot   bool   `json:"is_bot"`
		TeamID  string `json:"team_id"`
		Profile struct {
			RealName    string `json:"real_name"`
			DisplayName string `json:"display_name"`
			Email       string `json:"email"`
		} `json:"profile"`
	} `json:"members"`
	ResponseMetadata struct {
		NextCursor string `json:"next_cursor"`
	} `json:"response_metadata"`
}

// ListMembers fetches all active, non-bot members from the workspace.
// Handles Slack API pagination via cursor.
func (c *slackAPIClient) ListMembers() ([]SlackMember, error) {
	var allMembers []SlackMember
	cursor := ""

	for {
		members, nextCursor, err := c.fetchPage(cursor)
		if err != nil {
			return nil, err
		}
		allMembers = append(allMembers, members...)

		if nextCursor == "" {
			break
		}
		cursor = nextCursor
	}

	return allMembers, nil
}

// fetchPage fetches a single page of users from the Slack API.
func (c *slackAPIClient) fetchPage(cursor string) ([]SlackMember, string, error) {
	params := url.Values{
		"limit": {"200"}, // max per page
	}
	if cursor != "" {
		params.Set("cursor", cursor)
	}

	reqURL := c.baseURL + "/users.list?" + params.Encode()
	req, err := http.NewRequest(http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, "", fmt.Errorf("slack users.list: creating request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.botToken)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("slack users.list: sending request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, "", fmt.Errorf("slack users.list: HTTP %d: %s", resp.StatusCode, body)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 10<<20)) // 10MB max
	if err != nil {
		return nil, "", fmt.Errorf("slack users.list: reading response: %w", err)
	}

	var result slackUsersListResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, "", fmt.Errorf("slack users.list: parsing response: %w", err)
	}

	if !result.OK {
		return nil, "", fmt.Errorf("slack users.list: API error: %s", result.Error)
	}

	// Filter to active, non-bot human members
	var members []SlackMember
	for _, m := range result.Members {
		// Skip USLACKBOT (the built-in Slack bot)
		if m.ID == "USLACKBOT" {
			continue
		}
		member := SlackMember{
			ID:          m.ID,
			RealName:    m.Profile.RealName,
			DisplayName: m.Profile.DisplayName,
			Email:       m.Profile.Email,
			IsBot:       m.IsBot,
			Deleted:     m.Deleted,
			TeamID:      m.TeamID,
		}
		members = append(members, member)
	}

	return members, result.ResponseMetadata.NextCursor, nil
}
