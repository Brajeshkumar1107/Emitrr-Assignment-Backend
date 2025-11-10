package bot

import (
	"math"
)

// Bot represents the AI opponent
type Bot struct {
	ID       string
	Username string
}

const (
	maxDepth    = 6
	winScore    = 1000000
	loseScore   = -1000000
	botPlayer   = 2
	humanPlayer = 1
)

// NewBot creates a new bot instance
func NewBot() *Bot {
	return &Bot{
		Username: "AI Bot",
	}
}

// CalculateNextMove determines the best move for the bot
func (b *Bot) CalculateNextMove(board [][]int) int {
	// First check for immediate winning move
	if move := checkWinningMove(board, botPlayer); move != -1 {
		return move
	}

	// Then check if we need to block opponent's winning move
	if move := checkBlockingMove(board, humanPlayer); move != -1 {
		return move
	}

	// If no immediate wins/blocks, use minimax
	bestScore := math.Inf(-1)
	bestMove := 3 // Default to middle column
	alpha := math.Inf(-1)
	beta := math.Inf(1)

	for col := 0; col < 7; col++ {
		if isValidMove(board, col) {
			tempBoard := copyBoard(board)
			makeMove(tempBoard, col, botPlayer)
			
			score := minimax(tempBoard, maxDepth, alpha, beta, false)
			
			if score > bestScore {
				bestScore = score
				bestMove = col
			}
			alpha = math.Max(alpha, score)
		}
	}

	return bestMove
}

// minimax implements the minimax algorithm with alpha-beta pruning
func minimax(board [][]int, depth int, alpha, beta float64, maximizing bool) float64 {
	// Check terminal conditions
	if isWinningBoard(board, botPlayer) {
		return winScore
	}
	if isWinningBoard(board, humanPlayer) {
		return loseScore
	}
	if isBoardFull(board) || depth == 0 {
		return evaluatePosition(board)
	}

	if maximizing {
		maxScore := math.Inf(-1)
		for col := 0; col < 7; col++ {
			if isValidMove(board, col) {
				tempBoard := copyBoard(board)
				makeMove(tempBoard, col, botPlayer)
				
				score := minimax(tempBoard, depth-1, alpha, beta, false)
				maxScore = math.Max(maxScore, score)
				alpha = math.Max(alpha, score)
				
				if beta <= alpha {
					break
				}
			}
		}
		return maxScore
	} else {
		minScore := math.Inf(1)
		for col := 0; col < 7; col++ {
			if isValidMove(board, col) {
				tempBoard := copyBoard(board)
				makeMove(tempBoard, col, humanPlayer)
				
				score := minimax(tempBoard, depth-1, alpha, beta, true)
				minScore = math.Min(minScore, score)
				beta = math.Min(beta, score)
				
				if beta <= alpha {
					break
				}
			}
		}
		return minScore
	}
}

// evaluatePosition evaluates the current board position
func evaluatePosition(board [][]int) float64 {
	var score float64

	// Check horizontal windows
	for row := 0; row < 6; row++ {
		for col := 0; col < 4; col++ {
			window := board[row][col:col+4]
			score += evaluateWindow(window)
		}
	}

	// Check vertical windows
	for row := 0; row < 3; row++ {
		for col := 0; col < 7; col++ {
			window := []int{
				board[row][col],
				board[row+1][col],
				board[row+2][col],
				board[row+3][col],
			}
			score += evaluateWindow(window)
		}
	}

	// Check diagonal windows (positive slope)
	for row := 0; row < 3; row++ {
		for col := 0; col < 4; col++ {
			window := []int{
				board[row][col],
				board[row+1][col+1],
				board[row+2][col+2],
				board[row+3][col+3],
			}
			score += evaluateWindow(window)
		}
	}

	// Check diagonal windows (negative slope)
	for row := 3; row < 6; row++ {
		for col := 0; col < 4; col++ {
			window := []int{
				board[row][col],
				board[row-1][col+1],
				board[row-2][col+2],
				board[row-3][col+3],
			}
			score += evaluateWindow(window)
		}
	}

	// Prefer center column
	centerCount := 0
	for row := 0; row < 6; row++ {
		if board[row][3] == botPlayer {
			centerCount++
		}
	}
	score += float64(centerCount) * 3

	return score
}

// evaluateWindow evaluates a window of 4 positions
func evaluateWindow(window []int) float64 {
	botCount := 0
	humanCount := 0
	emptyCount := 0

	for _, cell := range window {
		switch cell {
		case botPlayer:
			botCount++
		case humanPlayer:
			humanCount++
		case 0:
			emptyCount++
		}
	}

	if botCount == 4 {
		return 100
	} else if botCount == 3 && emptyCount == 1 {
		return 5
	} else if botCount == 2 && emptyCount == 2 {
		return 2
	}

	if humanCount == 3 && emptyCount == 1 {
		return -4
	}

	return 0
}

// checkWinningMove checks if there's an immediate winning move
func checkWinningMove(board [][]int, player int) int {
	for col := 0; col < 7; col++ {
		if isValidMove(board, col) {
			tempBoard := copyBoard(board)
			makeMove(tempBoard, col, player)
			if isWinningBoard(tempBoard, player) {
				return col
			}
		}
	}
	return -1
}

// checkBlockingMove checks if we need to block opponent's winning move
func checkBlockingMove(board [][]int, player int) int {
	opponent := 3 - player // Switch between 1 and 2
	return checkWinningMove(board, opponent)
}

// Helper functions
func isValidMove(board [][]int, col int) bool {
	return col >= 0 && col < 7 && board[0][col] == 0
}

func makeMove(board [][]int, col, player int) {
	for row := 5; row >= 0; row-- {
		if board[row][col] == 0 {
			board[row][col] = player
			return
		}
	}
}

func copyBoard(board [][]int) [][]int {
	newBoard := make([][]int, len(board))
	for i := range board {
		newBoard[i] = make([]int, len(board[i]))
		copy(newBoard[i], board[i])
	}
	return newBoard
}

func isWinningBoard(board [][]int, player int) bool {
	// Check horizontal
	for row := 0; row < 6; row++ {
		for col := 0; col < 4; col++ {
			if board[row][col] == player &&
				board[row][col+1] == player &&
				board[row][col+2] == player &&
				board[row][col+3] == player {
				return true
			}
		}
	}

	// Check vertical
	for row := 0; row < 3; row++ {
		for col := 0; col < 7; col++ {
			if board[row][col] == player &&
				board[row+1][col] == player &&
				board[row+2][col] == player &&
				board[row+3][col] == player {
				return true
			}
		}
	}

	// Check diagonal (positive slope)
	for row := 0; row < 3; row++ {
		for col := 0; col < 4; col++ {
			if board[row][col] == player &&
				board[row+1][col+1] == player &&
				board[row+2][col+2] == player &&
				board[row+3][col+3] == player {
				return true
			}
		}
	}

	// Check diagonal (negative slope)
	for row := 3; row < 6; row++ {
		for col := 0; col < 4; col++ {
			if board[row][col] == player &&
				board[row-1][col+1] == player &&
				board[row-2][col+2] == player &&
				board[row-3][col+3] == player {
				return true
			}
		}
	}

	return false
}

func isBoardFull(board [][]int) bool {
	for col := 0; col < 7; col++ {
		if board[0][col] == 0 {
			return false
		}
	}
	return true
}