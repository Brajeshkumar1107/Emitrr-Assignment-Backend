package utils

import (
	"context"
	"log"
	"os"
	"os/signal"
	"sync"
	"syscall"
)

// CleanupFunc represents a cleanup function
type CleanupFunc func() error

// ResourceManager handles graceful shutdown of resources
type ResourceManager struct {
	cleanupFuncs []CleanupFunc
	mu           sync.Mutex
}

// NewResourceManager creates a new resource manager
func NewResourceManager() *ResourceManager {
	return &ResourceManager{
		cleanupFuncs: make([]CleanupFunc, 0),
	}
}

// AddCleanupFunc adds a cleanup function to be executed during shutdown
func (rm *ResourceManager) AddCleanupFunc(fn CleanupFunc) {
	rm.mu.Lock()
	defer rm.mu.Unlock()
	rm.cleanupFuncs = append(rm.cleanupFuncs, fn)
}

// Cleanup executes all cleanup functions
func (rm *ResourceManager) Cleanup() {
	rm.mu.Lock()
	defer rm.mu.Unlock()

	for _, fn := range rm.cleanupFuncs {
		if err := fn(); err != nil {
			log.Printf("Cleanup error: %v", err)
		}
	}
}

// HandleGracefulShutdown sets up signal handling for graceful shutdown
func (rm *ResourceManager) HandleGracefulShutdown(ctx context.Context) {
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		select {
		case <-signals:
			log.Println("Shutdown signal received, cleaning up...")
			rm.Cleanup()
			os.Exit(0)
		case <-ctx.Done():
			log.Println("Context cancelled, cleaning up...")
			rm.Cleanup()
			os.Exit(0)
		}
	}()
}
