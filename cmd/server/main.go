package main

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"os"

	"github.com/connect4/backend/internal/analytics"
	"github.com/connect4/backend/internal/database"
	"github.com/connect4/backend/internal/ws"
)

func main() {
	log.Println("Starting Connect 4 Game Server...")

	// Initialize database (optional - can be disabled for testing)
	var db *database.DB
	var err error
	dbHost := os.Getenv("DB_HOST")
	if dbHost != "" {
		db, err = database.NewDB(database.Config{
			Host:     getEnv("DB_HOST", "localhost"),
			Port:     5432,
			User:     getEnv("DB_USER", "postgres"),
			Password: getEnv("DB_PASSWORD", "postgres"),
			DBName:   getEnv("DB_NAME", "connect4"),
		})
		if err != nil {
			log.Printf("Warning: Database connection failed: %v. Continuing without database.", err)
		} else {
			log.Println("Database connected successfully")
		}
	}

	// Initialize Kafka producer (optional)
	var producer *analytics.Producer
	kafkaBrokers := os.Getenv("KAFKA_BROKERS")
	if kafkaBrokers != "" {
		brokers := []string{kafkaBrokers}
		producer, err = analytics.NewProducer(brokers, "game-events")
		if err != nil {
			log.Printf("Warning: Kafka producer initialization failed: %v. Continuing without analytics.", err)
		} else {
			log.Println("Kafka producer initialized successfully")
			defer producer.Close()
		}
	}

	// Initialize Kafka consumer (optional)
	var consumer *analytics.Consumer
	if db != nil && kafkaBrokers != "" {
		brokers := []string{kafkaBrokers}
		consumer, err = analytics.NewConsumer(brokers, "analytics-group", db.DB)
		if err != nil {
			log.Printf("Warning: Kafka consumer initialization failed: %v. Continuing without analytics.", err)
		} else {
			log.Println("Kafka consumer initialized successfully")
			// Create analytics table
			if _, err := db.DB.Exec(analytics.CreateAnalyticsTableSQL); err != nil {
				log.Printf("Warning: Failed to create analytics table: %v", err)
			}
			// Start consumer in background
			ctx := context.Background()
			go func() {
				if err := consumer.Start(ctx, []string{"game-events"}); err != nil {
					log.Printf("Error in consumer: %v", err)
				}
			}()
			defer consumer.Close()
		}
	}

	// Initialize the WebSocket hub
	hub := ws.NewHub()
	// Set database and producer if available
	if db != nil {
		hub.SetDB(db)
	}
	if producer != nil {
		hub.SetProducer(producer)
	}
	go hub.Run()

	// Handle WebSocket connections
	http.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		// CORS headers
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")

		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}

		ws.ServeWs(hub, w, r)
	})

	// Handle active users endpoint
	http.HandleFunc("/active-users", func(w http.ResponseWriter, r *http.Request) {
		hub.HandleActiveUsers(w, r)
	})

	// Handle leaderboard endpoint
	http.HandleFunc("/leaderboard", func(w http.ResponseWriter, r *http.Request) {
		// CORS headers
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		w.Header().Set("Content-Type", "application/json")

		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}

		if r.Method != "GET" {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		if db == nil {
			// Return empty leaderboard if database not available
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

		// Convert to frontend format
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

	// Handle CORS for development
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")

		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}

		http.NotFound(w, r)
	})

	// Start server - port configurable via SERVER_PORT env var (default 8080)
	port := getEnv("SERVER_PORT", "8080")
	log.Printf("Server running on :%s", port)
	if err := http.ListenAndServe(":"+port, nil); err != nil {
		log.Fatal("ListenAndServe: ", err)
	}
}

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}
