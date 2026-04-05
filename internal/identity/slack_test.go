package identity

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNewSlackClient_NilWithoutToken(t *testing.T) {
	client := NewSlackClient(SlackClientConfig{})
	if client != nil {
		t.Error("expected nil client when no bot token provided")
	}
}

func TestNewSlackClient_CreatesWithToken(t *testing.T) {
	client := NewSlackClient(SlackClientConfig{BotToken: "xoxb-test"})
	if client == nil {
		t.Error("expected non-nil client with bot token")
	}
}

func TestSlackClient_ListMembers_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify auth header
		if auth := r.Header.Get("Authorization"); auth != "Bearer xoxb-test" {
			t.Errorf("expected Bearer auth, got %q", auth)
		}

		resp := slackUsersListResponse{
			OK: true,
			Members: []struct {
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
			}{
				{
					ID: "U001", Name: "john", TeamID: "T001",
					Profile: struct {
						RealName    string `json:"real_name"`
						DisplayName string `json:"display_name"`
						Email       string `json:"email"`
					}{"John Doe", "johnd", "john@company.com"},
				},
				{
					ID: "U002", Name: "jane", TeamID: "T001",
					Profile: struct {
						RealName    string `json:"real_name"`
						DisplayName string `json:"display_name"`
						Email       string `json:"email"`
					}{"Jane Smith", "janes", "jane@company.com"},
				},
				{
					ID: "U003", Name: "bot", TeamID: "T001", IsBot: true,
					Profile: struct {
						RealName    string `json:"real_name"`
						DisplayName string `json:"display_name"`
						Email       string `json:"email"`
					}{"My Bot", "", ""},
				},
				{
					ID: "U004", Name: "left", TeamID: "T001", Deleted: true,
					Profile: struct {
						RealName    string `json:"real_name"`
						DisplayName string `json:"display_name"`
						Email       string `json:"email"`
					}{"Left User", "", "left@company.com"},
				},
				{
					ID: "USLACKBOT", Name: "slackbot", TeamID: "T001",
					Profile: struct {
						RealName    string `json:"real_name"`
						DisplayName string `json:"display_name"`
						Email       string `json:"email"`
					}{"Slackbot", "", ""},
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := NewSlackClient(SlackClientConfig{
		BotToken: "xoxb-test",
		BaseURL:  server.URL,
	})

	members, err := client.ListMembers()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should include all 4 non-USLACKBOT members (filtering happens in service)
	// The client returns bots and deleted users so the service can decide
	if len(members) != 4 {
		t.Errorf("expected 4 members (excluding USLACKBOT), got %d", len(members))
	}

	// Verify first member
	if members[0].ID != "U001" || members[0].RealName != "John Doe" {
		t.Errorf("unexpected first member: %+v", members[0])
	}
	if members[0].Email != "john@company.com" {
		t.Errorf("expected email john@company.com, got %q", members[0].Email)
	}

	// Verify bot flag
	found := false
	for _, m := range members {
		if m.ID == "U003" && m.IsBot {
			found = true
		}
	}
	if !found {
		t.Error("expected bot user U003 with IsBot=true")
	}
}

func TestSlackClient_ListMembers_Pagination(t *testing.T) {
	callCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		cursor := r.URL.Query().Get("cursor")

		var resp slackUsersListResponse
		resp.OK = true

		if cursor == "" {
			// First page
			resp.Members = append(resp.Members, struct {
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
			}{ID: "U001", Name: "john"})
			resp.ResponseMetadata.NextCursor = "page2"
		} else {
			// Second page
			resp.Members = append(resp.Members, struct {
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
			}{ID: "U002", Name: "jane"})
			// No next cursor → pagination complete
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := NewSlackClient(SlackClientConfig{
		BotToken: "xoxb-test",
		BaseURL:  server.URL,
	})

	members, err := client.ListMembers()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if callCount != 2 {
		t.Errorf("expected 2 API calls for pagination, got %d", callCount)
	}
	if len(members) != 2 {
		t.Errorf("expected 2 members across pages, got %d", len(members))
	}
}

func TestSlackClient_ListMembers_APIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]interface{}{
			"ok":    false,
			"error": "invalid_auth",
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := NewSlackClient(SlackClientConfig{
		BotToken: "xoxb-bad-token",
		BaseURL:  server.URL,
	})

	_, err := client.ListMembers()
	if err == nil {
		t.Error("expected error for invalid auth")
	}
}

func TestSlackClient_ListMembers_HTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("internal error"))
	}))
	defer server.Close()

	client := NewSlackClient(SlackClientConfig{
		BotToken: "xoxb-test",
		BaseURL:  server.URL,
	})

	_, err := client.ListMembers()
	if err == nil {
		t.Error("expected error for HTTP 500")
	}
}
