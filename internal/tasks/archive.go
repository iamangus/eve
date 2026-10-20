package tasks

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/iamangus/eve/internal/durable"
)

func (s *Store) archivePath(id string) (string, error) {
	if id == "" || len(id) > 256 {
		return "", ErrNotFound
	}
	for _, char := range id {
		if !(char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= '0' && char <= '9' || char == '-' || char == '_') {
			return "", ErrNotFound
		}
	}
	return filepath.Join(s.dir, "tasks-archive", id+".json"), nil
}

func (s *Store) archived(id string) (Task, error) {
	path, err := s.archivePath(id)
	if err != nil {
		return Task{}, err
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return Task{}, ErrNotFound
	}
	if err != nil {
		return Task{}, err
	}
	var task Task
	if err := json.Unmarshal(data, &task); err != nil {
		return Task{}, err
	}
	task.Archived = true
	return task, nil
}

func (s *Store) Archive(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	task, ok := s.tasks[id]
	if !ok {
		_, err := s.archived(id)
		return err
	}
	if !task.Terminal() || !task.Reported || task.CancellationPending {
		return fmt.Errorf("only reported terminal tasks without pending cancellation may be archived")
	}
	path, err := s.archivePath(id)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	if err := durable.JSON(path, task); err != nil {
		return err
	}
	delete(s.tasks, id)
	if err := s.save(); err != nil {
		s.tasks[id] = task
		return err
	}
	return nil
}

func (s *Store) ListArchived(offset, limit int) ([]Task, error) {
	entries, err := os.ReadDir(filepath.Join(s.dir, "tasks-archive"))
	if errors.Is(err, os.ErrNotExist) {
		return []Task{}, nil
	}
	if err != nil {
		return nil, err
	}
	out := make([]Task, 0)
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		if offset > 0 {
			offset--
			continue
		}
		task, err := s.archived(strings.TrimSuffix(entry.Name(), ".json"))
		if err != nil {
			return nil, err
		}
		out = append(out, task)
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}
