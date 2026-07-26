package agentfoundry

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type Message struct {
	Role    string `json:"role"`
	Content any    `json:"content,omitempty"`
}

type runRequest struct {
	Message string   `json:"message"`
	History []Message `json:"history,omitempty"`
}

type runResponse struct {
	RunID string `json:"run_id"`
}

type runStatus struct {
	Status   string `json:"status"`
	Response string `json:"response"`
	Error    string `json:"error"`
}

type Client struct {
	baseURL    *url.URL
	httpClient *http.Client
	apiKey     string
}

func NewClient(baseURL, apiKey string) (*Client, error) {
	u, err := url.Parse(baseURL)
	if err != nil {
		return nil, fmt.Errorf("parse agentfoundry URL: %w", err)
	}
	return &Client{
		baseURL:    u,
		httpClient: &http.Client{Timeout: 0},
		apiKey:     apiKey,
	}, nil
}

func (c *Client) RunAgent(ctx context.Context, agentID, message string, history []Message) (string, error) {
	body := runRequest{Message: message, History: history}
	data, err := json.Marshal(body)
	if err != nil {
		return "", fmt.Errorf("marshal run request: %w", err)
	}
	u := c.baseURL.JoinPath("/api/v1/agents/" + url.PathEscape(agentID) + "/run")
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), bytes.NewReader(data))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	c.withAuth(req)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("agentfoundry run: %s: %s", resp.Status, string(b))
	}
	var out runResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", fmt.Errorf("decode run response: %w", err)
	}
	return out.RunID, nil
}

func (c *Client) GetRun(ctx context.Context, runID string) (*runStatus, error) {
	u := c.baseURL.JoinPath("/api/v1/runs/" + url.PathEscape(runID))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	c.withAuth(req)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return &runStatus{Status: "unknown"}, nil
	}
	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("agentfoundry get run: %s: %s", resp.Status, string(b))
	}
	var rs runStatus
	if err := json.NewDecoder(resp.Body).Decode(&rs); err != nil {
		return nil, fmt.Errorf("decode run status: %w", err)
	}
	return &rs, nil
}

func (c *Client) StreamRunEvents(ctx context.Context, runID string) (io.ReadCloser, error) {
	u := c.baseURL.JoinPath("/api/v1/runs/" + url.PathEscape(runID) + "/events")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "text/event-stream")
	c.withAuth(req)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return nil, fmt.Errorf("agentfoundry SSE: %s: %s", resp.Status, string(b))
	}
	return resp.Body, nil
}

type SSEEvent struct {
	Type string
	Data string
}

func (c *Client) StreamRunEventsChan(ctx context.Context, runID string) (<-chan SSEEvent, error) {
	body, err := c.StreamRunEvents(ctx, runID)
	if err != nil {
		return nil, err
	}
	ch := make(chan SSEEvent, 64)
	go func() {
		defer body.Close()
		defer close(ch)
		scanner := bufio.NewScanner(body)
		scanner.Buffer(make([]byte, 1024*1024), 1024*1024)
		var eventType, dataLines string
		flush := func() {
			if eventType != "" && dataLines != "" {
				ch <- SSEEvent{Type: eventType, Data: dataLines}
			}
			eventType = ""
			dataLines = ""
		}
		for scanner.Scan() {
			line := scanner.Text()
			switch {
			case line == "":
				flush()
			case strings.HasPrefix(line, "id:"):
			case strings.HasPrefix(line, "event:"):
				eventType = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
			case strings.HasPrefix(line, "data:"):
				d := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
				if dataLines == "" {
					dataLines = d
				} else {
					dataLines += "\n" + d
				}
			}
		}
		flush()
	}()
	return ch, nil
}

func (c *Client) AwaitRunText(ctx context.Context, runID string, timeout time.Duration) (string, error) {
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	ch, err := c.StreamRunEventsChan(cctx, runID)
	if err != nil {
		return "", err
	}
	for ev := range ch {
		switch ev.Type {
		case "done":
			return ev.Data, nil
		case "error":
			return "", fmt.Errorf("run error: %s", ev.Data)
		}
	}
	return "", fmt.Errorf("run ended without done event")
}

func (c *Client) withAuth(req *http.Request) {
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}
}