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
	"github.com/joho/godotenv"
)

func main() {
	// Load .env.local for local development (only if not on Railway)
	if os.Getenv("RAILWAY_ENVIRONMENT_NAME") == "" {
		if err := godotenv.Load(".env.local"); err != nil {
			log.Println("Note: .env.local not found, using system environment variables")
		}
	}

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

	// Define allowed origins
	allowedOrigins := []string{
		// Production frontend (current and legacy) - include with and without trailing slash
		"https://emitrr-assignment-frontend.vercel.app",
		"https://emitrr-assignment-frontend.vercel.app/",
		"https://emitrr-assignment-frontend-9xvs.vercel.app",
		"https://emitrr-assignment-frontend-9xvs.vercel.app/",
		"http://localhost:3000",
		"http://localhost:3001",
		"http://localhost:5173",
	}

	// CORS middleware helper for HTTP requests
	corsMiddleware := func(w http.ResponseWriter, r *http.Request, allowedOrigins []string) bool {
		origin := r.Header.Get("Origin")
		for _, allowed := range allowedOrigins {
			if origin == allowed {
				w.Header().Set("Access-Control-Allow-Origin", origin)
				w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS, PUT, DELETE")
				w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
				w.Header().Set("Access-Control-Allow-Credentials", "true")
				return true
			}
		}
		// Allow all for fallback
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS, PUT, DELETE")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		return true
	}

	// Check if origin is allowed for WebSocket
	isOriginAllowed := func(origin string) bool {
		// Remove trailing slash if present
		if len(origin) > 0 && origin[len(origin)-1] == '/' {
			origin = origin[:len(origin)-1]
		}
		for _, allowed := range allowedOrigins {
			if origin == allowed {
				return true
			}
		}
		return false
	}

	// Handle WebSocket connections
	http.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")

		// Log the connection attempt
		log.Printf("WebSocket connection attempt from origin: %s", origin)

		// Check if origin is allowed; if not, reject the upgrade to avoid confusing logs
		if origin != "" && !isOriginAllowed(origin) {
			log.Printf("WebSocket connection rejected - origin not allowed: %s", origin)
			http.Error(w, "Origin not allowed", http.StatusForbidden)
			return
		}

		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}

		// Allow WebSocket upgrade from allowed origins or localhost for development
		// Note: WebSocket doesn't use standard CORS, browser handles it automatically after connection
		ws.ServeWs(hub, w, r)
	})

	// Handle active users endpoint
	http.HandleFunc("/active-users", func(w http.ResponseWriter, r *http.Request) {
		// CORS headers
		corsMiddleware(w, r, allowedOrigins)
		w.Header().Set("Content-Type", "application/json")

		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}

		hub.HandleActiveUsers(w, r)
	})

	// Handle leaderboard endpoint
	http.HandleFunc("/leaderboard", func(w http.ResponseWriter, r *http.Request) {
		// CORS headers
		corsMiddleware(w, r, allowedOrigins)
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

	// Handle root path
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		corsMiddleware(w, r, allowedOrigins)

		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}

		http.NotFound(w, r)
	})

	// Start server - port configurable via PORT env var (Railway uses PORT)
	// Falls back to SERVER_PORT or 8080 for local development
	port := getEnv("PORT", getEnv("SERVER_PORT", "8080"))
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
