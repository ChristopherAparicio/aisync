package client

import "time"

// ── Sync types ──

// PushResult contains the outcome of a push operation.
type PushResult struct {
	Pushed int  `json:"Pushed"`
	Remote bool `json:"Remote"`
}

// PullResult contains the outcome of a pull operation.
type PullResult struct {
	Pulled int `json:"Pulled"`
}

// SyncResult contains the outcome of a sync (pull+push) operation.
type SyncResult struct {
	Pulled int  `json:"Pulled"`
	Pushed int  `json:"Pushed"`
	Remote bool `json:"Remote"`
}

// IndexEntry is a lightweight summary for the sync branch index.
type IndexEntry struct {
	ID           string `json:"id"`
	Provider     string `json:"provider"`
	Agent        string `json:"agent"`
	Branch       string `json:"branch,omitempty"`
	Summary      string `json:"summary,omitempty"`
	MessageCount int    `json:"message_count"`
}

// Index holds the sync branch index.
type Index struct {
	UpdatedAt time.Time    `json:"updated_at"`
	Entries   []IndexEntry `json:"entries"`
	Version   int          `json:"version"`
}

// ── Sync methods ──

// Push exports sessions to the sync branch and optionally pushes to remote.
func (c *Client) Push(remote bool) (*PushResult, error) {
	body := struct {
		Remote bool `json:"remote,omitempty"`
	}{Remote: remote}

	data, err := c.doPost("/api/v1/sync/push", body)
	if err != nil {
		return nil, err
	}
	var result PushResult
	return &result, decode(data, &result)
}

// Pull imports sessions from the sync branch.
func (c *Client) Pull(remote bool) (*PullResult, error) {
	body := struct {
		Remote bool `json:"remote,omitempty"`
	}{Remote: remote}

	data, err := c.doPost("/api/v1/sync/pull", body)
	if err != nil {
		return nil, err
	}
	var result PullResult
	return &result, decode(data, &result)
}

// Sync performs pull then push.
func (c *Client) Sync(remote bool) (*SyncResult, error) {
	body := struct {
		Remote bool `json:"remote,omitempty"`
	}{Remote: remote}

	data, err := c.doPost("/api/v1/sync/sync", body)
	if err != nil {
		return nil, err
	}
	var result SyncResult
	return &result, decode(data, &result)
}

// ReadIndex reads the sync branch index.
func (c *Client) ReadIndex() (*Index, error) {
	data, err := c.doGet("/api/v1/sync/index")
	if err != nil {
		return nil, err
	}
	var index Index
	return &index, decode(data, &index)
}
