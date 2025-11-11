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
	// Registered clients
	clients map[*Client]bool

	// Register requests from the clients
	register chan *Client

	// Unregister requests from clients
	unregister chan *Client

	// Game management
	waitingPlayer *Client
	activeGames   map[string]*WSGame
	mu            sync.Mutex

	// Optional database and analytics (can be nil)
	db       *database.DB
	producer interface{} // *analytics.Producer - using interface{} to avoid circular import
}

// Client represents a connected player
type Client struct {
	hub             *Hub
	conn            *websocket.Conn
	send            chan []byte
	username        string
	gameID          string
	isBot           bool
	disconnectedAt  *time.Time  // Time of disconnect, nil if connected
	waitingBotTimer *time.Timer // Timer for bot fallback in friend mode
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
			// Don't call handleNewPlayer here - it will be called when join message is received

		case client := <-h.unregister:
			if _, ok := h.clients[client]; ok {
				delete(h.clients, client)
				// Safely close the channel
				if client.send != nil {
					close(client.send)
					client.send = nil // Set to nil to prevent double-close
				}
				h.handlePlayerDisconnect(client)
			}
		}
	}
}

// handleNewPlayer processes a new player connection
func (h *Hub) handleNewPlayer(client *Client, gameMode string) {
	log.Printf("[BACKEND-10] Hub.handleNewPlayer: Called for username=%s, mode=%s", client.username, gameMode)

	// Try to reconnect first
	if h.reconnectClient(client) {
		log.Printf("[BACKEND-10] Hub.handleNewPlayer: Successfully reconnected %s to existing game", client.username)
		return
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	if gameMode == "computer" {
		// Immediately create game with bot
		log.Printf("[BACKEND-11] Hub.handleNewPlayer: COMPUTER MODE - Creating immediate bot game for %s", client.username)
		botClient := &Client{
			hub:      h,
			conn:     nil, // Bot doesn't need WebSocket connection
			send:     nil, // Bot doesn't need send channel
			username: "AI Bot",
			isBot:    true,
		}
		log.Printf("[BACKEND-12] Hub.handleNewPlayer: Bot client created, calling createGame(user=%s, bot=AI Bot)", client.username)
		h.createGame(client, botClient)
	} else if h.waitingPlayer == nil {
		// Friend mode - wait for another player
		log.Printf("[BACKEND-11] Hub.handleNewPlayer: FRIEND MODE - No waiting player, setting %s as waiting", client.username)
		h.waitingPlayer = client
		// Send waiting message to client
		log.Printf("[BACKEND-12] Hub.handleNewPlayer: Sending waiting message to %s", client.username)
		h.sendWaitingMessage(client)

		// Start 10-second bot fallback timer for initial matchmaking
		log.Printf("[BACKEND-13] Hub.handleNewPlayer: Starting 10-second bot matchmaking timer for %s", client.username)
		client.waitingBotTimer = time.AfterFunc(10*time.Second, func() {
			h.mu.Lock()
			defer h.mu.Unlock()

			// Check if player is still waiting and no one joined
			if h.waitingPlayer == client {
				log.Printf("[BACKEND-14] Hub.handleNewPlayer: 10-second timer expired, creating bot game for %s", client.username)
				h.waitingPlayer = nil

				// Create bot client
				botClient := &Client{
					hub:      h,
					conn:     nil, // Bot doesn't need WebSocket connection
					send:     nil, // Bot doesn't need send channel
					username: "AI Bot",
					isBot:    true,
				}

				// Create game with bot
				h.createGame(client, botClient)
			} else {
				log.Printf("[BACKEND-14] Hub.handleNewPlayer: Timer expired but player %s is no longer waiting (matched with another player)", client.username)
			}
		})
	} else {
		// Another player is waiting - create game
		waiting := h.waitingPlayer
		log.Printf("[BACKEND-11] Hub.handleNewPlayer: FRIEND MODE - Found waiting player %s, creating game with %s", waiting.username, client.username)

		// Stop the bot fallback timer if it's running
		if waiting.waitingBotTimer != nil {
			waiting.waitingBotTimer.Stop()
			waiting.waitingBotTimer = nil
			log.Printf("[BACKEND-12] Hub.handleNewPlayer: Stopped bot fallback timer for %s (matched with %s)", waiting.username, client.username)
		}

		h.waitingPlayer = nil
		log.Printf("[BACKEND-12] Hub.handleNewPlayer: Calling createGame(waiting=%s, new=%s)", waiting.username, client.username)
		h.createGame(waiting, client)
	}
}

// handlePlayerDisconnect handles player disconnection
func (h *Hub) handlePlayerDisconnect(client *Client) {
	h.mu.Lock()
	defer h.mu.Unlock()

	// Set disconnection time
	now := time.Now()
	client.disconnectedAt = &now

	// Remove from waiting if they were waiting
	if h.waitingPlayer == client {
		// Stop the bot fallback timer if it's running
		if client.waitingBotTimer != nil {
			client.waitingBotTimer.Stop()
			client.waitingBotTimer = nil
			log.Printf("[BACKEND] Stopped bot fallback timer for disconnected waiting player %s", client.username)
		}
		h.waitingPlayer = nil
		return
	}

	if client.gameID != "" {
		g, exists := h.activeGames[client.gameID]
		if !exists {
			return
		}

		// Start bot fallback timer (10 seconds) for friend mode
		if !client.isBot {
			if client.waitingBotTimer != nil {
				client.waitingBotTimer.Stop()
			}
			client.waitingBotTimer = time.AfterFunc(10*time.Second, func() {
				h.mu.Lock()
				defer h.mu.Unlock()

				// Check if player has reconnected
				if client.disconnectedAt != nil && g.game.IsActive {
					log.Printf("[BACKEND] Bot fallback timer triggered for player %s", client.username)

					// Create bot client to replace disconnected player
					botClient := &Client{
						hub:      h,
						conn:     nil,
						send:     nil,
						username: "AI Bot",
						isBot:    true,
						gameID:   client.gameID,
					}

					// Update game with bot
					if g.game.Player1.ID == client.username {
						g.game.Player1.ID = botClient.username
					} else {
						g.game.Player2.ID = botClient.username
					}

					// Notify other player about bot replacement
					var otherClient *Client
					if g.game.Player1.ID == client.username {
						otherClient = h.findClientUnsafe(g.game.Player2.ID)
					} else {
						otherClient = h.findClientUnsafe(g.game.Player1.ID)
					}

					if otherClient != nil && otherClient.send != nil {
						state := g.ToGameState()
						msg := GameMessage{
							Type:   "playerReplaced",
							GameID: g.game.ID,
							Payload: map[string]interface{}{
								"replacedPlayer": client.username,
								"newPlayer":      "AI Bot",
								"gameState":      state,
							},
						}
						if data, err := json.Marshal(msg); err == nil {
							select {
							case otherClient.send <- data:
							default:
								// Safely close the channel
								func() {
									defer func() {
										if r := recover(); r != nil {
											// Channel already closed, ignore panic
										}
									}()
									if otherClient.send != nil {
										close(otherClient.send)
										otherClient.send = nil
									}
								}()
							}
						}
					}

					// Clean up disconnected client
					delete(h.clients, client)
					// Safely close the channel (may already be closed)
					if client.send != nil {
						func() {
							defer func() {
								if r := recover(); r != nil {
									// Channel already closed, ignore panic
									log.Printf("[BACKEND] Channel already closed for client %s", client.username)
								}
							}()
							close(client.send)
							client.send = nil
						}()
					}
				}
			})
		}

		// Start disconnection cleanup timer (30 seconds)
		time.AfterFunc(30*time.Second, func() {
			h.mu.Lock()
			defer h.mu.Unlock()

			// If still disconnected after 30 seconds
			if client.disconnectedAt != nil {
				if g, exists := h.activeGames[client.gameID]; exists {
					g.game.IsActive = false

					// Notify other player
					var otherClient *Client
					if g.game.Player1.ID == client.username {
						otherClient = h.findClientUnsafe(g.game.Player2.ID)
					} else if g.game.Player2.ID == client.username {
						otherClient = h.findClientUnsafe(g.game.Player1.ID)
					}

					if otherClient != nil && otherClient.send != nil {
						state := g.ToGameState()
						state = g.game.GetState()
						msg := GameMessage{
							Type:    "gameState",
							GameID:  g.game.ID,
							Payload: state,
						}
						if data, err := json.Marshal(msg); err == nil {
							select {
							case otherClient.send <- data:
							default:
								// Safely close the channel
								func() {
									defer func() {
										if r := recover(); r != nil {
											// Channel already closed, ignore panic
										}
									}()
									if otherClient.send != nil {
										close(otherClient.send)
										otherClient.send = nil
									}
								}()
							}
						}
					}

					// Remove game from active games
					delete(h.activeGames, client.gameID)
				}

				// Clean up client
				delete(h.clients, client)
				// Safely close the channel (may already be closed)
				if client.send != nil {
					func() {
						defer func() {
							if r := recover(); r != nil {
								// Channel already closed, ignore panic
								log.Printf("[BACKEND] Channel already closed for client %s", client.username)
							}
						}()
						close(client.send)
						client.send = nil
					}()
				}
			}
		})
	}
}

// findClientUnsafe finds a client without locking (must be called with lock held)
func (h *Hub) findClientUnsafe(username string) *Client {
	for client := range h.clients {
		if client.username == username {
			return client
		}
	}
	return nil
}

// sendWaitingMessage sends a waiting status to the client
func (h *Hub) sendWaitingMessage(client *Client) {
	log.Printf("[BACKEND-14] Hub.sendWaitingMessage: Sending waiting message to %s", client.username)
	if client.send == nil {
		log.Printf("[BACKEND-14] Hub.sendWaitingMessage: Client %s has no send channel", client.username)
		return
	}

	// Create a proper board structure
	board := make([][]int, 6)
	for i := range board {
		board[i] = make([]int, 7)
	}

	msg := GameMessage{
		Type:   "gameState",
		GameID: "",
		Payload: map[string]interface{}{
			"status":      "waiting",
			"board":       board,
			"currentTurn": 1,
		},
	}

	if data, err := json.Marshal(msg); err == nil {
		log.Printf("[BACKEND-15] Hub.sendWaitingMessage: Waiting message marshaled, sending to %s", client.username)
		select {
		case client.send <- data:
			log.Printf("[BACKEND-16] Hub.sendWaitingMessage: Waiting message sent to %s", client.username)
		default:
			log.Printf("[BACKEND-15] Hub.sendWaitingMessage: Failed to send waiting message to %s (channel full)", client.username)
		}
	} else {
		log.Printf("[BACKEND-15] Hub.sendWaitingMessage: Error marshaling waiting message: %v", err)
	}
}

// reconnectClient attempts to reconnect a previously disconnected client
func (h *Hub) reconnectClient(client *Client) bool {
	h.mu.Lock()
	defer h.mu.Unlock()

	// Find existing client with same username
	var existingClient *Client
	for c := range h.clients {
		if c.username == client.username && c.disconnectedAt != nil {
			existingClient = c
			break
		}
	}

	if existingClient == nil {
		return false // No disconnected client found
	}

	// Check if within 30-second window
	if time.Since(*existingClient.disconnectedAt) > 30*time.Second {
		return false // Reconnection window expired
	}

	// Stop any pending timers
	if existingClient.waitingBotTimer != nil {
		existingClient.waitingBotTimer.Stop()
		existingClient.waitingBotTimer = nil
	}

	// Transfer game state to new connection
	client.gameID = existingClient.gameID
	client.send = make(chan []byte, 256)

	// Update game references
	if g, exists := h.activeGames[client.gameID]; exists {
		if g.game.Player1.ID == client.username {
			g.player1Client = client
		} else if g.game.Player2.ID == client.username {
			g.player2Client = client
		}

		// Send current game state
		state := g.ToGameState()
		msg := GameMessage{
			Type:    "gameState",
			GameID:  g.game.ID,
			Payload: state,
		}
		if data, err := json.Marshal(msg); err == nil {
			select {
			case client.send <- data:
				log.Printf("[BACKEND] Successfully sent game state to reconnected client %s", client.username)
			default:
				log.Printf("[BACKEND] Failed to send game state to reconnected client %s", client.username)
			}
		}

		// Notify other player about reconnection
		var otherClient *Client
		if g.game.Player1.ID == client.username {
			otherClient = h.findClientUnsafe(g.game.Player2.ID)
		} else {
			otherClient = h.findClientUnsafe(g.game.Player1.ID)
		}

		if otherClient != nil && otherClient.send != nil {
			msg := GameMessage{
				Type:   "playerReconnected",
				GameID: g.game.ID,
				Payload: map[string]interface{}{
					"username": client.username,
				},
			}
			if data, err := json.Marshal(msg); err == nil {
				select {
				case otherClient.send <- data:
					log.Printf("[BACKEND] Notified other player about reconnection of %s", client.username)
				default:
					log.Printf("[BACKEND] Failed to notify other player about reconnection of %s", client.username)
				}
			}
		}
	}

	// Remove old client and register new one
	delete(h.clients, existingClient)
	h.clients[client] = true
	client.disconnectedAt = nil // Reset disconnection time

	log.Printf("[BACKEND] Successfully reconnected client %s", client.username)
	return true
}

// handlePlayAgain handles a client's request to play again after a finished game.
func (h *Hub) handlePlayAgain(client *Client) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if client.gameID == "" {
		return
	}
	g, exists := h.activeGames[client.gameID]
	if !exists {
		return
	}

	// Do not accept play-again while game is still active
	if g.game.IsActive {
		return
	}

	// Avoid duplicate requests
	for _, u := range g.PlayAgainRequests {
		if u == client.username {
			return
		}
	}
	g.PlayAgainRequests = append(g.PlayAgainRequests, client.username)

	// Broadcast playAgainUpdate to both players
	payload := map[string]interface{}{
		"playAgainRequests": g.PlayAgainRequests,
	}
	msg := GameMessage{
		Type:    "playAgainUpdate",
		GameID:  g.game.ID,
		Payload: payload,
	}
	if data, err := json.Marshal(msg); err == nil {
		if p1 := h.findClientUnsafe(g.game.Player1.ID); p1 != nil && p1.send != nil {
			select {
			case p1.send <- data:
			default:
			}
		}
		if p2 := h.findClientUnsafe(g.game.Player2.ID); p2 != nil && p2.send != nil {
			select {
			case p2.send <- data:
			default:
			}
		}
	}

	// If both human players requested rematch, or one player is a bot, start rematch
	bothRequested := false
	if len(g.PlayAgainRequests) >= 2 {
		bothRequested = true
	}
	// If either player is a bot, treat as accepted
	if g.player1Client != nil && g.player1Client.isBot {
		bothRequested = true
	}
	if g.player2Client != nil && g.player2Client.isBot {
		bothRequested = true
	}

	if bothRequested {
		// Clear old game and start a fresh game with same clients
		delete(h.activeGames, g.game.ID)

		p1 := g.player1Client
		p2 := g.player2Client

		// Reset PlayAgainRequests for new game context
		g.PlayAgainRequests = nil

		// Ensure clients still exist in hub's client map; if not, create placeholders
		if p1 != nil {
			p1.gameID = ""
		}
		if p2 != nil {
			p2.gameID = ""
		}

		log.Printf("[BACKEND] Starting rematch between %v and %v", p1.username, func() string {
			if p2 != nil {
				return p2.username
			}
			return "(nil)"
		}())

		// If either side is a bot, ensure immediate rematch with a fresh bot client to avoid
		// any stale state/timers. Also ensure human clients have a send channel.
		if (p1 != nil && p1.isBot) || (p2 != nil && p2.isBot) {
			// Prefer human as player1 for rematch; if player1 is bot, swap
			var human *Client
			if p1 != nil && !p1.isBot {
				human = p1
			} else if p2 != nil && !p2.isBot {
				human = p2
			}

			// Ensure human client has a send channel
			if human != nil && human.send == nil && human.conn != nil {
				human.send = make(chan []byte, 256)
				h.clients[human] = true
				log.Printf("[BACKEND] Reinitialized send channel for human client %s", human.username)
			}

			// Create a fresh bot client
			botClient := &Client{
				hub:      h,
				conn:     nil,
				send:     nil,
				username: "AI Bot",
				isBot:    true,
			}

			// Start rematch between human and new bot client
			if human != nil {
				log.Printf("[BACKEND] Starting immediate rematch human=%s vs bot", human.username)
				h.createGame(human, botClient)
			} else {
				// No human found (both nil or both bots?) — fall back to original players
				log.Printf("[BACKEND] No human found for rematch, falling back to original clients")
				h.createGame(p1, p2)
			}
			return
		}

		// Normal rematch between two human players
		h.createGame(p1, p2)
	}
}

// handleExit handles a client's request to exit the current game.
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

	// Mark game inactive and notify the other player
	g.game.IsActive = false

	var otherClient *Client
	if g.game.Player1.ID == client.username {
		otherClient = h.findClientUnsafe(g.game.Player2.ID)
	} else {
		otherClient = h.findClientUnsafe(g.game.Player1.ID)
	}

	// Notify other player that opponent exited
	if otherClient != nil && otherClient.send != nil {
		payload := map[string]interface{}{
			"gameId":  client.gameID,
			"message": "opponentExited",
		}
		msg := GameMessage{
			Type:    "opponentExited",
			GameID:  client.gameID,
			Payload: payload,
		}
		if data, err := json.Marshal(msg); err == nil {
			select {
			case otherClient.send <- data:
			default:
			}
		}
		otherClient.gameID = ""
	}

	// Remove game and clear client's gameID
	delete(h.activeGames, client.gameID)
	client.gameID = ""
}
