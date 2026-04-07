package errlog

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/redis/go-redis/v9"
)

// Entry represents a single error/warning log entry written to Redis.
type Entry struct {
	Timestamp string `json:"ts"`
	Level     string `json:"level"`   // ERROR, WARN, INFO
	Source    string `json:"source"`  // e.g. "gate:handler", "link:stream"
	Message   string `json:"message"`
}

// Logger writes structured log entries to a Redis Stream.
// Stream key: dylaris:errors:{service}:{instanceID}
type Logger struct {
	client     *redis.Client
	streamKey  string
	maxEntries int64
}

// New creates a new error logger that writes to Redis Streams.
// service: "gate", "link", "hub"
// instanceID: unique identifier for this instance (e.g. gateID, nodeID)
func New(client *redis.Client, service, instanceID string) *Logger {
	return &Logger{
		client:     client,
		streamKey:  fmt.Sprintf("dylaris:errors:%s:%s", service, instanceID),
		maxEntries: 500,
	}
}

// StreamKey returns the Redis stream key used by this logger.
func (l *Logger) StreamKey() string {
	return l.streamKey
}

// Error logs an error-level entry.
func (l *Logger) Error(source, message string) {
	l.write("ERROR", source, message)
}

// Warn logs a warning-level entry.
func (l *Logger) Warn(source, message string) {
	l.write("WARN", source, message)
}

// Info logs an info-level entry.
func (l *Logger) Info(source, message string) {
	l.write("INFO", source, message)
}

// Errorf logs a formatted error-level entry.
func (l *Logger) Errorf(source, format string, args ...interface{}) {
	l.write("ERROR", source, fmt.Sprintf(format, args...))
}

// Warnf logs a formatted warning-level entry.
func (l *Logger) Warnf(source, format string, args ...interface{}) {
	l.write("WARN", source, fmt.Sprintf(format, args...))
}

func (l *Logger) write(level, source, message string) {
	if l == nil || l.client == nil {
		return
	}

	entry := Entry{
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		Level:     level,
		Source:    source,
		Message:   message,
	}

	data, err := json.Marshal(entry)
	if err != nil {
		log.Printf("[errlog] marshal error: %v", err)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	_, err = l.client.XAdd(ctx, &redis.XAddArgs{
		Stream: l.streamKey,
		MaxLen: l.maxEntries,
		Approx: true,
		Values: map[string]interface{}{"data": string(data)},
	}).Result()

	if err != nil {
		// Don't recurse - just log locally
		log.Printf("[errlog] redis write failed: %v", err)
	}
}

// ReadEntries reads the latest entries from any error stream.
// Returns up to `count` entries, newest first.
func ReadEntries(client *redis.Client, streamKey string, count int64) ([]Entry, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	results, err := client.XRevRangeN(ctx, streamKey, "+", "-", count).Result()
	if err != nil {
		return nil, err
	}

	entries := make([]Entry, 0, len(results))
	for _, msg := range results {
		dataStr, ok := msg.Values["data"].(string)
		if !ok {
			continue
		}
		var entry Entry
		if err := json.Unmarshal([]byte(dataStr), &entry); err != nil {
			continue
		}
		entries = append(entries, entry)
	}
	return entries, nil
}

// ScanErrorStreams finds all error streams matching a pattern.
func ScanErrorStreams(client *redis.Client, service string) ([]string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	pattern := fmt.Sprintf("dylaris:errors:%s:*", service)
	var keys []string
	var cursor uint64

	for {
		batch, nextCursor, err := client.Scan(ctx, cursor, pattern, 100).Result()
		if err != nil {
			return keys, err
		}
		keys = append(keys, batch...)
		cursor = nextCursor
		if cursor == 0 {
			break
		}
	}
	return keys, nil
}
