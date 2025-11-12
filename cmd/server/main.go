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
	// Load local .env file for local development
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
		// Railway's default connection method
		log.Println("[DB] Connecting using DATABASE_URL...")
		db, err = database.NewDB(database.Config{}) // database.go handles DATABASE_URL internally
		if err != nil {
			log.Printf("❌ Database connection failed: %v", err)
		} else {
			log.Println("✅ Database connected successfully (via DATABASE_URL)")
		}
	} else if os.Getenv("PGHOST") != "" {
		// Railway's PG* environment variables
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
			log.Printf("❌ Database connection failed: %v", err)
		} else {
			log.Println("✅ Database connected successfully (via PGHOST)")
		}
	} else if os.Getenv("DB_HOST") != "" {
		// Local dev fallback
		log.Println("[DB] Connecting using local environment variables...")
		db, err = database.NewDB(database.Config{
			Host:     getEnv("DB_HOST", "localhost"),
			Port:     5432,
			User:     getEnv("DB_USER", "postgres"),
			Password: getEnv("DB_PASSWORD", "postgres"),
			DBName:   getEnv("DB_NAME", "connect4"),
		})
		if err != nil {
			log.Printf("❌ Local database connection failed: %v", err)
		} else {
			log.Println("✅ Local database connected successfully")
		}
	} else {
		log.Println("⚠️ No database configuration found. Running without DB.")
	}

	// -----------------------------------------
	// Initialize Kafka (Optional - Safe Fallback)
	// -----------------------------------------
	var producer *analytics.Producer
	kafkaBrokers := os.Getenv("KAFKA_BROKERS")
	if kafkaBrokers != "" {
		brokers := []string{kafkaBrokers}
		producer, err = analytics.NewProducer(brokers, "game-events")
		if err != nil {
			log.Printf("Warning: Kafka producer initialization failed: %v", err)
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
			log.Printf("Warning: Kafka consumer initialization failed: %v", err)
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
