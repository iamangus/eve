package store

import (
	"bytes"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"time"
)

var ErrNotFound = errors.New("conversation not found")

type Message struct {
	ID        int64     `json:"id"`
	Role      string    `json:"role"`
	Content   string    `json:"content"`
	RunID     string    `json:"run_id,omitempty"`
	Channel   string    `json:"channel,omitempty"`
	Sender    string    `json:"sender,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

type Conversation struct {
	ID            string    `json:"id"`
	Title         string    `json:"title"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
	ActiveRunID   string    `json:"active_run_id,omitempty"`
	SummarizedUpTo int64    `json:"summarized_up_to,omitempty"`
	Messages      []Message `json:"messages,omitempty"`
}

type ConversationSummary struct {
	ID           string    `json:"id"`
	Title        string    `json:"title"`
	UpdatedAt    time.Time `json:"updated_at"`
	MessageCount int       `json:"message_count"`
	ActiveRunID  string    `json:"active_run_id,omitempty"`
}

type conversationRecord struct {
	Title          string    `json:"title"`
	Created        time.Time `json:"created"`
	Updated        time.Time `json:"updated"`
	ActiveRunID    string    `json:"active_run_id,omitempty"`
	Messages       []Message `json:"messages"`
	NextMsgID      int64     `json:"next_msg_id"`
	Participants   []string  `json:"participants,omitempty"`
	HistorianRunAt time.Time `json:"historian_run_at,omitempty"`
	HistorianError string    `json:"historian_error,omitempty"`
}

// Store is a JSON-file-backed conversation store. It survives process
// restarts and is safe for concurrent use. All durable state lives in the
// configured data directory as conversations.json, compartments.json, and
// memories.json, written atomically (tmp file + rename).
type Store struct {
	mu           sync.RWMutex
	dir          string
	convs        map[string]*conversationRecord
	compartments map[string][]Compartment
	memories     []Memory
}

func New(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	s := &Store{
		dir:          dir,
		convs:        make(map[string]*conversationRecord),
		compartments: make(map[string][]Compartment),
	}
	if err := s.load(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Store) load() error {
	// conversations.json is a map keyed by conversation ID.
	var raw map[string]*conversationRecord
	if err := s.loadJSON("conversations.json", &raw); err != nil {
		return err
	}
	s.convs = make(map[string]*conversationRecord, len(raw))
	for id, r := range raw {
		if r == nil {
			r = &conversationRecord{}
		}
		s.convs[id] = r
	}
	var comps map[string][]Compartment
	if err := s.loadJSON("compartments.json", &comps); err != nil {
		return err
	}
	if comps == nil {
		comps = make(map[string][]Compartment)
	}
	s.compartments = comps
	var mems []Memory
	if err := s.loadJSON("memories.json", &mems); err != nil {
		return err
	}
	s.memories = mems
	return nil
}

func (s *Store) loadJSON(name string, v any) error {
	data, err := os.ReadFile(filepath.Join(s.dir, name))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return nil
	}
	return json.Unmarshal(data, v)
}

func (s *Store) saveJSON(name string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	tmp := filepath.Join(s.dir, name+".tmp")
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(s.dir, name))
}

func (s *Store) saveConversations() error {
	return s.saveJSON("conversations.json", s.convs)
}
