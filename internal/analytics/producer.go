package analytics

import (
	"encoding/json"
	"time"

	"github.com/IBM/sarama"
)

// GameEvent represents an event in the game
type GameEvent struct {
	Type      string                 `json:"type"`
	Timestamp time.Time              `json:"timestamp"`
	GameID    string                 `json:"gameId"`
	Data      map[string]interface{} `json:"data"`
}

// EventType constants
const (
	EventGameStart   = "game_start"
	EventMove        = "move"
	EventGameEnd     = "game_end"
	EventPlayerJoin  = "player_join"
	EventPlayerLeave = "player_leave"
)

// Producer handles sending events to Kafka
type Producer struct {
	producer sarama.SyncProducer
	topic    string
}

// NewProducer creates a new Kafka producer
func NewProducer(brokers []string, topic string) (*Producer, error) {
	config := sarama.NewConfig()
	config.Producer.RequiredAcks = sarama.WaitForAll
	config.Producer.Retry.Max = 5
	config.Producer.Return.Successes = true

	producer, err := sarama.NewSyncProducer(brokers, config)
	if err != nil {
		return nil, err
	}

	return &Producer{
		producer: producer,
		topic:    topic,
	}, nil
}

// SendEvent sends a game event to Kafka
func (p *Producer) SendEvent(event GameEvent) error {
	event.Timestamp = time.Now()

	payload, err := json.Marshal(event)
	if err != nil {
		return err
	}

	msg := &sarama.ProducerMessage{
		Topic: p.topic,
		Value: sarama.StringEncoder(payload),
	}

	_, _, err = p.producer.SendMessage(msg)
	return err
}

// Close closes the Kafka producer
func (p *Producer) Close() error {
	return p.producer.Close()
}

// Helper functions to create specific events

// CreateGameStartEvent creates a game start event
func CreateGameStartEvent(gameID string, player1, player2 string, isBotGame bool) GameEvent {
	return GameEvent{
		Type:   EventGameStart,
		GameID: gameID,
		Data: map[string]interface{}{
			"player1":   player1,
			"player2":   player2,
			"isBotGame": isBotGame,
		},
	}
}

// CreateMoveEvent creates a move event
func CreateMoveEvent(gameID string, player string, row, col int) GameEvent {
	return GameEvent{
		Type:   EventMove,
		GameID: gameID,
		Data: map[string]interface{}{
			"player": player,
			"row":    row,
			"column": col,
		},
	}
}

// CreateGameEndEvent creates a game end event
func CreateGameEndEvent(gameID string, winner string, isDraw bool, duration time.Duration) GameEvent {
	return GameEvent{
		Type:   EventGameEnd,
		GameID: gameID,
		Data: map[string]interface{}{
			"winner":   winner,
			"isDraw":   isDraw,
			"duration": duration.Seconds(),
		},
	}
}

// CreatePlayerEvent creates a player join/leave event
func CreatePlayerEvent(eventType string, gameID, player string) GameEvent {
	return GameEvent{
		Type:   eventType,
		GameID: gameID,
		Data: map[string]interface{}{
			"player": player,
		},
	}
}
