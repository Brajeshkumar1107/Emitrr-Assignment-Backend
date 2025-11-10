package game

import (
	"encoding/json"
)

// GameStatus represents the current state of the game
type GameStatus string

const (
	StatusWaiting    GameStatus = "waiting"
	StatusInProgress GameStatus = "in_progress"
	StatusCompleted  GameStatus = "completed"
	StatusDraw       GameStatus = "draw"
)

// GameState represents the current state of the game for client updates
type GameState struct {
	ID          string     `json:"id"`
	Board       [][]int    `json:"board"`
	CurrentTurn int        `json:"currentTurn"`
	Status      GameStatus `json:"status"`
	Player1     *Player    `json:"player1,omitempty"`
	Player2     *Player    `json:"player2,omitempty"`
	Winner      *Player    `json:"winner,omitempty"`
	LastMove    *struct {
		Row    int `json:"row"`
		Column int `json:"column"`
		Player int `json:"player"`
	} `json:"lastMove,omitempty"`
}

// GetState returns the current game state
func (g *Game) GetState() *GameState {
	boardCopy := make([][]int, 6)
	for i := range boardCopy {
		boardCopy[i] = make([]int, 7)
		for j := range boardCopy[i] {
			boardCopy[i][j] = g.Board.Grid[i][j]
		}
	}

	state := &GameState{
		ID:          g.ID,
		Board:       boardCopy,
		CurrentTurn: g.CurrentTurn,
		Player1:     &g.Player1,
		Player2:     &g.Player2,
		LastMove: &struct {
			Row    int `json:"row"`
			Column int `json:"column"`
			Player int `json:"player"`
		}{
			Row:    g.Board.LastMove.Row,
			Column: g.Board.LastMove.Column,
			Player: g.Board.LastMove.Player,
		},
	}

	if g.IsActive {
		state.Status = StatusInProgress
	} else {
		if g.Board.IsBoardFull() {
			state.Status = StatusDraw
		} else {
			state.Status = StatusCompleted
			// Determine winner based on last move
			if g.CheckWin() {
				if g.Board.LastMove.Player == 1 {
					state.Winner = &g.Player1
				} else {
					state.Winner = &g.Player2
				}
			}
		}
	}

	return state
}

// GetBoardForBot returns a copy of the current board state for bot calculations
func (g *Game) GetBoardForBot() [][]int {
	board := make([][]int, 6)
	for i := range board {
		board[i] = make([]int, 7)
		copy(board[i], g.Board.Grid[i][:])
	}
	return board
}

// IsGameOver checks if the game has ended
func (g *Game) IsGameOver() bool {
	return g.CheckWin() || g.Board.IsBoardFull()
}

// SwitchTurn changes the current player
func (g *Game) SwitchTurn() {
	g.CurrentTurn = 3 - g.CurrentTurn // Switches between 1 and 2
}

// ToJSON converts the game state to JSON
func (g *Game) ToJSON() ([]byte, error) {
	return json.Marshal(g.GetState())
}
