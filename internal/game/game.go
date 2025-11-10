package game

import (
	"fmt"
	"time"

	"github.com/google/uuid"
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
}

// NewGame creates a new game instance
func NewGame(player1, player2 Player) *Game {
	return &Game{
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
	}
}

// MakeMove attempts to make a move in the specified column
func (g *Game) MakeMove(column int) error {
	if !g.Board.IsValidMove(column) {
		return fmt.Errorf("invalid move: column %d is full or out of bounds", column)
	}

	// Find the lowest empty position in the column
	row := 5 // Start from bottom
	for row >= 0 && g.Board.Grid[row][column] != 0 {
		row--
	}

	// Make the move
	g.Board.Grid[row][column] = g.CurrentTurn
	g.Board.LastMove.Row = row
	g.Board.LastMove.Column = column
	g.Board.LastMove.Player = g.CurrentTurn

	// Update game state
	g.LastMoveTime = time.Now().Unix()
	g.CurrentTurn = 3 - g.CurrentTurn // Switch between 1 and 2

	return nil
}

// CheckWin checks if the last move resulted in a win
func (g *Game) CheckWin() bool {
	row := g.Board.LastMove.Row
	col := g.Board.LastMove.Column
	player := g.Board.LastMove.Player

	// Check horizontal
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

	// Check vertical
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

	// Check diagonal (top-left to bottom-right)
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

	// Check diagonal (top-right to bottom-left)
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
func (g *Board) IsBoardFull() bool {
	for col := 0; col < 7; col++ {
		if g.Grid[0][col] == 0 { // Check top row
			return false
		}
	}
	return true
}

// IsValidMove checks if a move can be made in the specified column
func (g *Board) IsValidMove(column int) bool {
	// Check if column is within bounds
	if column < 0 || column >= 7 {
		return false
	}

	// Check if column is not full (top position is empty)
	return g.Grid[0][column] == 0
}
