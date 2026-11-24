package db

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ivan/engram/internal/db"
)

type OutboxListener struct {
	pool       *pgxpool.Pool
	queries    *db.Queries
	dsn        string
	mu         sync.RWMutex
	subs       map[string][]chan []byte
	lastSeenID int64
}

func NewOutboxListener(pool *pgxpool.Pool, dsn string) *OutboxListener {
	return &OutboxListener{
		pool:    pool,
		queries: db.New(pool),
		dsn:     dsn,
		subs:    make(map[string][]chan []byte),
	}
}

func (l *OutboxListener) Subscribe(projectID string) (<-chan []byte, func()) {
	ch := make(chan []byte, 32)
	l.mu.Lock()
	l.subs[projectID] = append(l.subs[projectID], ch)
	l.mu.Unlock()

	unsubscribe := func() {
		l.mu.Lock()
		defer l.mu.Unlock()
		subList := l.subs[projectID]
		for i, c := range subList {
			if c == ch {
				l.subs[projectID] = append(subList[:i], subList[i+1:]...)
				close(ch)
				break
			}
		}
	}

	return ch, unsubscribe
}

func (l *OutboxListener) broadcast(projectID string, data []byte) {
	l.mu.RLock()
	defer l.mu.RUnlock()

	for _, ch := range l.subs[projectID] {
		select {
		case ch <- data:
		default:
			// Non-blocking drop if client channel buffer is full
		}
	}
}

func (l *OutboxListener) Start(ctx context.Context) error {
	conn, err := pgx.Connect(ctx, l.dsn)
	if err != nil {
		return fmt.Errorf("outbox listener failed to connect dedicated pgx.Conn: %w", err)
	}

	_, err = conn.Exec(ctx, "LISTEN engram_events")
	if err != nil {
		_ = conn.Close(ctx)
		return fmt.Errorf("outbox listener failed to LISTEN engram_events: %w", err)
	}

	log.Printf("[engram-outbox] Listening for outbox notifications on channel 'engram_events'")

	go func() {
		defer func() {
			_ = conn.Close(context.Background())
		}()

		for {
			select {
			case <-ctx.Done():
				return
			default:
			}

			// Wait for ping notification with timeout so we can check context
			waitCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
			_, err := conn.WaitForNotification(waitCtx)
			cancel()

			if err != nil {
				if ctx.Err() != nil {
					return
				}
				// Timeout is normal; poll for any unread events as a safety fallback
			}

			// Query missed events since lastSeenID
			events, err := l.queries.GetEventsAfter(ctx, db.GetEventsAfterParams{
				ProjectID: "", // empty matches all
				AfterID:   l.lastSeenID,
				MaxLimit:  100,
			})
			if err != nil {
				continue
			}

			for _, ev := range events {
				if ev.ID > l.lastSeenID {
					l.lastSeenID = ev.ID
				}
				msgBytes, _ := json.Marshal(map[string]interface{}{
					"id":         ev.ID,
					"project_id": ev.ProjectID,
					"topic":      ev.Topic,
					"payload":    json.RawMessage(ev.Payload),
					"created_at": ev.CreatedAt.Time,
				})
				l.broadcast(ev.ProjectID, msgBytes)
			}
		}
	}()

	return nil
}
