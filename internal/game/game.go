package game

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/google/uuid"
	"github.com/connect4/backend/internal/database"
)

// Player represents a player in the game
type Player struct {
	ID       string
	Username string
	IsBot    bool
}

// Board represents the game board
type Board struct {
	Grid     [6][7]int // 0 = empty, 1 = player 1, 2 = player 2
	Columns  int
	Rows     int
	LastMove struct {
		Row    int `json:"row"`
		Column int `json:"column"`
		Player int `json:"player"`
	}
}

// Game represents an active game session
type Game struct {
	ID           string
	Board        Board
	Player1      Player
	Player2      Player
	CurrentTurn  int
	IsActive     bool
	StartTime    int64
	LastMoveTime int64
	DB           *database.DB
	DBGameID     int // ID of this game record in DB
}

// NewGame creates a new game instance and inserts it into the database
func NewGame(db *database.DB, player1, player2 Player) (*Game, error) {
	g := &Game{
		ID: uuid.New().String(),
		Board: Board{
			Columns: 7,
			Rows:    6,
		},
		Player1:     player1,
		Player2:     player2,
		CurrentTurn: 1,
		IsActive:    true,
		StartTime:   time.Now().Unix(),
		DB:          db,
	}

	// Create DB record if DB is provided
	if db != nil {
		ctx := context.Background()

		// Ensure both players exist in DB
		player1Entity, err := db.GetPlayer(ctx, player1.Username)
		if err != nil {
			log.Printf("[DB] Error fetching player1: %v", err)
		}
		if player1Entity == nil {
			player1Entity, err = db.CreatePlayer(ctx, player1.Username)
			if err != nil {
				log.Printf("[DB] Error creating player1: %v", err)
			}
		}

		var player2Entity *database.Player
		if !player2.IsBot {
			player2Entity, err = db.GetPlayer(ctx, player2.Username)
			if err != nil {
				log.Printf("[DB] Error fetching player2: %v", err)
			}
			if player2Entity == nil {
				player2Entity, err = db.CreatePlayer(ctx, player2.Username)
				if err != nil {
					log.Printf("[DB] Error creating player2: %v", err)
				}
			}
		}

		var p2ID *int
		if player2Entity != nil {
			p2ID = &player2Entity.ID
		}

		gameRecord, err := db.CreateGame(ctx, player1Entity.ID, p2ID, player2.IsBot)
		if err != nil {
			log.Printf("DB error creating game record: %v", err)
		} else {
			g.DBGameID = gameRecord.ID
			log.Printf("[DB] Game record created with ID=%d (isBotGame=%v)", g.DBGameID, player2.IsBot)
		}
	}

	return g, nil
}

// MakeMove attempts to make a move in the specified column
func (g *Game) MakeMove(column int) error {
	if !g.Board.IsValidMove(column) {
		return fmt.Errorf("invalid move: column %d is full or out of bounds", column)
	}

	row := 5 // Start from bottom
	for row >= 0 && g.Board.Grid[row][column] != 0 {
		row--
	}

	g.Board.Grid[row][column] = g.CurrentTurn
	g.Board.LastMove.Row = row
	g.Board.LastMove.Column = column
	g.Board.LastMove.Player = g.CurrentTurn

	g.LastMoveTime = time.Now().Unix()
	g.CurrentTurn = 3 - g.CurrentTurn // Switch between 1 and 2

	// Check for win or draw after every move
	g.CheckGameCompletion()

	return nil
}

// CheckGameCompletion checks if game ended (win or draw) and saves to DB
func (g *Game) CheckGameCompletion() {
	if g.CheckWin() {
		g.IsActive = false
		winner := g.Board.LastMove.Player
		log.Printf("[GAME] Player %d wins! (GameID=%s)", winner, g.ID)
		g.saveGameResult(winner)
	} else if g.Board.IsBoardFull() {
		g.IsActive = false
		log.Printf("[GAME] Game %s ended in a draw", g.ID)
		g.saveGameResult(0)
	}
}

// saveGameResult writes the result of the game into the database
func (g *Game) saveGameResult(winner int) {
	if g.DB == nil || g.DBGameID == 0 {
		return
	}

	ctx := context.Background()

	var winnerID int
	if winner == 1 {
		if playerEntity, err := g.DB.GetPlayer(ctx, g.Player1.Username); err == nil && playerEntity != nil {
			winnerID = playerEntity.ID
		}
	} else if winner == 2 && !g.Player2.IsBot {
		if playerEntity, err := g.DB.GetPlayer(ctx, g.Player2.Username); err == nil && playerEntity != nil {
			winnerID = playerEntity.ID
		}
	}

	// Convert game state
	gameState := map[string]interface{}{
		"id":        g.ID,
		"grid":      g.Board.Grid,
		"lastMove":  g.Board.LastMove,
		"isActive":  g.IsActive,
		"startTime": g.StartTime,
	}

	go func() {
		err := g.DB.UpdateGameResult(ctx, g.DBGameID, winnerID, gameState)
		if err != nil {
			log.Printf("[DB] ❌ Error saving game result (GameID=%d): %v", g.DBGameID, err)
		} else {
			log.Printf("[DB] ✅ Game result saved successfully (GameID=%d, WinnerID=%v)", g.DBGameID, winnerID)
		}
	}()
}

// CheckWin checks if the last move resulted in a win
func (g *Game) CheckWin() bool {
	row := g.Board.LastMove.Row
	col := g.Board.LastMove.Column
	player := g.Board.LastMove.Player

	// Horizontal
	for c := 0; c <= 3; c++ {
		count := 0
		for i := 0; i < 4; i++ {
			if col-c+i >= 0 && col-c+i < 7 && g.Board.Grid[row][col-c+i] == player {
				count++
			}
		}
		if count == 4 {
			return true
		}
	}

	// Vertical
	for r := 0; r <= 3; r++ {
		count := 0
		for i := 0; i < 4; i++ {
			if row-r+i >= 0 && row-r+i < 6 && g.Board.Grid[row-r+i][col] == player {
				count++
			}
		}
		if count == 4 {
			return true
		}
	}

	// Diagonal (top-left → bottom-right)
	for i := -3; i <= 0; i++ {
		count := 0
		for j := 0; j < 4; j++ {
			r := row + i + j
			c := col + i + j
			if r >= 0 && r < 6 && c >= 0 && c < 7 && g.Board.Grid[r][c] == player {
				count++
			}
		}
		if count == 4 {
			return true
		}
	}

	// Diagonal (top-right → bottom-left)
	for i := -3; i <= 0; i++ {
		count := 0
		for j := 0; j < 4; j++ {
			r := row + i + j
			c := col - i - j
			if r >= 0 && r < 6 && c >= 0 && c < 7 && g.Board.Grid[r][c] == player {
				count++
			}
		}
		if count == 4 {
			return true
		}
	}

	return false
}

// IsBoardFull checks if the board is completely filled
func (b *Board) IsBoardFull() bool {
	for col := 0; col < 7; col++ {
		if b.Grid[0][col] == 0 {
			return false
		}
	}
	return true
}

// IsValidMove checks if a move can be made in the specified column
func (b *Board) IsValidMove(column int) bool {
	if column < 0 || column >= 7 {
		return false
	}
	return b.Grid[0][column] == 0
}
