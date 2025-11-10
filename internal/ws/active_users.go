package ws

import (
	"encoding/json"
	"net/http"
)

type ActiveUser struct {
	Username string `json:"username"`
	Status   string `json:"status"` // "waiting", "in_game"
}

// GetActiveUsers returns a list of all active users
func (h *Hub) GetActiveUsers() []ActiveUser {
	h.mu.Lock()
	defer h.mu.Unlock()

	activeUsers := make([]ActiveUser, 0)
	for client := range h.clients {
		if client.username != "" && !client.isBot { // Only include non-bot users with usernames
			status := "waiting"
			if client.gameID != "" {
				status = "in_game"
			}
			activeUsers = append(activeUsers, ActiveUser{
				Username: client.username,
				Status:   status,
			})
		}
	}
	return activeUsers
}

// HandleActiveUsers is an HTTP handler for getting active users
func (h *Hub) HandleActiveUsers(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")

	if r.Method == "OPTIONS" {
		w.WriteHeader(http.StatusOK)
		return
	}

	if r.Method != "GET" {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	activeUsers := h.GetActiveUsers()
	json.NewEncoder(w).Encode(activeUsers)
}
