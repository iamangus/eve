package store

import (
	"errors"
	"sync"
	"time"
)

var ErrNotFound = errors.New("conversation not found")

type Message struct {
	ID        int64     `json:"id"`
	Role      string    `json:"role"`
	Content   string    `json:"content"`
	RunID     string    `json:"run_id,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

type Conversation struct {
	ID          string    `json:"id"`
	Title       string    `json:"title"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
	ActiveRunID string    `json:"active_run_id,omitempty"`
	Messages    []Message `json:"messages,omitempty"`
}

type ConversationSummary struct {
	ID           string    `json:"id"`
	Title        string    `json:"title"`
	UpdatedAt    time.Time `json:"updated_at"`
	MessageCount int       `json:"message_count"`
	ActiveRunID  string    `json:"active_run_id,omitempty"`
}

type conversationRecord struct {
	title       string
	created     time.Time
	updated     time.Time
	activeRunID string
	messages    []Message
	nextMsgID   int64
}

// Store is an in-memory conversation store. It is safe for concurrent use.
// History is lost when the process exits.
type Store struct {
	mu   sync.RWMutex
	convs map[string]*conversationRecord
}

func New() *Store {
	return &Store{convs: make(map[string]*conversationRecord)}
}