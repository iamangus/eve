package io

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// matrixSyncResponse is the subset of the /sync response we care about:
// the next_batch token and room timeline events.
type matrixSyncResponse struct {
	NextBatch string `json:"next_batch"`
	Rooms     struct {
		Join map[string]struct {
			Timeline struct {
				Events []matrixEvent `json:"events"`
			} `json:"timeline"`
		} `json:"join"`
	} `json:"rooms"`
}

type matrixEvent struct {
	Type    string          `json:"type"`
	RoomID  string          `json:"room_id"`
	EventID string          `json:"event_id"`
	Sender  string          `json:"sender"`
	Content map[string]any `json:"content"`
}

// MatrixPoller long-polls the matrix sync API and forwards timeline m.room.message
// events to the manager's inbound path. It keeps a cursor file in DataDir so
// the since token survives restarts (a message is ingested at most once).
type MatrixPoller struct {
	cfg    MatrixConfig
	manager *Manager
	dataDir string
	cursor string
}

// NewMatrixPoller returns a sync poller for the configured matrix channel.
func NewMatrixPoller(cfg MatrixConfig, m *Manager, dataDir string) *MatrixPoller {
	return &MatrixPoller{cfg: cfg, manager: m, dataDir: dataDir}
}

// Run blocks until ctx is cancelled, long-polling /sync with a 30s timeout.
func (p *MatrixPoller) Run(ctx context.Context) {
	p.loadCursor()
	if p.cursor == "" {
		// First sync without a cursor returns all state — we only care about
		// messages from here on, so start with a full sync to seed the token.
		if _, err := p.sync(ctx, ""); err != nil {
			slog.Warn("matrix sync initial", "error", err)
		}
	}
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			next, err := p.sync(ctx, p.cursor)
			p.manager.RecordPollHealth("matrix", err)
			if err != nil {
				slog.Warn("matrix sync", "error", err)
				continue
			}
			if next != "" && next != p.cursor {
				p.cursor = next
				p.saveCursor()
			}
		}
	}
}

// sync performs one /sync call and forwards any new message events.
func (p *MatrixPoller) sync(ctx context.Context, since string) (string, error) {
	endpoint := strings.TrimSuffix(p.cfg.Homeserver, "/") + "/_matrix/client/v3/sync"
	q := url.Values{}
	q.Set("timeout", "25000")
	if since != "" {
		q.Set("since", since)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint+"?"+q.Encode(), nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+p.cfg.AccessToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		var er struct {
			Error string `json:"error"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&er)
		return "", fmt.Errorf("sync %s: %s", resp.Status, er.Error)
	}
	var sr matrixSyncResponse
	if err := json.NewDecoder(resp.Body).Decode(&sr); err != nil {
		return "", err
	}
	for roomID, room := range sr.Rooms.Join {
		for _, ev := range room.Timeline.Events {
			if ev.Type != "m.room.message" || ev.Sender == p.cfg.UserID {
				continue
			}
			if ev.RoomID == "" {
				ev.RoomID = roomID
			}
			body := ""
			if b, ok := ev.Content["body"].(string); ok {
				body = b
			}
			text := strings.TrimSpace(body)
			if text == "" {
				continue
			}
			if _, err := p.manager.Inbound(ctx, InboundMessage{
				Channel:   ChannelMatrix,
				Sender:    ev.Sender,
				Text:      text,
				ThreadRef: ev.RoomID,
			}); err != nil {
				slog.Warn("matrix inbound", "room", ev.RoomID, "sender", ev.Sender, "error", err)
			}
		}
	}
	return sr.NextBatch, nil
}

func (p *MatrixPoller) loadCursor() {
	p.cursor = loadCursorFile(p.dataDir, "matrix_since.txt")
}

func (p *MatrixPoller) saveCursor() {
	if err := saveCursorFile(p.dataDir, "matrix_since.txt", p.cursor); err != nil {
		slog.Warn("matrix cursor", "error", err)
	}
}

func loadCursorFile(dir, name string) string {
	path := dir + "/" + name
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

func saveCursorFile(dir, name, value string) error {
	return os.WriteFile(dir+"/"+name, []byte(value), 0o644)
}
