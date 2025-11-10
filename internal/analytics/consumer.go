package analytics

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"

	"github.com/IBM/sarama"
)

// Consumer handles consuming and processing game events
type Consumer struct {
	consumer sarama.ConsumerGroup
	db       *sql.DB
	ready    chan bool
}

// ConsumerGroupHandler implements the sarama.ConsumerGroupHandler interface
type ConsumerGroupHandler struct {
	ready    chan bool
	db       *sql.DB
	consumer *Consumer
}

// NewConsumer creates a new Kafka consumer
func NewConsumer(brokers []string, groupID string, db *sql.DB) (*Consumer, error) {
	config := sarama.NewConfig()
	config.Consumer.Group.Rebalance.Strategy = sarama.BalanceStrategyRoundRobin
	config.Consumer.Offsets.Initial = sarama.OffsetOldest

	group, err := sarama.NewConsumerGroup(brokers, groupID, config)
	if err != nil {
		return nil, err
	}

	consumer := &Consumer{
		consumer: group,
		db:       db,
		ready:    make(chan bool),
	}

	return consumer, nil
}

// Start starts consuming messages from Kafka
func (c *Consumer) Start(ctx context.Context, topics []string) error {
	handler := &ConsumerGroupHandler{
		ready:    c.ready,
		db:       c.db,
		consumer: c,
	}

	for {
		err := c.consumer.Consume(ctx, topics, handler)
		if err != nil {
			return err
		}

		if ctx.Err() != nil {
			return ctx.Err()
		}

		c.ready = make(chan bool)
	}
}

// Setup is run before consuming begins
func (h *ConsumerGroupHandler) Setup(sarama.ConsumerGroupSession) error {
	close(h.ready)
	return nil
}

// Cleanup is run when consuming ends
func (h *ConsumerGroupHandler) Cleanup(sarama.ConsumerGroupSession) error {
	return nil
}

// ConsumeClaim processes messages from a partition
func (h *ConsumerGroupHandler) ConsumeClaim(session sarama.ConsumerGroupSession, claim sarama.ConsumerGroupClaim) error {
	// Set up error channel for async error handling and carry message metadata
	type procErr struct {
		err error
		msg *sarama.ConsumerMessage
	}
	errChan := make(chan procErr, 1)

	// Set up message processing loop
	for {
		select {
		case message := <-claim.Messages():
			if message == nil {
				return nil
			}

			// Process message in a separate goroutine
			go func(msg *sarama.ConsumerMessage) {
				var event GameEvent
				if err := json.Unmarshal(msg.Value, &event); err != nil {
					errChan <- procErr{err: fmt.Errorf("error unmarshaling event: %v", err), msg: msg}
					return
				}

				if err := h.processEvent(event); err != nil {
					errChan <- procErr{err: fmt.Errorf("error processing event: %v", err), msg: msg}
					return
				}

				// Mark message as processed
				session.MarkMessage(msg, "")
			}(message)

		case pe := <-errChan:
			// Log error and continue processing
			log.Printf("Error processing message: %v", pe.err)

			// Implement retry logic or dead letter queue here
			if h.db != nil {
				// Store failed message in dead letter queue table
				_, dbErr := h.db.Exec(`
					INSERT INTO failed_events (
						topic,
						partition,
						offset,
						message,
						error,
						timestamp
					) VALUES ($1, $2, $3, $4, $5, NOW())`,
					pe.msg.Topic,
					pe.msg.Partition,
					pe.msg.Offset,
					string(pe.msg.Value),
					pe.err.Error(),
				)
				if dbErr != nil {
					log.Printf("Error storing failed message: %v", dbErr)
				}
			}

		case <-session.Context().Done():
			return nil
		}
	}
}

// processEvent handles different types of game events
func (h *ConsumerGroupHandler) processEvent(event GameEvent) error {
	switch event.Type {
	case EventGameEnd:
		return h.processGameEndEvent(event)
	case EventMove:
		return h.processMoveEvent(event)
	case EventPlayerJoin, EventPlayerLeave:
		return h.processPlayerEvent(event)
	}
	return nil
}

// Database schema for analytics
const (
	CreateAnalyticsTableSQL = `
		CREATE TABLE IF NOT EXISTS game_analytics (
			game_id TEXT,
			event_type TEXT,
			event_time TIMESTAMP,
			player TEXT,
			duration FLOAT,
			is_bot_game BOOLEAN,
			additional_data JSONB
		)`

	insertAnalyticsSQL = `
		INSERT INTO game_analytics (
			game_id, event_type, event_time, player, duration, is_bot_game, additional_data
		) VALUES ($1, $2, $3, $4, $5, $6, $7)`
)

func (h *ConsumerGroupHandler) processGameEndEvent(event GameEvent) error {
	winner, ok := event.Data["winner"].(string)
	if !ok {
		return fmt.Errorf("invalid winner data")
	}
	isDraw, ok := event.Data["isDraw"].(bool)
	if !ok {
		return fmt.Errorf("invalid isDraw data")
	}
	duration, ok := event.Data["duration"].(float64)
	if !ok {
		return fmt.Errorf("invalid duration data")
	}

	additionalData := map[string]interface{}{
		"isDraw": isDraw,
	}

	jsonData, err := json.Marshal(additionalData)
	if err != nil {
		return err
	}

	_, err = h.db.Exec(insertAnalyticsSQL,
		event.GameID,
		event.Type,
		event.Timestamp,
		winner,
		duration,
		false, // is_bot_game will be determined by game start event
		jsonData,
	)

	return err
}

func (h *ConsumerGroupHandler) processMoveEvent(event GameEvent) error {
	player, ok := event.Data["player"].(string)
	if !ok {
		return fmt.Errorf("invalid player data")
	}
	row, ok := event.Data["row"].(float64)
	if !ok {
		return fmt.Errorf("invalid row data")
	}
	col, ok := event.Data["column"].(float64)
	if !ok {
		return fmt.Errorf("invalid column data")
	}

	additionalData := map[string]interface{}{
		"row":    row,
		"column": col,
	}

	jsonData, err := json.Marshal(additionalData)
	if err != nil {
		return err
	}

	_, err = h.db.Exec(insertAnalyticsSQL,
		event.GameID,
		event.Type,
		event.Timestamp,
		player,
		0, // duration not applicable for moves
		false,
		jsonData,
	)

	return err
}

func (h *ConsumerGroupHandler) processPlayerEvent(event GameEvent) error {
	player, ok := event.Data["player"].(string)
	if !ok {
		return fmt.Errorf("invalid player data")
	}

	_, err := h.db.Exec(insertAnalyticsSQL,
		event.GameID,
		event.Type,
		event.Timestamp,
		player,
		0, // duration not applicable
		false,
		nil,
	)

	return err
}

// Close closes the consumer group
func (c *Consumer) Close() error {
	return c.consumer.Close()
}
