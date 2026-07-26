package store

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"time"
)

func newID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return hex.EncodeToString(b)
}

var ErrNotFound = errors.New("conversation not found")

type Conversation struct {
	ID          string        `json:"id"`
	Title       string        `json:"title"`
	CreatedAt   time.Time     `json:"created_at"`
	UpdatedAt   time.Time     `json:"updated_at"`
	ActiveRunID string        `json:"active_run_id,omitempty"`
	Messages    []Message     `json:"messages,omitempty"`
}

type Message struct {
	ID        int64      `json:"id"`
	Role      string     `json:"role"`
	Content   string     `json:"content"`
	RunID     string     `json:"run_id,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
}

type ConversationSummary struct {
	ID            string    `json:"id"`
	Title         string    `json:"title"`
	UpdatedAt     time.Time `json:"updated_at"`
	MessageCount  int       `json:"message_count"`
	ActiveRunID   string    `json:"active_run_id,omitempty"`
}

func CreateConversation(db *sql.DB, title string) (*Conversation, error) {
	id := newID()
	now := time.Now()
	if title == "" {
		title = "New chat"
	}
	_, err := db.Exec(`INSERT INTO conversations (id, title, created_at, updated_at) VALUES (?, ?, ?, ?)`,
		id, title, now, now)
	if err != nil {
		return nil, fmt.Errorf("insert conversation: %w", err)
	}
	return &Conversation{ID: id, Title: title, CreatedAt: now, UpdatedAt: now}, nil
}

func ListConversations(db *sql.DB) ([]ConversationSummary, error) {
	rows, err := db.Query(`
		SELECT c.id, c.title, c.updated_at, COALESCE(c.active_run_id, ''),
		       (SELECT COUNT(*) FROM messages m WHERE m.conversation_id = c.id)
		FROM conversations c
		ORDER BY c.updated_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("list conversations: %w", err)
	}
	defer rows.Close()
	var out []ConversationSummary
	for rows.Next() {
		var c ConversationSummary
		if err := rows.Scan(&c.ID, &c.Title, &c.UpdatedAt, &c.ActiveRunID, &c.MessageCount); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func GetConversation(db *sql.DB, id string) (*Conversation, error) {
	c := &Conversation{}
	err := db.QueryRow(`SELECT id, title, created_at, updated_at, COALESCE(active_run_id, '') FROM conversations WHERE id = ?`, id).
		Scan(&c.ID, &c.Title, &c.CreatedAt, &c.UpdatedAt, &c.ActiveRunID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get conversation: %w", err)
	}
	rows, err := db.Query(`SELECT id, role, content, COALESCE(run_id, ''), created_at FROM messages WHERE conversation_id = ? ORDER BY id ASC`, id)
	if err != nil {
		return nil, fmt.Errorf("get messages: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var m Message
		if err := rows.Scan(&m.ID, &m.Role, &m.Content, &m.RunID, &m.CreatedAt); err != nil {
			return nil, err
		}
		c.Messages = append(c.Messages, m)
	}
	return c, rows.Err()
}

func DeleteConversation(db *sql.DB, id string) error {
	res, err := db.Exec(`DELETE FROM conversations WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete conversation: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func AppendUserMessage(db *sql.DB, convID, content string) error {
	now := time.Now()
	_, err := db.Exec(`INSERT INTO messages (conversation_id, role, content, created_at) VALUES (?, 'user', ?, ?)`,
		convID, content, now)
	if err != nil {
		return fmt.Errorf("insert user message: %w", err)
	}
	_, err = db.Exec(`UPDATE conversations SET updated_at = ? WHERE id = ?`, now, convID)
	return err
}

func AppendAssistantMessage(db *sql.DB, convID, runID, content string) error {
	now := time.Now()
	_, err := db.Exec(`INSERT INTO messages (conversation_id, role, content, run_id, created_at) VALUES (?, 'assistant', ?, ?, ?)`,
		convID, content, runID, now)
	if err != nil {
		return fmt.Errorf("insert assistant message: %w", err)
	}
	_, err = db.Exec(`UPDATE conversations SET updated_at = ? WHERE id = ?`, now, convID)
	return err
}

func ConversationHistory(db *sql.DB, convID string) ([]Message, error) {
	rows, err := db.Query(`SELECT id, role, content, COALESCE(run_id, ''), created_at FROM messages WHERE conversation_id = ? ORDER BY id ASC`, convID)
	if err != nil {
		return nil, fmt.Errorf("history: %w", err)
	}
	defer rows.Close()
	var out []Message
	for rows.Next() {
		var m Message
		if err := rows.Scan(&m.ID, &m.Role, &m.Content, &m.RunID, &m.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func SetActiveRun(db *sql.DB, convID, runID string) error {
	now := time.Now()
	_, err := db.Exec(`UPDATE conversations SET active_run_id = ?, updated_at = ? WHERE id = ?`, runID, now, convID)
	return err
}

func ClearActiveRun(db *sql.DB, convID string) error {
	now := time.Now()
	_, err := db.Exec(`UPDATE conversations SET active_run_id = NULL, updated_at = ? WHERE id = ?`, now, convID)
	return err
}

func SetTitle(db *sql.DB, convID, title string) error {
	_, err := db.Exec(`UPDATE conversations SET title = ? WHERE id = ?`, title, convID)
	return err
}

func UserMessageCount(db *sql.DB, convID string) (int, error) {
	var n int
	err := db.QueryRow(`SELECT COUNT(*) FROM messages WHERE conversation_id = ? AND role = 'user'`, convID).Scan(&n)
	return n, err
}

func ConversationByActiveRun(db *sql.DB, runID string) (string, error) {
	var convID string
	err := db.QueryRow(`SELECT id FROM conversations WHERE active_run_id = ?`, runID).Scan(&convID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return convID, err
}

func ActiveRuns(db *sql.DB) (map[string]string, error) {
	rows, err := db.Query(`SELECT id, active_run_id FROM conversations WHERE active_run_id IS NOT NULL`)
	if err != nil {
		return nil, fmt.Errorf("active runs: %w", err)
	}
	defer rows.Close()
	out := make(map[string]string)
	for rows.Next() {
		var convID, runID string
		if err := rows.Scan(&convID, &runID); err != nil {
			return nil, err
		}
		out[convID] = runID
	}
	return out, rows.Err()
}