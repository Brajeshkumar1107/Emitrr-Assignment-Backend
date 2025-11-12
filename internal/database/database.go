package database

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"time"

	_ "github.com/lib/pq"
)

// DB represents the database connection
type DB struct {
	*sql.DB
}

// Config holds database configuration
type Config struct {
	Host     string
	Port     int
	User     string
	Password string
	DBName   string
}

// Player represents a player in the database
type Player struct {
	ID          int       `json:"id"`
	Username    string    `json:"username"`
	GamesPlayed int       `json:"gamesPlayed"`
	GamesWon    int       `json:"gamesWon"`
	CreatedAt   time.Time `json:"createdAt"`
}

// Game represents a game record in the database
type Game struct {
	ID        int                    `json:"id"`
	Player1ID int                    `json:"player1Id"`
	Player2ID int                    `json:"player2Id"`
	WinnerID  *int                   `json:"winnerId,omitempty"`
	IsBotGame bool                   `json:"isBotGame"`
	StartTime time.Time              `json:"startTime"`
	EndTime   *time.Time             `json:"endTime,omitempty"`
	GameState map[string]interface{} `json:"gameState"`
}

// NewDB creates a new database connection with connection pooling
func NewDB(config Config) (*DB, error) {
	connStr := fmt.Sprintf(
		"host=%s port=%d user=%s password=%s dbname=%s sslmode=disable",
		config.Host, config.Port, config.User, config.Password, config.DBName,
	)

	db, err := sql.Open("postgres", connStr)
	if err != nil {
		return nil, fmt.Errorf("error opening database: %v", err)
	}

	// Configure connection pool
	db.SetMaxOpenConns(25)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(5 * time.Minute)
	db.SetConnMaxIdleTime(5 * time.Minute)

	// Create a context with timeout for connection test
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Test connection with context
	if err := db.PingContext(ctx); err != nil {
		return nil, fmt.Errorf("error connecting to the database: %v", err)
	}

	// Debug: log successful DB connection for troubleshooting
	log.Printf("[DB] Connected to database host=%s dbname=%s", config.Host, config.DBName)

	return &DB{db}, nil
}

// CreatePlayer creates a new player record
func (db *DB) CreatePlayer(ctx context.Context, username string) (*Player, error) {
	query := `
		INSERT INTO players (username)
		VALUES ($1)
		RETURNING id, username, games_played, games_won, created_at`

	var player Player
	err := db.QueryRowContext(ctx, query, username).Scan(
		&player.ID,
		&player.Username,
		&player.GamesPlayed,
		&player.GamesWon,
		&player.CreatedAt,
	)

	if err != nil {
		return nil, fmt.Errorf("error creating player: %v", err)
	}

	return &player, nil
}

// GetPlayer retrieves a player by username
func (db *DB) GetPlayer(ctx context.Context, username string) (*Player, error) {
	query := `
		SELECT id, username, games_played, games_won, created_at
		FROM players
		WHERE username = $1`

	var player Player
	err := db.QueryRowContext(ctx, query, username).Scan(
		&player.ID,
		&player.Username,
		&player.GamesPlayed,
		&player.GamesWon,
		&player.CreatedAt,
	)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("error getting player: %v", err)
	}

	return &player, nil
}

// CreateGame creates a new game record. player2ID may be nil for bot games.
func (db *DB) CreateGame(ctx context.Context, player1ID int, player2ID *int, isBotGame bool) (*Game, error) {
	query := `
		INSERT INTO games (player1_id, player2_id, is_bot_game, start_time)
		VALUES ($1, $2, $3, CURRENT_TIMESTAMP)
		RETURNING id, player1_id, player2_id, is_bot_game, start_time`

	var game Game
	// Use interface{} for nullable parameter
	var p2 interface{}
	if player2ID == nil {
		p2 = nil
	} else {
		p2 = *player2ID
	}

	err := db.QueryRowContext(ctx, query, player1ID, p2, isBotGame).Scan(
		&game.ID,
		&game.Player1ID,
		&game.Player2ID,
		&game.IsBotGame,
		&game.StartTime,
	)

	if err != nil {
		return nil, fmt.Errorf("error creating game: %v", err)
	}

	return &game, nil
}

// UpdateGameResult updates the game result
func (db *DB) UpdateGameResult(ctx context.Context, gameID, winnerID int, gameState map[string]interface{}) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("error starting transaction: %v", err)
	}
	defer tx.Rollback()

	// Update game record
	gameStateJSON, err := json.Marshal(gameState)
	if err != nil {
		return fmt.Errorf("error marshaling game state: %v", err)
	}

	// Use nil for winner_id when winnerID == 0 (draw / no winner)
	var winnerParam interface{}
	if winnerID == 0 {
		winnerParam = nil
	} else {
		winnerParam = winnerID
	}

	_, err = tx.ExecContext(ctx, `
		UPDATE games 
		SET winner_id = $1, end_time = CURRENT_TIMESTAMP, game_state = $2
		WHERE id = $3`,
		winnerParam, gameStateJSON, gameID,
	)
	if err != nil {
		return fmt.Errorf("error updating game: %v", err)
	}

	// Update player statistics
	// Update player statistics. If the game is a bot game, only update the human player (player1).
	_, err = tx.ExecContext(ctx, `
		UPDATE players
		SET games_played = games_played + 1,
			games_won = CASE WHEN players.id = $1 THEN games_won + 1 ELSE games_won END
		FROM games g
		WHERE g.id = $2
		  AND (
			(g.is_bot_game = TRUE AND players.id = g.player1_id)
			OR (g.is_bot_game = FALSE AND players.id IN (g.player1_id, g.player2_id))
		  )`,
		winnerID, gameID,
	)
	if err != nil {
		return fmt.Errorf("error updating player statistics: %v", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("error committing transaction: %v", err)
	}

	return nil
}

// GetLeaderboard retrieves the top players
func (db *DB) GetLeaderboard(ctx context.Context, limit int) ([]Player, error) {
	query := `
		SELECT username, games_played, games_won,
		       CASE WHEN games_played > 0 THEN ROUND((games_won::numeric / games_played::numeric) * 100, 2)
		            ELSE 0 END as win_percentage
		FROM leaderboard
		ORDER BY games_won DESC, win_percentage DESC
		LIMIT $1`

	rows, err := db.QueryContext(ctx, query, limit)
	if err != nil {
		return nil, fmt.Errorf("error getting leaderboard: %v", err)
	}
	defer rows.Close()

	var players []Player
	for rows.Next() {
		var p Player
		// query returns (username, games_played, games_won, win_percentage)
		var winPct float64
		if err := rows.Scan(&p.Username, &p.GamesPlayed, &p.GamesWon, &winPct); err != nil {
			return nil, fmt.Errorf("error scanning leaderboard row: %v", err)
		}
		players = append(players, p)
	}

	return players, nil
}

// GetPlayerStats retrieves statistics for a specific player
func (db *DB) GetPlayerStats(ctx context.Context, username string) (*Player, error) {
	query := `
		SELECT username, games_played, games_won
		FROM leaderboard
		WHERE username = $1`

	var player Player
	err := db.QueryRowContext(ctx, query, username).Scan(
		&player.Username,
		&player.GamesPlayed,
		&player.GamesWon,
	)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("error getting player stats: %v", err)
	}

	return &player, nil
}
