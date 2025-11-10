-- Create players table
CREATE TABLE IF NOT EXISTS players (
    id SERIAL PRIMARY KEY,
    username VARCHAR(50) UNIQUE NOT NULL,
    games_played INT DEFAULT 0,
    games_won INT DEFAULT 0,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

-- Create games table
CREATE TABLE IF NOT EXISTS games (
    id SERIAL PRIMARY KEY,
    player1_id INT NOT NULL,
    player2_id INT,
    winner_id INT,
    is_bot_game BOOLEAN DEFAULT FALSE,
    start_time TIMESTAMP NOT NULL,
    end_time TIMESTAMP,
    game_state JSON,
    FOREIGN KEY (player1_id) REFERENCES players(id),
    FOREIGN KEY (player2_id) REFERENCES players(id),
    FOREIGN KEY (winner_id) REFERENCES players(id)
);

-- Create index for player statistics
CREATE INDEX IF NOT EXISTS idx_players_games_won ON players(games_won DESC);

-- Create view for leaderboard
CREATE OR REPLACE VIEW leaderboard AS
SELECT 
    username,
    games_played,
    games_won,
    CASE 
        WHEN games_played > 0 THEN ROUND((games_won::numeric / games_played::numeric) * 100, 2)
        ELSE 0
    END as win_percentage
FROM players
WHERE games_played > 0
ORDER BY games_won DESC, win_percentage DESC;