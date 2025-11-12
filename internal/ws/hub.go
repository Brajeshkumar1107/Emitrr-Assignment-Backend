package ws

import (
	"encoding/json"
	"log"
	"sync"
	"time"

	"github.com/connect4/backend/internal/database"
	"github.com/gorilla/websocket"
)

// Hub maintains the set of active clients and broadcasts messages
type Hub struct {
	clients        map[*Client]bool
	register       chan *Client
	unregister     chan *Client
	waitingPlayer  *Client
	activeGames    map[string]*WSGame
	mu             sync.Mutex
	db             *database.DB
	producer       interface{}
}

// Client represents a connected player
type Client struct {
	hub             *Hub
	conn            *websocket.Conn
	send            chan []byte
	username        string
	gameID          string
	isBot           bool
	disconnectedAt  *time.Time
	waitingBotTimer *time.Timer
}

// Message represents the WebSocket message structure
type Message struct {
	Type    string      `json:"type"`
	Payload interface{} `json:"payload"`
}

// NewHub creates a new Hub instance
func NewHub() *Hub {
	return &Hub{
		clients:     make(map[*Client]bool),
		register:    make(chan *Client),
		unregister:  make(chan *Client),
		activeGames: make(map[string]*WSGame),
	}
}

// SetDB sets the database connection (optional)
func (h *Hub) SetDB(db *database.DB) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.db = db
}

// SetProducer sets the analytics producer (optional)
func (h *Hub) SetProducer(producer interface{}) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.producer = producer
}

// Run starts the hub
func (h *Hub) Run() {
	for {
		select {
		case client := <-h.register:
			h.clients[client] = true

		case client := <-h.unregister:
			if _, ok := h.clients[client]; ok {
				delete(h.clients, client)
				if client.send != nil {
					close(client.send)
					client.send = nil
				}
				h.handlePlayerDisconnect(client)
			}
		}
	}
}

// handleNewPlayer processes a new player connection
func (h *Hub) handleNewPlayer(client *Client, gameMode string) {
	log.Printf("[BACKEND-10] Hub.handleNewPlayer: Called for username=%s, mode=%s", client.username, gameMode)

	if h.reconnectClient(client) {
		log.Printf("[BACKEND-10] Hub.handleNewPlayer: Successfully reconnected %s to existing game", client.username)
		return
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	if gameMode == "computer" {
		log.Printf("[BACKEND-11] Hub.handleNewPlayer: COMPUTER MODE - Creating immediate bot game for %s", client.username)
		botClient := &Client{
			hub:      h,
			conn:     nil,
			send:     nil,
			username: "AI Bot",
			isBot:    true,
		}
		h.clients[botClient] = true
		h.createGame(client, botClient)
		return
	}

	if h.waitingPlayer == nil {
		h.waitingPlayer = client
		h.sendWaitingMessage(client)
		client.waitingBotTimer = time.AfterFunc(10*time.Second, func() {
			h.mu.Lock()
			defer h.mu.Unlock()
			if h.waitingPlayer == client {
				h.waitingPlayer = nil
				botClient := &Client{
					hub:      h,
					username: "AI Bot",
					isBot:    true,
				}
				h.clients[botClient] = true
				h.createGame(client, botClient)
			}
		})
	} else {
		waiting := h.waitingPlayer
		if waiting.waitingBotTimer != nil {
			waiting.waitingBotTimer.Stop()
		}
		h.waitingPlayer = nil
		h.createGame(waiting, client)
	}
}

// handlePlayerDisconnect handles player disconnection
func (h *Hub) handlePlayerDisconnect(client *Client) {
	h.mu.Lock()
	defer h.mu.Unlock()

	now := time.Now()
	client.disconnectedAt = &now

	if h.waitingPlayer == client {
		if client.waitingBotTimer != nil {
			client.waitingBotTimer.Stop()
		}
		h.waitingPlayer = nil
		return
	}

	if client.gameID == "" {
		return
	}

	g, exists := h.activeGames[client.gameID]
	if !exists {
		return
	}

	if !client.isBot {
		client.waitingBotTimer = time.AfterFunc(10*time.Second, func() {
			h.mu.Lock()
			defer h.mu.Unlock()
			if client.disconnectedAt != nil && g.game.IsActive {
				log.Printf("[BACKEND] Bot fallback triggered for player %s", client.username)
				botClient := &Client{
					hub:      h,
					username: "AI Bot",
					isBot:    true,
				}
				h.clients[botClient] = true
				if g.game.Player1.ID == client.username {
					g.game.Player1.ID = botClient.username
				} else {
					g.game.Player2.ID = botClient.username
				}
			}
		})
	}

	time.AfterFunc(30*time.Second, func() {
		h.mu.Lock()
		defer h.mu.Unlock()
		if client.disconnectedAt != nil {
			delete(h.activeGames, client.gameID)
			delete(h.clients, client)
			if client.send != nil {
				close(client.send)
				client.send = nil
			}
		}
	})
}

// reconnectClient attempts to reconnect a previously disconnected client
func (h *Hub) reconnectClient(client *Client) bool {
	h.mu.Lock()
	defer h.mu.Unlock()

	var existingClient *Client
	for c := range h.clients {
		if c.username == client.username && c.disconnectedAt != nil {
			existingClient = c
			break
		}
	}

	if existingClient == nil {
		return false
	}

	if time.Since(*existingClient.disconnectedAt) > 30*time.Second {
		return false
	}

	if existingClient.waitingBotTimer != nil {
		existingClient.waitingBotTimer.Stop()
	}

	client.gameID = existingClient.gameID
	client.send = make(chan []byte, 256)
	delete(h.clients, existingClient)
	h.clients[client] = true
	client.disconnectedAt = nil

	if g, exists := h.activeGames[client.gameID]; exists {
		if g.game.Player1.ID == client.username {
			g.player1Client = client
		} else if g.game.Player2.ID == client.username {
			g.player2Client = client
		}
	}

	log.Printf("[BACKEND] Successfully reconnected client %s", client.username)
	return true
}

// sendWaitingMessage sends waiting status to client
func (h *Hub) sendWaitingMessage(client *Client) {
	if client.send == nil {
		return
	}

	board := make([][]int, 6)
	for i := range board {
		board[i] = make([]int, 7)
	}

	msg := GameMessage{
		Type:   "gameState",
		Payload: map[string]interface{}{
			"status":      "waiting",
			"board":       board,
			"currentTurn": 1,
		},
	}

	if data, err := json.Marshal(msg); err == nil {
		select {
		case client.send <- data:
		default:
		}
	}
}

// handlePlayAgain handles a client's request to play again
func (h *Hub) handlePlayAgain(client *Client) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if client.conn == nil {
		log.Printf("[BACKEND-PLAYAGAIN] Ignoring playAgain from disconnected client %s", client.username)
		return
	}

	if client.gameID == "" {
		return
	}
	g, exists := h.activeGames[client.gameID]
	if !exists {
		return
	}
	if g.game.IsActive {
		return
	}

	for _, u := range g.PlayAgainRequests {
		if u == client.username {
			return
		}
	}
	g.PlayAgainRequests = append(g.PlayAgainRequests, client.username)

	msg := GameMessage{
		Type: "playAgainUpdate",
		Payload: map[string]interface{}{
			"playAgainRequests": g.PlayAgainRequests,
		},
	}
	if data, err := json.Marshal(msg); err == nil {
		if p1 := h.findClientUnsafe(g.game.Player1.ID); p1 != nil && p1.send != nil {
			select { case p1.send <- data: default: }
		}
		if p2 := h.findClientUnsafe(g.game.Player2.ID); p2 != nil && p2.send != nil {
			select { case p2.send <- data: default: }
		}
	}

	bothRequested := len(g.PlayAgainRequests) >= 2
	if g.player1Client != nil && g.player1Client.isBot {
		bothRequested = true
	}
	if g.player2Client != nil && g.player2Client.isBot {
		bothRequested = true
	}

	if bothRequested {
		delete(h.activeGames, g.game.ID)
		p1 := g.player1Client
		p2 := g.player2Client
		g.PlayAgainRequests = nil

		if (p1 != nil && p1.isBot) || (p2 != nil && p2.isBot) {
			var human *Client
			if p1 != nil && !p1.isBot {
				human = p1
			} else if p2 != nil && !p2.isBot {
				human = p2
			}

			if human != nil {
				if human.send == nil && human.conn != nil {
					human.send = make(chan []byte, 256)
				}

				oldGameID := human.gameID
				human.gameID = ""
				delete(h.activeGames, oldGameID)

				botClient := &Client{
					hub:      h,
					username: "AI Bot",
					isBot:    true,
				}
				h.clients[botClient] = true

				go func(human *Client, botClient *Client) {
					log.Printf("[BACKEND] Starting immediate rematch human=%s vs bot (fresh bot instance)", human.username)
					h.createGame(human, botClient)
				}(human, botClient)
				return
			}
		}

		h.createGame(p1, p2)
	}
}

// handleExit handles a client's request to exit the game
func (h *Hub) handleExit(client *Client) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if client.gameID == "" {
		return
	}
	g, exists := h.activeGames[client.gameID]
	if !exists {
		return
	}

	g.game.IsActive = false
	var otherClient *Client
	if g.game.Player1.ID == client.username {
		otherClient = h.findClientUnsafe(g.game.Player2.ID)
	} else {
		otherClient = h.findClientUnsafe(g.game.Player1.ID)
	}

	if otherClient != nil && otherClient.send != nil {
		msg := GameMessage{
			Type: "opponentExited",
			Payload: map[string]interface{}{
				"gameId":  client.gameID,
				"message": "opponentExited",
			},
		}
		if data, err := json.Marshal(msg); err == nil {
			select { case otherClient.send <- data: default: }
		}
		otherClient.gameID = ""
	}

	g.PlayAgainRequests = nil
	delete(h.activeGames, client.gameID)
	client.gameID = ""
}

// findClientUnsafe finds a client without locking
func (h *Hub) findClientUnsafe(username string) *Client {
	for client := range h.clients {
		if client.username == username {
			return client
		}
	}
	return nil
}
