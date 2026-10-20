package events

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/iamangus/eve/internal/durable"
)

func (q *Queue) archivePath(source, id string) string {
	sum := sha256.Sum256([]byte(eventKey(source, id)))
	return filepath.Join(q.dir, "completed", hex.EncodeToString(sum[:])+".json")
}

func (q *Queue) archived(source, id string) (*Event, error) {
	data, err := os.ReadFile(q.archivePath(source, id))
	if err != nil {
		return nil, err
	}
	var event Event
	if err := json.Unmarshal(data, &event); err != nil {
		return nil, err
	}
	if event.Source != source || event.ID != id || event.State != StateDone || event.PayloadDigest == "" {
		return nil, errors.New("invalid completed event archive")
	}
	return &event, nil
}

func (q *Queue) ArchiveCompleted(before time.Time) (int, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	selected := make(map[string]*Event)
	for key, event := range q.events {
		if event.State == StateDone && event.UpdatedAt.Before(before) {
			selected[key] = event
		}
	}
	if len(selected) == 0 {
		return 0, nil
	}
	if err := os.MkdirAll(filepath.Join(q.dir, "completed"), 0o700); err != nil {
		return 0, err
	}
	for _, event := range selected {
		if err := durable.JSON(q.archivePath(event.Source, event.ID), event); err != nil {
			return 0, err
		}
	}
	for key := range selected {
		delete(q.events, key)
	}
	if err := q.saveLocked(); err != nil {
		for key, event := range selected {
			q.events[key] = event
		}
		return 0, err
	}
	return len(selected), nil
}

func (q *Queue) ExpireCompleted(before time.Time) (int, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	directory := filepath.Join(q.dir, "completed")
	entries, err := os.ReadDir(directory)
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	removed := 0
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		path := filepath.Join(directory, entry.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			return removed, err
		}
		var event Event
		if err := json.Unmarshal(data, &event); err != nil {
			return removed, err
		}
		if event.State != StateDone || event.UpdatedAt.IsZero() || !event.UpdatedAt.Before(before) {
			continue
		}
		if err := os.Remove(path); err != nil {
			return removed, err
		}
		removed++
	}
	if removed > 0 {
		dir, err := os.Open(directory)
		if err != nil {
			return removed, err
		}
		defer dir.Close()
		return removed, dir.Sync()
	}
	return removed, nil
}
