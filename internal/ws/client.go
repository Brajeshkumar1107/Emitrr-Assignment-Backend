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
	writeWait      = 10 * time.Second  // Time allowed to write a message to the peer.
	pongWait       = 60 * time.Second  // Time allowed to read the next pong message from the peer.
	pingPeriod     = (pongWait * 9) / 10 // Send pings to peer with this period. Must be less than pongWait.
	maxMessageSize = 512               // Maximum message size allowed from peer.
)

// ServeWs handles WebSocket connection requests and upgrades them
func ServeWs(hub *Hub, w http.ResponseWriter, r *http.Request) {
	origin := r.Header.Get("Origin")
	log.Printf("[BACKEND-1] ServeWs: New WebSocket connection request from origin: %s", origin)

	upgrader := websocket.Upgrader{
		ReadBufferSize:  1024,
		WriteBufferSize: 1024,

		CheckOrigin: func(r *http.Request) bool {
			origin := r.Header.Get("Origin")
			log.Printf("[BACKEND-WS-CHECK] Checking origin: %s", origin)

			// ✅ Environment variables for flexible config
			if os.Getenv("ALLOW_ALL_ORIGINS") == "true" {
				log.Printf("[BACKEND-WS-CHECK] ⚠️ ALLOW_ALL_ORIGINS enabled — accepting all origins (for testing only!)")
				return true
			}

			// ✅ Default allowed origins
			allowedOrigins := []string{
				"https://emitrr-assignment-frontend.vercel.app",   // ✅ Current production frontend
				"https://emitrr-assignment-frontend-9xvs.vercel.app", // Optional old Vercel deploy
				"http://localhost:3000", // Local React dev server
				"http://127.0.0.1:3000",
				"http://localhost:5173",
				"http://127.0.0.1:5173",
			}

			// ✅ Check hardcoded list
			for _, allowed := range allowedOrigins {
				if origin == allowed {
					log.Printf("[BACKEND-WS-CHECK] ✓ Origin ALLOWED: %s", origin)
					return true
				}
			}

			// ✅ Check dynamic environment variable (comma-separated)
			if envOrigins := os.Getenv("ALLOWED_ORIGINS"); envOrigins != "" {
				for _, allowed := range strings.Split(envOrigins, ",") {
					if strings.TrimSpace(allowed) == origin {
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
		// Detailed logging to help diagnose upgrade failures (useful on hosted platforms)
		log.Printf("[BACKEND-1] ServeWs: WebSocket upgrade failed: %v | remote=%s host=%s url=%s method=%s", err, r.RemoteAddr, r.Host, r.URL.String(), r.Method)
		// Log headers to capture Origin / Sec-WebSocket-Extensions / Sec-WebSocket-Key etc.
		for name, values := range r.Header {
			log.Printf("[BACKEND-1] ServeWs: Header %s: %v", name, values)
		}
		if r.TLS != nil {
			log.Printf("[BACKEND-1] ServeWs: TLS connection state present (HandshakeComplete=%v)", r.TLS.HandshakeComplete)
		} else {
			log.Printf("[BACKEND-1] ServeWs: TLS is nil (non-TLS connection)")
		}
		return
	}

	log.Printf("[BACKEND-2] ServeWs: WebSocket connection upgraded successfully from %s", r.RemoteAddr)

	client := &Client{
		hub:  hub,
		conn: conn,
		send: make(chan []byte, 256),
	}

	client.hub.register <- client
	go client.writePump()
	go client.readPump()
}

// readPump continuously reads messages from the WebSocket connection
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
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure, websocket.CloseNoStatusReceived) {
				log.Printf("[BACKEND-5] Client.readPump: WebSocket error: %v", err)
			} else {
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

		switch msg.Type {
		case "join":
			log.Printf("[BACKEND-8] Client.readPump: Processing 'join' message")
			if usernameStr, ok := msg.Payload.(string); ok {
				c.username = usernameStr
				c.hub.handleNewPlayer(c, "friend") // default mode
			} else if payloadObj, ok := msg.Payload.(map[string]interface{}); ok {
				if username, ok := payloadObj["username"].(string); ok {
					c.username = username
					gameMode := "friend"
					if mode, ok := payloadObj["gameMode"].(string); ok {
						gameMode = mode
					}
					log.Printf("[BACKEND-9] Client.readPump: Player %s joining with mode: %s", username, gameMode)
					c.hub.handleNewPlayer(c, gameMode)
				}
			}

		case "move":
			if move, ok := msg.Payload.(map[string]interface{}); ok {
				if column, ok := move["column"].(float64); ok {
					log.Printf("[BACKEND-9] Client.readPump: Player %s move column %d", c.username, int(column))
					c.hub.handleMove(c, int(column))
				}
			}

		case "cancelWaiting":
			c.hub.mu.Lock()
			if c.hub.waitingPlayer == c {
				c.hub.waitingPlayer = nil
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
			c.hub.handlePlayAgain(c)

		case "exitGame":
			c.hub.handleExit(c)
		}
	}
}

// writePump continuously writes messages from the hub to the WebSocket connection
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
