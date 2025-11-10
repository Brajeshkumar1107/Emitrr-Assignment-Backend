package ws

type GameStatus string

const (
	StatusInProgress GameStatus = "in_progress"
	StatusCompleted  GameStatus = "completed"
	StatusDraw       GameStatus = "draw"
)

type Move struct {
	Row    int `json:"row"`
	Column int `json:"column"`
}

type Player struct {
	ID       string `json:"id"`
	Username string `json:"username"`
	Color    string `json:"color"`
	IsBot    bool   `json:"isBot,omitempty"`
}

type GameState struct {
	ID                string     `json:"id"`
	Board             [][]int    `json:"board"`
	CurrentTurn       int        `json:"currentTurn"`
	LastMove          *Move      `json:"lastMove,omitempty"`
	Player1           *Player    `json:"player1"`
	Player2           *Player    `json:"player2"`
	Status            GameStatus `json:"status"`
	Winner            *Player    `json:"winner,omitempty"`
	PlayAgainRequests []string   `json:"playAgainRequests,omitempty"`
}
