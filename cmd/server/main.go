package main

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"strconv"

	"github.com/connect4/backend/internal/analytics"
	"github.com/connect4/backend/internal/database"
	"github.com/connect4/backend/internal/ws"
	"github.com/joho/godotenv"
)

func main() {
	// -----------------------------------------
	// Load .env.local for local development
	// -----------------------------------------
	if os.Getenv("RAILWAY_ENVIRONMENT_NAME") == "" {
		if err := godotenv.Load(".env.local"); err != nil {
			log.Println("Note: .env.local not found, using system environment variables")
		}
	}

	log.Println("Starting Connect 4 Game Server...")

	// -----------------------------------------
	// ✅ Initialize Database (Railway or Local)
	// -----------------------------------------
	var db *database.DB
	var err error

	if os.Getenv("DATABASE_URL") != "" {
		// Railway DATABASE_URL connection
		log.Println("[DB] Connecting using DATABASE_URL...")
		db, err = database.NewDB(database.Config{})
		if err != nil {
			log.Fatalf("❌ Database connection failed: %v", err)
		}
		log.Println("✅ Database connected successfully (via DATABASE_URL)")
	} else if os.Getenv("PGHOST") != "" {
		// Railway PG* environment vars
		log.Println("[DB] Connecting using PG* environment variables...")
		portStr := os.Getenv("PGPORT")
		port, _ := strconv.Atoi(portStr)
		dbConfig := database.Config{
			Host:     os.Getenv("PGHOST"),
			Port:     port,
			User:     os.Getenv("PGUSER"),
			Password: os.Getenv("PGPASSWORD"),
			DBName:   os.Getenv("PGDATABASE"),
		}
		db, err = database.NewDB(dbConfig)
		if err != nil {
			log.Fatalf("❌ Database connection failed: %v", err)
		}
		log.Println("✅ Database connected successfully (via PGHOST)")
	} else {
		log.Println("⚠️ No database configuration found. Running without DB.")
	}

	// -----------------------------------------
	// 🧱 Auto-create schema (tables + view)
	// -----------------------------------------
	if db != nil {
		schemaSQL := `
CREATE TABLE IF NOT EXISTS players (
	id SERIAL PRIMARY KEY,
	username VARCHAR(50) UNIQUE NOT NULL,
	games_played INT DEFAULT 0,
	games_won INT DEFAULT 0,
	created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

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

CREATE INDEX IF NOT EXISTS idx_players_games_won ON players(games_won DESC);

CREATE OR REPLACE VIEW leaderboard AS
SELECT 
	username,
	games_played,
	games_won,
	CASE 
		WHEN games_played > 0 THEN ROUND((games_won::numeric / games_played::numeric) * 100, 2)
		ELSE 0
	END AS win_percentage
FROM players
WHERE games_played > 0
ORDER BY games_won DESC, win_percentage DESC;
`
		if _, err := db.ExecContext(context.Background(), schemaSQL); err != nil {
			log.Printf("⚠️ Failed to initialize schema: %v", err)
		} else {
			log.Println("✅ Database schema ensured (tables & leaderboard view ready)")
		}
	}

	// -----------------------------------------
	// Initialize Kafka (Optional)
	// -----------------------------------------
	var producer *analytics.Producer
	kafkaBrokers := os.Getenv("KAFKA_BROKERS")
	if kafkaBrokers != "" {
		brokers := []string{kafkaBrokers}
		producer, err = analytics.NewProducer(brokers, "game-events")
		if err != nil {
			log.Printf("Warning: Kafka producer init failed: %v", err)
		} else {
			log.Println("Kafka producer initialized successfully")
			defer producer.Close()
		}
	}

	var consumer *analytics.Consumer
	if db != nil && kafkaBrokers != "" {
		brokers := []string{kafkaBrokers}
		consumer, err = analytics.NewConsumer(brokers, "analytics-group", db.DB)
		if err != nil {
			log.Printf("Warning: Kafka consumer init failed: %v", err)
		} else {
			log.Println("Kafka consumer initialized successfully")
			if _, err := db.DB.Exec(analytics.CreateAnalyticsTableSQL); err != nil {
				log.Printf("Warning: Failed to create analytics table: %v", err)
			}
			ctx := context.Background()
			go func() {
				if err := consumer.Start(ctx, []string{"game-events"}); err != nil {
					log.Printf("Error in consumer: %v", err)
				}
			}()
			defer consumer.Close()
		}
	}

	// -----------------------------------------
	// Initialize WebSocket Hub
	// -----------------------------------------
	hub := ws.NewHub()
	if db != nil {
		hub.SetDB(db)
	}
	if producer != nil {
		hub.SetProducer(producer)
	}
	go hub.Run()

	// -----------------------------------------
	// Allowed Origins for CORS
	// -----------------------------------------
	allowedOrigins := []string{
		"https://emitrr-assignment-frontend.vercel.app",
		"https://emitrr-assignment-frontend.vercel.app/",
		"https://emitrr-assignment-frontend-9xvs.vercel.app",
		"https://emitrr-assignment-frontend-9xvs.vercel.app/",
		"http://localhost:3000",
		"http://localhost:3001",
		"http://localhost:5173",
	}

	// -----------------------------------------
	// WebSocket Endpoint
	// -----------------------------------------
	http.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		log.Printf("WebSocket connection attempt from origin: %s", origin)
		if !isAllowedOrigin(origin, allowedOrigins) {
			log.Printf("WebSocket connection rejected - origin not allowed: %s", origin)
			http.Error(w, "Origin not allowed", http.StatusForbidden)
			return
		}
		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}
		ws.ServeWs(hub, w, r)
	})

	// -----------------------------------------
	// Leaderboard Endpoint
	// -----------------------------------------
	http.HandleFunc("/leaderboard", func(w http.ResponseWriter, r *http.Request) {
		addCORSHeaders(w, r, allowedOrigins)
		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}
		if r.Method != "GET" {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if db == nil {
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode([]database.Player{})
			return
		}
		ctx := context.Background()
		players, err := db.GetLeaderboard(ctx, 100)
		if err != nil {
			log.Printf("Error fetching leaderboard: %v", err)
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}
		type PlayerStats struct {
			Username      string  `json:"username"`
			GamesPlayed   int     `json:"gamesPlayed"`
			GamesWon      int     `json:"gamesWon"`
			WinPercentage float64 `json:"winPercentage"`
		}
		stats := make([]PlayerStats, len(players))
		for i, p := range players {
			var winPct float64
			if p.GamesPlayed > 0 {
				winPct = float64(p.GamesWon) / float64(p.GamesPlayed) * 100
			}
			stats[i] = PlayerStats{
				Username:      p.Username,
				GamesPlayed:   p.GamesPlayed,
				GamesWon:      p.GamesWon,
				WinPercentage: winPct,
			}
		}
		json.NewEncoder(w).Encode(stats)
	})

	// -----------------------------------------
	// Active Users Endpoint
	// -----------------------------------------
	http.HandleFunc("/active-users", func(w http.ResponseWriter, r *http.Request) {
		addCORSHeaders(w, r, allowedOrigins)
		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}
		hub.HandleActiveUsers(w, r)
	})

	// -----------------------------------------
	// Default Route
	// -----------------------------------------
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		addCORSHeaders(w, r, allowedOrigins)
		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}
		http.NotFound(w, r)
	})

	// -----------------------------------------
	// Start HTTP Server
	// -----------------------------------------
	port := getEnv("PORT", getEnv("SERVER_PORT", "8080"))
	log.Printf("Server running on :%s", port)
	if err := http.ListenAndServe(":"+port, nil); err != nil {
		log.Fatal("ListenAndServe: ", err)
	}
}

// -----------------------------------------
// Helper Functions
// -----------------------------------------

func addCORSHeaders(w http.ResponseWriter, r *http.Request, allowedOrigins []string) {
	origin := r.Header.Get("Origin")
	for _, allowed := range allowedOrigins {
		if origin == allowed {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS, PUT, DELETE")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
			w.Header().Set("Access-Control-Allow-Credentials", "true")
			return
		}
	}
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS, PUT, DELETE")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
}

func isAllowedOrigin(origin string, allowed []string) bool {
	if len(origin) > 0 && origin[len(origin)-1] == '/' {
		origin = origin[:len(origin)-1]
	}
	for _, a := range allowed {
		if origin == a || origin+"/" == a {
			return true
		}
	}
	return false
}

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}
