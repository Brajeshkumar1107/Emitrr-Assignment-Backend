package ws

import (
	"context"
	"encoding/json"
	"log"
	"sync"
	"time"

	"github.com/connect4/backend/internal/bot"
	"github.com/connect4/backend/internal/database"
	"github.com/connect4/backend/internal/game"
)

// WSGame represents the websocket wrapper around a game session
type WSGame struct {
	game          *game.Game
	hub           *Hub
	player1Client *Client
	player2Client *Client
	mu            sync.Mutex // Mutex for thread-safe operations
	// Track play-again requests (usernames)
	PlayAgainRequests []string
}

func (g *WSGame) ToGameState() *game.GameState {
	return g.game.GetState()
}

func (g *WSGame) CheckWinner() int {
	if g.game.CheckWin() {
		return g.game.Board.LastMove.Player
	}
	return 0
}

// GameMessage represents a game-related message
type GameMessage struct {
	Type    string      `json:"type"`
	GameID  string      `json:"gameId"`
	Payload interface{} `json:"payload"`
}

// handleMove processes a player's move
func (h *Hub) handleMove(client *Client, column int) {
	h.mu.Lock()
	defer h.mu.Unlock()

	g, exists := h.activeGames[client.gameID]
	if !exists {
		return
	}

	// Verify it's the player's turn
	// Determine if it's the client's turn
	var isPlayer1 = g.game.Player1.ID == client.username
	var isPlayer2 = g.game.Player2.ID == client.username

	if !isPlayer1 && !isPlayer2 {
		return
	}

	if (g.game.CurrentTurn != 1 && isPlayer1) || (g.game.CurrentTurn != 2 && isPlayer2) {
		return
	}

	// Make the move
	if err := g.game.MakeMove(column); err != nil {
		// Send error message to client
		msg := GameMessage{
			Type:    "error",
			GameID:  g.game.ID,
			Payload: err.Error(),
		}
		if data, err := json.Marshal(msg); err == nil {
			client.send <- data
		}
		return
	}

	// Check for game end conditions
	if g.game.CheckWin() {
		g.game.IsActive = false
		// Store game result (if database is available)
		h.storeGameResult(g, g.game.Board.LastMove.Player, false)
	} else if g.game.Board.IsBoardFull() {
		g.game.IsActive = false
		// Store game result as draw (if database is available)
		h.storeGameResult(g, 0, true)
	}

	// Broadcast game state to both players
	state := g.ToGameState()
	msg := GameMessage{
		Type:    "gameState",
		GameID:  g.game.ID,
		Payload: state,
	}

	if data, err := json.Marshal(msg); err == nil {
		// We already hold h.mu; use findClientUnsafe to avoid deadlock
		if p1Client := h.findClientUnsafe(g.game.Player1.ID); p1Client != nil {
			select {
			case p1Client.send <- data:
			default:
			}
		}
		if p2Client := h.findClientUnsafe(g.game.Player2.ID); p2Client != nil {
			select {
			case p2Client.send <- data:
			default:
			}
		}
	}

	// If the game finished, send a dedicated gameFinished message so frontends can
	// show a popup with Play Again / Exit options.
	if !g.game.IsActive {
		isDraw := g.game.Board.IsBoardFull() && !g.game.CheckWin()
		var winnerUsername interface{} = nil
		botWon := false
		if g.game.CheckWin() {
			if g.game.Board.LastMove.Player == 1 {
				winnerUsername = g.game.Player1.Username
			} else if g.game.Board.LastMove.Player == 2 {
				if !g.game.Player2.IsBot {
					winnerUsername = g.game.Player2.Username
				} else {
					// Bot won
					botWon = true
				}
			}
		}

		finishPayload := map[string]interface{}{
			"gameId": g.game.ID,
			"isDraw": isDraw,
			"winner": winnerUsername,
			"botWon": botWon,
		}

		finishMsg := GameMessage{
			Type:    "gameFinished",
			GameID:  g.game.ID,
			Payload: finishPayload,
		}
		if data, err := json.Marshal(finishMsg); err == nil {
			if p1Client := h.findClientUnsafe(g.game.Player1.ID); p1Client != nil && p1Client.send != nil {
				select {
				case p1Client.send <- data:
				default:
				}
			}
			if p2Client := h.findClientUnsafe(g.game.Player2.ID); p2Client != nil && p2Client.send != nil {
				select {
				case p2Client.send <- data:
				default:
				}
			}
		}
	}

	// If playing against bot, trigger bot move
	if g.game.IsActive && g.game.Player2.IsBot && g.game.CurrentTurn == 2 {
		go h.makeBotMove(g)
	}
}

// GetBoardForBot creates a 2D slice representation of the board for the bot
func (g *WSGame) GetBoardForBot() [][]int {
	return g.game.GetBoardForBot()
}

// makeBotMove handles the bot's turn
func (h *Hub) makeBotMove(wsGame *WSGame) {
	botPlayer := bot.NewBot()
	board := wsGame.game.GetBoardForBot()
	column := botPlayer.CalculateNextMove(board)

	// Small delay to simulate "thinking"
	time.Sleep(500 * time.Millisecond)

	h.mu.Lock()
	defer h.mu.Unlock()

	// Verify game still exists and is active
	if _, exists := h.activeGames[wsGame.game.ID]; !exists {
		return
	}

	if !wsGame.game.IsActive {
		return
	}

	if err := wsGame.game.MakeMove(column); err != nil {
		log.Printf("Bot move error: %v", err)
		return
	}

	// Check for game end conditions
	if wsGame.game.CheckWin() {
		wsGame.game.IsActive = false
		// Store game result (if database is available)
		h.storeGameResult(wsGame, wsGame.game.Board.LastMove.Player, false)
	} else if wsGame.game.Board.IsBoardFull() {
		wsGame.game.IsActive = false
		// Store game result as draw (if database is available)
		h.storeGameResult(wsGame, 0, true)
	}

	// Broadcast updated game state
	state := wsGame.ToGameState()
	msg := GameMessage{
		Type:    "gameState",
		GameID:  wsGame.game.ID,
		Payload: state,
	}

	if data, err := json.Marshal(msg); err == nil {
		// h.mu is held here; use unsafe lookup to send without re-locking
		if p1Client := h.findClientUnsafe(wsGame.game.Player1.ID); p1Client != nil {
			select {
			case p1Client.send <- data:
			default:
			}
		}
		if p2Client := h.findClientUnsafe(wsGame.game.Player2.ID); p2Client != nil {
			select {
			case p2Client.send <- data:
			default:
			}
		}
	}

	// If the game finished after the bot move, send gameFinished message
	if !wsGame.game.IsActive {
		isDraw := wsGame.game.Board.IsBoardFull() && !wsGame.game.CheckWin()
		var winnerUsername interface{} = nil
		botWon := false
		if wsGame.game.CheckWin() {
			if wsGame.game.Board.LastMove.Player == 1 {
				winnerUsername = wsGame.game.Player1.Username
			} else if wsGame.game.Board.LastMove.Player == 2 {
				if !wsGame.game.Player2.IsBot {
					winnerUsername = wsGame.game.Player2.Username
				} else {
					botWon = true
				}
			}
		}

		finishPayload := map[string]interface{}{
			"gameId": wsGame.game.ID,
			"isDraw": isDraw,
			"winner": winnerUsername,
			"botWon": botWon,
		}

		finishMsg := GameMessage{
			Type:    "gameFinished",
			GameID:  wsGame.game.ID,
			Payload: finishPayload,
		}
		if data, err := json.Marshal(finishMsg); err == nil {
			if p1Client := h.findClientUnsafe(wsGame.game.Player1.ID); p1Client != nil && p1Client.send != nil {
				select {
				case p1Client.send <- data:
				default:
				}
			}
			if p2Client := h.findClientUnsafe(wsGame.game.Player2.ID); p2Client != nil && p2Client.send != nil {
				select {
				case p2Client.send <- data:
				default:
				}
			}
		}
	}
}

// findClient finds a client by username
func (h *Hub) findClient(username string) *Client {
	h.mu.Lock()
	defer h.mu.Unlock()

	for client := range h.clients {
		if client.username == username {
			return client
		}
	}
	return nil
}

// storeGameResult stores game result to database if available
// Note: This requires database integration with user management to map
// string usernames to integer player IDs. Currently a placeholder.
func (h *Hub) storeGameResult(g *WSGame, winner int, isDraw bool) {
	if h.db != nil {
		ctx := context.Background()
		// 1. Get or create players by username
		p1, err := h.db.GetPlayer(ctx, g.game.Player1.Username)
		if err != nil {
			log.Printf("DB error getting player1: %v", err)
			return
		}
		if p1 == nil {
			p1, err = h.db.CreatePlayer(ctx, g.game.Player1.Username)
			if err != nil {
				log.Printf("DB error creating player1: %v", err)
				return
			}
		}
		var p2 *database.Player
		var p2IDPtr *int
		if !g.game.Player2.IsBot {
			p2, err = h.db.GetPlayer(ctx, g.game.Player2.Username)
			if err != nil {
				log.Printf("DB error getting player2: %v", err)
				return
			}
			if p2 == nil {
				p2, err = h.db.CreatePlayer(ctx, g.game.Player2.Username)
				if err != nil {
					log.Printf("DB error creating player2: %v", err)
					return
				}
			}
			p2IDPtr = &p2.ID
		} else {
			// Bot game: do not create a DB record for the bot; leave player2_id NULL
			p2IDPtr = nil
		}

		// 2. Create game record (player2_id may be NULL for bot games)
		gameRec, err := h.db.CreateGame(ctx, p1.ID, p2IDPtr, g.game.Player2.IsBot)
		if err != nil {
			log.Printf("DB error creating game record: %v", err)
			return
		}

		// 3. Update game result and player statistics
		// Determine winnerID to pass into DB. For bot games we do NOT create a DB record
		// for the bot; therefore if the bot wins we set winnerID = 0 (NULL in DB).
		var winnerID int
		if isDraw {
			winnerID = 0 // No winner
		} else if winner == 1 {
			winnerID = p1.ID
		} else if winner == 2 {
			if p2 != nil {
				winnerID = p2.ID
			} else {
				// Bot won - do not record bot as player in DB
				winnerID = 0
			}
		}
		// Convert *GameState to map[string]interface{}
		var gameStateMap map[string]interface{}
		gameState := g.game.GetState()
		data, err := json.Marshal(gameState)
		if err != nil {
			log.Printf("DB error marshaling game state: %v", err)
			return
		}
		if err := json.Unmarshal(data, &gameStateMap); err != nil {
			log.Printf("DB error unmarshaling game state: %v", err)
			return
		}
		err = h.db.UpdateGameResult(ctx, gameRec.ID, winnerID, gameStateMap)
		if err != nil {
			log.Printf("DB error updating game result: %v", err)
			return
		}

		log.Printf("Game saved: ID=%d, WinnerID=%d, IsDraw=%v", gameRec.ID, winnerID, isDraw)
	}

	// Broadcast a leaderboardUpdate message to all connected clients so frontends
	// can refresh their leaderboard view. We include minimal details (winner username,
	// isDraw, gameId). To avoid frontend optimistically adding bot users to the
	// leaderboard, only include the winner username when the winner is a human.
	payload := map[string]interface{}{
		"gameId": g.game.ID,
		"isDraw": isDraw,
	}
	if winner == 1 {
		payload["winner"] = g.game.Player1.Username
	} else if winner == 2 {
		// Only include winner username if player2 is not a bot
		if !g.game.Player2.IsBot {
			payload["winner"] = g.game.Player2.Username
		} else {
			// Bot won - do not include winner to prevent optimistic frontend insert
			payload["winner"] = nil
			payload["botWon"] = true
		}
	} else {
		payload["winner"] = nil
	}

	msg := map[string]interface{}{
		"type":    "leaderboardUpdate",
		"payload": payload,
	}

	if data, err := json.Marshal(msg); err == nil {
		log.Printf("[BACKEND-STORE] Broadcasting leaderboardUpdate: game=%s, winner=%v, isDraw=%v", g.game.ID, payload["winner"], isDraw)
		// Iterate over connected clients and send if they have a send channel
		for client := range h.clients {
			if client.send == nil {
				continue
			}
			select {
			case client.send <- data:
			default:
				// If client's send channel is full, skip to avoid blocking
			}
		}
	} else {
		log.Printf("[BACKEND-STORE] Failed to marshal leaderboardUpdate: %v", err)
	}
}

// createGame creates a new game between two players
func (h *Hub) createGame(player1, player2 *Client) {
	log.Printf("[BACKEND-14] Hub.createGame: Creating game between player1=%s, player2=%s (isBot=%v)", player1.username, player2.username, player2.isBot)

	g := game.NewGame(
		game.Player{ID: player1.username, Username: player1.username},
		game.Player{ID: player2.username, Username: player2.username, IsBot: player2.isBot},
	)

	log.Printf("[BACKEND-15] Hub.createGame: Game created with ID=%s, CurrentTurn=%d", g.ID, g.CurrentTurn)
	player1.gameID = g.ID
	player2.gameID = g.ID

	wsGame := &WSGame{
		game:          g,
		hub:           h,
		player1Client: player1,
		player2Client: player2,
	}
	h.activeGames[g.ID] = wsGame
	log.Printf("[BACKEND-16] Hub.createGame: Game added to activeGames, total active games: %d", len(h.activeGames))

	// Send initial game state to both players
	state := g.GetState()
	log.Printf("[BACKEND-17] Hub.createGame: Game state retrieved, status=%s, player1=%s, player2=%s", state.Status, state.Player1.Username, state.Player2.Username)

	msg := GameMessage{
		Type:    "gameStart",
		GameID:  g.ID,
		Payload: state,
	}

	if data, err := json.Marshal(msg); err == nil {
		log.Printf("[BACKEND-18] Hub.createGame: GameStart message marshaled, size=%d bytes", len(data))
		// Send to player1 if they have a send channel
		if player1.send != nil {
			log.Printf("[BACKEND-19] Hub.createGame: Sending gameStart to player1=%s", player1.username)
			select {
			case player1.send <- data:
				log.Printf("[BACKEND-20] Hub.createGame: gameStart message sent to player1=%s", player1.username)
			default:
				log.Printf("[BACKEND-19] Hub.createGame: Failed to send game start to player1: %s (channel full)", player1.username)
			}
		} else {
			log.Printf("[BACKEND-19] Hub.createGame: player1=%s has no send channel", player1.username)
		}
		// Send to player2 only if they're not a bot (bots don't have send channels)
		if player2 != nil && !player2.isBot {
			if player2.send != nil {
				log.Printf("[BACKEND-19] Hub.createGame: Sending gameStart to player2=%s", player2.username)
				select {
				case player2.send <- data:
					log.Printf("[BACKEND-20] Hub.createGame: gameStart message sent to player2=%s", player2.username)
				default:
					log.Printf("[BACKEND-19] Hub.createGame: Failed to send game start to player2: %s (channel full)", player2.username)
				}
			} else {
				log.Printf("[BACKEND-19] Hub.createGame: player2=%s has no send channel (connection missing)", player2.username)
			}
		} else {
			log.Printf("[BACKEND-19] Hub.createGame: player2=%s is a bot or nil (no send channel)", player2.username)
		}
	} else {
		log.Printf("[BACKEND-18] Hub.createGame: Error marshaling gameStart message: %v", err)
	}
}
