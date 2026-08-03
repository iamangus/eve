package store

import (
	"bytes"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

const maxStoredRuns = 1000

// maxProcessed bounds the dedup set of processed message IDs. Excess entries
// are evicted oldest-first.
const maxProcessed = 10000

type Account struct {
	ID        string    `json:"id"`
	Address   string    `json:"address"`
	Host      string    `json:"host"`
	Port      int       `json:"port"`
	Username  string    `json:"username"`
	Password  string    `json:"password"`
	UseTLS    bool      `json:"use_tls"`
	LastUID   uint32    `json:"last_uid,omitempty"`
	Enabled   bool      `json:"enabled"`
	CreatedAt time.Time `json:"created_at"`
}

type Filter struct {
	Field    string `json:"field"`
	Contains string `json:"contains"`
}

type Trigger struct {
	ID        string    `json:"id"`
	AccountID string    `json:"account_id"`
	Name      string    `json:"name"`
	Filters   []Filter  `json:"filters"`
	Prompt    string    `json:"prompt"`
	Enabled   bool      `json:"enabled"`
	CreatedAt time.Time `json:"created_at"`
}

type EmailMessage struct {
	UID       uint32    `json:"uid,omitempty"`
	From      string    `json:"from"`
	To        string    `json:"to"`
	Subject   string    `json:"subject"`
	Date      time.Time `json:"date"`
	Body      string    `json:"body"`
	MessageID string    `json:"message_id,omitempty"`
}

type TriggerRun struct {
	ID         string       `json:"id"`
	TriggerID  string       `json:"trigger_id"`
	AccountID  string       `json:"account_id"`
	Email      EmailMessage `json:"email"`
	Prompt     string       `json:"prompt"`
	AgentRunID string       `json:"agent_run_id,omitempty"`
	Status     string       `json:"status"`
	Result     string       `json:"result,omitempty"`
	Error      string       `json:"error,omitempty"`
	CreatedAt  time.Time    `json:"created_at"`
}

// EmailStore is a JSON-file-backed store for email accounts, triggers, and
// trigger runs. It survives process restarts and is safe for concurrent use.
type EmailStore struct {
	mu           sync.Mutex
	dir          string
	accounts     []Account
	triggers     []Trigger
	runs         []TriggerRun
	processed    []string
	processedIdx map[string]bool
}

func NewEmailStore(dir string) (*EmailStore, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	s := &EmailStore{dir: dir}
	if err := s.load(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *EmailStore) load() error {
	var accounts []Account
	if err := s.loadJSON("accounts.json", &accounts); err != nil {
		return err
	}
	var triggers []Trigger
	if err := s.loadJSON("triggers.json", &triggers); err != nil {
		return err
	}
	var runs []TriggerRun
	if err := s.loadJSON("runs.json", &runs); err != nil {
		return err
	}
	s.accounts = accounts
	s.triggers = triggers
	s.runs = runs
	if err := s.loadProcessed(); err != nil {
		return err
	}
	return nil
}

func (s *EmailStore) loadJSON(name string, v any) error {
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

func (s *EmailStore) saveJSON(name string, v any) error {
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

func (s *EmailStore) saveAll() error {
	if err := s.saveJSON("accounts.json", s.accounts); err != nil {
		return err
	}
	if err := s.saveJSON("triggers.json", s.triggers); err != nil {
		return err
	}
	return s.saveJSON("runs.json", s.runs)
}

func findAccount(accounts []Account, id string) int {
	for i, a := range accounts {
		if a.ID == id {
			return i
		}
	}
	return -1
}

func findTrigger(triggers []Trigger, id string) int {
	for i, t := range triggers {
		if t.ID == id {
			return i
		}
	}
	return -1
}

func findRun(runs []TriggerRun, id string) int {
	for i, r := range runs {
		if r.ID == id {
			return i
		}
	}
	return -1
}

func (s *EmailStore) Accounts() []Account {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Account(nil), s.accounts...)
}

func (s *EmailStore) EnabledAccounts() []Account {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Account, 0, len(s.accounts))
	for _, a := range s.accounts {
		if a.Enabled {
			out = append(out, a)
		}
	}
	return out
}

func (s *EmailStore) GetAccount(id string) (Account, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if i := findAccount(s.accounts, id); i >= 0 {
		return s.accounts[i], nil
	}
	return Account{}, ErrNotFound
}

func (s *EmailStore) AddAccount(a Account) (Account, error) {
	if a.ID == "" {
		a.ID = newID()
	}
	if a.CreatedAt.IsZero() {
		a.CreatedAt = time.Now()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.accounts = append(s.accounts, a)
	if err := s.saveJSON("accounts.json", s.accounts); err != nil {
		return Account{}, err
	}
	return a, nil
}

func (s *EmailStore) UpdateAccount(a Account) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	i := findAccount(s.accounts, a.ID)
	if i < 0 {
		return ErrNotFound
	}
	s.accounts[i] = a
	return s.saveJSON("accounts.json", s.accounts)
}

func (s *EmailStore) SetAccountLastUID(id string, uid uint32) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	i := findAccount(s.accounts, id)
	if i < 0 {
		return ErrNotFound
	}
	if uid <= s.accounts[i].LastUID {
		return nil
	}
	s.accounts[i].LastUID = uid
	return s.saveJSON("accounts.json", s.accounts)
}

// DeleteAccount removes an account along with its triggers and their runs.
func (s *EmailStore) DeleteAccount(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if i := findAccount(s.accounts, id); i < 0 {
		return ErrNotFound
	} else {
		s.accounts = append(s.accounts[:i], s.accounts[i+1:]...)
	}

	removed := make(map[string]bool)
	keptTriggers := s.triggers[:0]
	for _, t := range s.triggers {
		if t.AccountID == id {
			removed[t.ID] = true
			continue
		}
		keptTriggers = append(keptTriggers, t)
	}
	s.triggers = keptTriggers

	keptRuns := s.runs[:0]
	for _, r := range s.runs {
		if r.AccountID == id || removed[r.TriggerID] {
			continue
		}
		keptRuns = append(keptRuns, r)
	}
	s.runs = keptRuns

	return s.saveAll()
}

func (s *EmailStore) Triggers() []Trigger {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Trigger(nil), s.triggers...)
}

func (s *EmailStore) GetTrigger(id string) (Trigger, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if i := findTrigger(s.triggers, id); i >= 0 {
		return s.triggers[i], nil
	}
	return Trigger{}, ErrNotFound
}

func (s *EmailStore) EnabledTriggersForAccount(accountID string) ([]Trigger, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Trigger
	for _, t := range s.triggers {
		if t.AccountID == accountID && t.Enabled {
			out = append(out, t)
		}
	}
	return out, nil
}

func (s *EmailStore) AddTrigger(t Trigger) (Trigger, error) {
	if t.ID == "" {
		t.ID = newID()
	}
	if t.CreatedAt.IsZero() {
		t.CreatedAt = time.Now()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.triggers = append(s.triggers, t)
	if err := s.saveJSON("triggers.json", s.triggers); err != nil {
		return Trigger{}, err
	}
	return t, nil
}

func (s *EmailStore) UpdateTrigger(t Trigger) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	i := findTrigger(s.triggers, t.ID)
	if i < 0 {
		return ErrNotFound
	}
	s.triggers[i] = t
	return s.saveJSON("triggers.json", s.triggers)
}

func (s *EmailStore) DeleteTrigger(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if i := findTrigger(s.triggers, id); i < 0 {
		return ErrNotFound
	} else {
		s.triggers = append(s.triggers[:i], s.triggers[i+1:]...)
	}

	keptRuns := s.runs[:0]
	for _, r := range s.runs {
		if r.TriggerID == id {
			continue
		}
		keptRuns = append(keptRuns, r)
	}
	s.runs = keptRuns

	return s.saveAll()
}

func (s *EmailStore) Runs() []TriggerRun {
	s.mu.Lock()
	defer s.mu.Unlock()
	return sortRuns(s.runs)
}

func (s *EmailStore) RunsForTrigger(triggerID string) []TriggerRun {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []TriggerRun
	for _, r := range s.runs {
		if r.TriggerID == triggerID {
			out = append(out, r)
		}
	}
	return sortRuns(out)
}

func sortRuns(runs []TriggerRun) []TriggerRun {
	out := append([]TriggerRun(nil), runs...)
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].ID > out[j].ID
		}
		return out[i].CreatedAt.After(out[j].CreatedAt)
	})
	return out
}

func (s *EmailStore) GetRun(id string) (TriggerRun, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if i := findRun(s.runs, id); i >= 0 {
		return s.runs[i], nil
	}
	return TriggerRun{}, ErrNotFound
}

func (s *EmailStore) AddRun(r TriggerRun) (TriggerRun, error) {
	if r.ID == "" {
		r.ID = newID()
	}
	if r.CreatedAt.IsZero() {
		r.CreatedAt = time.Now()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.runs = append(s.runs, r)
	if len(s.runs) > maxStoredRuns {
		s.runs = append([]TriggerRun(nil), s.runs[len(s.runs)-maxStoredRuns:]...)
	}
	if err := s.saveJSON("runs.json", s.runs); err != nil {
		return TriggerRun{}, err
	}
	return r, nil
}

func (s *EmailStore) UpdateRun(r TriggerRun) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	i := findRun(s.runs, r.ID)
	if i < 0 {
		return ErrNotFound
	}
	s.runs[i] = r
	return s.saveJSON("runs.json", s.runs)
}

func processedKey(accountID, messageID string) string {
	return accountID + "|" + messageID
}

func (s *EmailStore) loadProcessed() error {
	var processed []string
	if err := s.loadJSON("processed.json", &processed); err != nil {
		return err
	}
	s.processed = processed
	s.processedIdx = make(map[string]bool, len(processed))
	for _, k := range processed {
		s.processedIdx[k] = true
	}
	return nil
}

func (s *EmailStore) saveProcessed() error {
	return s.saveJSON("processed.json", s.processed)
}

// IsProcessed reports whether a message (identified by its account and
// Message-ID) has already been handled, enabling at-most-once delivery.
func (s *EmailStore) IsProcessed(accountID, messageID string) bool {
	if messageID == "" {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.processedIdx[processedKey(accountID, messageID)]
}

// MarkProcessed records a message as handled so it is not processed again.
func (s *EmailStore) MarkProcessed(accountID, messageID string) error {
	if messageID == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	k := processedKey(accountID, messageID)
	if s.processedIdx[k] {
		return nil
	}
	s.processed = append(s.processed, k)
	s.processedIdx[k] = true
	if len(s.processed) > maxProcessed {
		overflow := len(s.processed) - maxProcessed
		for i := 0; i < overflow; i++ {
			delete(s.processedIdx, s.processed[i])
		}
		s.processed = append([]string(nil), s.processed[overflow:]...)
	}
	return s.saveProcessed()
}
