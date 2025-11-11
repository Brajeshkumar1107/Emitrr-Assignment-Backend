package ws

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

const (
	// Time allowed to write a message to the peer.
	writeWait = 10 * time.Second

	// Time allowed to read the next pong message from the peer.
	pongWait = 60 * time.Second

	// Send pings to peer with this period. Must be less than pongWait.
	pingPeriod = (pongWait * 9) / 10

	// Maximum message size allowed from peer.
	maxMessageSize = 512
)

// ServeWs handles WebSocket connections for a client
func ServeWs(hub *Hub, w http.ResponseWriter, r *http.Request) {
	origin := r.Header.Get("Origin")
	log.Printf("[BACKEND-1] ServeWs: New WebSocket connection request from origin: %s", origin)
	
	upgrader := websocket.Upgrader{
		ReadBufferSize:  1024,
		WriteBufferSize: 1024,
		CheckOrigin: func(r *http.Request) bool {
			origin := r.Header.Get("Origin")
			log.Printf("[BACKEND-WS-CHECK] Checking origin: %s", origin)

			// Allowed origins for production and development
			allowedOrigins := []string{
				"https://emitrr-assignment-frontend-9xvs.vercel.app",
				"http://localhost:3000",
				"http://localhost:5173",
				"http://localhost:8080",
				"http://127.0.0.1:3000",
				"http://127.0.0.1:5173",
				"http://127.0.0.1:8080",
			}

			// Check against allowed list
			for _, allowed := range allowedOrigins {
				if origin == allowed {
					log.Printf("[BACKEND-WS-CHECK] ✓ Origin ALLOWED: %s", origin)
					return true
				}
			}

			// Check environment variable for custom allowed origins
			allowedOriginsEnv := os.Getenv("ALLOWED_ORIGINS")
			if allowedOriginsEnv != "" {
				envOrigins := strings.Split(allowedOriginsEnv, ",")
				for _, allowed := range envOrigins {
					allowed = strings.TrimSpace(allowed)
					if origin == allowed {
						log.Printf("[BACKEND-WS-CHECK] ✓ Origin ALLOWED (from env): %s", origin)
						return true
					}
				}
			}

			log.Printf("[BACKEND-WS-CHECK] ✗ Origin REJECTED: %s (not in allowed list)", origin)
			return false
		},
	}

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("[BACKEND-1] ServeWs: WebSocket upgrade failed: %v", err)
		return
	}

	log.Printf("[BACKEND-2] ServeWs: WebSocket connection upgraded successfully from %s, url=%s, ua=%s, protocol=%s",
		r.RemoteAddr, r.URL.String(), r.Header.Get("User-Agent"), r.Header.Get("Sec-Websocket-Protocol"))
	client := &Client{
		hub:  hub,
		conn: conn,
		send: make(chan []byte, 256),
	}

	log.Printf("[BACKEND-3] ServeWs: Registering client in hub")
	// Register client
	client.hub.register <- client

	log.Printf("[BACKEND-4] ServeWs: Starting readPump and writePump goroutines")
	// Start goroutines for reading and writing messages
	go client.writePump()
	go client.readPump()
}

// readPump pumps messages from the WebSocket connection to the hub.
func (c *Client) readPump() {
	log.Printf("[BACKEND-5] Client.readPump: Starting read pump for client")
	defer func() {
		log.Printf("[BACKEND-5] Client.readPump: Client disconnecting, unregistering")
		c.hub.unregister <- c
		c.conn.Close()
	}()

	c.conn.SetReadLimit(maxMessageSize)
	c.conn.SetReadDeadline(time.Now().Add(pongWait))
	c.conn.SetPongHandler(func(string) error {
		c.conn.SetReadDeadline(time.Now().Add(pongWait))
		return nil
	})

	for {
		_, message, err := c.conn.ReadMessage()
		if err != nil {
			// Treat normal/expected close codes (including NoStatusReceived 1005 which
			// browsers sometimes send on quick refresh/unload) as informational so we
			// don't spam the logs. Only log unexpected close errors as errors.
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure, websocket.CloseNoStatusReceived) {
				log.Printf("[BACKEND-5] Client.readPump: WebSocket error: %v", err)
			} else {
				// Informational close (normal navigation/refresh/unload)
				log.Printf("[BACKEND-5] Client.readPump: WebSocket closed: %v", err)
			}
			break
		}

		log.Printf("[BACKEND-6] Client.readPump: Received raw message: %s", string(message))
		var msg Message
		if err := json.Unmarshal(message, &msg); err != nil {
			log.Printf("[BACKEND-6] Client.readPump: Error unmarshaling message: %v", err)
			continue
		}

		log.Printf("[BACKEND-7] Client.readPump: Parsed message type=%s, payload=%v", msg.Type, msg.Payload)
		// Handle different message types
		switch msg.Type {
		case "join":
			log.Printf("[BACKEND-8] Client.readPump: Processing 'join' message")
			// Handle both string (old format) and object (new format with gameMode)
			if usernameStr, ok := msg.Payload.(string); ok {
				// Old format - just username
				log.Printf("[BACKEND-8] Client.readPump: Old format join (string), username=%s", usernameStr)
				c.username = usernameStr
				c.hub.handleNewPlayer(c, "friend") // Default to friend mode for backward compatibility
			} else if payloadObj, ok := msg.Payload.(map[string]interface{}); ok {
				// New format - object with username and gameMode
				log.Printf("[BACKEND-8] Client.readPump: New format join (object), payload=%v", payloadObj)
				if username, ok := payloadObj["username"].(string); ok {
					c.username = username
					gameMode := "friend" // default
					if mode, ok := payloadObj["gameMode"].(string); ok {
						gameMode = mode
					}
					log.Printf("[BACKEND-9] Client.readPump: Player %s joining with mode: %s", username, gameMode)
					c.hub.handleNewPlayer(c, gameMode)
				} else {
					log.Printf("[BACKEND-8] Client.readPump: Error: username not found in join payload")
				}
			} else {
				log.Printf("[BACKEND-8] Client.readPump: Error: invalid join payload format")
			}
		case "move":
			log.Printf("[BACKEND-8] Client.readPump: Processing 'move' message")
			if move, ok := msg.Payload.(map[string]interface{}); ok {
				if column, ok := move["column"].(float64); ok {
					log.Printf("[BACKEND-9] Client.readPump: Player %s making move in column %d", c.username, int(column))
					c.hub.handleMove(c, int(column))
				}
			}
		case "cancelWaiting":
			log.Printf("[BACKEND-8] Client.readPump: Processing 'cancelWaiting' message from %s", c.username)
			c.hub.mu.Lock()
			if c.hub.waitingPlayer == c {
				c.hub.waitingPlayer = nil
				// Send cancelled message back
				msg := Message{
					Type: "waitingCancelled",
					Payload: map[string]interface{}{
						"message": "Waiting cancelled",
					},
				}
				if data, err := json.Marshal(msg); err == nil {
					c.send <- data
				}
			}
			c.hub.mu.Unlock()

		case "playAgain":
			log.Printf("[BACKEND-8] Client.readPump: Processing 'playAgain' from %s", c.username)
			c.hub.handlePlayAgain(c)

		case "exitGame":
			log.Printf("[BACKEND-8] Client.readPump: Processing 'exitGame' from %s", c.username)
			c.hub.handleExit(c)
		}
	}
}

// writePump pumps messages from the hub to the WebSocket connection.
func (c *Client) writePump() {
	ticker := time.NewTicker(pingPeriod)
	defer func() {
		ticker.Stop()
		c.conn.Close()
	}()

	for {
		select {
		case message, ok := <-c.send:
			c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if !ok {
				c.conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}

			w, err := c.conn.NextWriter(websocket.TextMessage)
			if err != nil {
				return
			}
			w.Write(message)

			if err := w.Close(); err != nil {
				return
			}

		case <-ticker.C:
			c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}
