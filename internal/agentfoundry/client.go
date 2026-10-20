package agentfoundry

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
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
	Message        string          `json:"message"`
	History        []Message       `json:"history,omitempty"`
	TaskID         string          `json:"task_id,omitempty"`
	MCPServers     []MCPServer     `json:"mcp_servers,omitempty"`
	ResponseSchema *ResponseSchema `json:"response_schema,omitempty"`
}

// MCPServer is an ephemeral MCP server attached to a single run. Agentfoundry
// dials it when the run starts so the agent can call the tools it exposes.
type MCPServer struct {
	Name      string `json:"name"`
	URL       string `json:"url"`
	Transport string `json:"transport"`
}

// ResponseSchema constrains the run's reply to a JSON Schema (OpenAI
// json_schema structured output).
type ResponseSchema struct {
	Name   string          `json:"name"`
	Schema json.RawMessage `json:"schema"`
	Strict bool            `json:"strict,omitempty"`
}

type runResponse struct {
	RunID string `json:"run_id"`
}

type persistentRunRequest struct {
	MCPServers []MCPServer `json:"mcp_servers,omitempty"`
	ClientKey  string      `json:"client_key,omitempty"`
	History    []Message   `json:"history,omitempty"`
}

type runInputRequest struct {
	Message  string            `json:"message"`
	InputID  string            `json:"input_id,omitempty"`
	Metadata map[string]string `json:"metadata,omitempty"`
}

type RunStatus struct {
	ID             string `json:"id"`
	Status         string `json:"status"`
	Response       string `json:"response"`
	Error          string `json:"error"`
	Persistent     bool   `json:"persistent"`
	CurrentInputID string `json:"current_input_id"`
	LastInputID    string `json:"last_input_id"`
}

type Client struct {
	baseURL    *url.URL
	httpClient *http.Client
	apiKey     string
}

// HTTPError retains an AgentFoundry response status for callers that can
// safely recover from a specific API condition.
type HTTPError struct {
	StatusCode int
	Status     string
	Body       string
}

func (e *HTTPError) Error() string {
	return fmt.Sprintf("agentfoundry: %s: %s", e.Status, e.Body)
}

func IsNotFound(err error) bool {
	var apiErr *HTTPError
	return errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusNotFound
}

func IsInvalidRequest(err error) bool {
	var apiErr *HTTPError
	if !errors.As(err, &apiErr) {
		return false
	}
	switch apiErr.StatusCode {
	case http.StatusBadRequest, http.StatusNotFound, http.StatusUnprocessableEntity, http.StatusRequestEntityTooLarge:
		return true
	}
	return false
}

// IsStaleRun reports whether AgentFoundry rejected a run reference because the
// run no longer exists or is no longer active, for example after a server
// restart or when the run failed. Callers should recreate the run.
func IsStaleRun(err error) bool {
	var apiErr *HTTPError
	if !errors.As(err, &apiErr) {
		return false
	}
	return apiErr.StatusCode == http.StatusNotFound ||
		(apiErr.StatusCode == http.StatusConflict && (strings.Contains(apiErr.Body, "run is not active") || strings.Contains(apiErr.Body, "run is not running")))
}

func NewClient(baseURL, apiKey string) (*Client, error) {
	u, err := url.Parse(baseURL)
	if err != nil {
		return nil, fmt.Errorf("parse agentfoundry URL: %w", err)
	}
	if (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil {
		return nil, fmt.Errorf("agentfoundry URL must be an absolute HTTP(S) URL without user credentials")
	}
	return &Client{
		baseURL:    u,
		httpClient: &http.Client{Timeout: 30 * time.Second},
		apiKey:     apiKey,
	}, nil
}

func (c *Client) RunAgent(ctx context.Context, agentID, message string, history []Message) (string, error) {
	return c.RunAgentWith(ctx, agentID, RunOptions{Message: message, History: history})
}

// RunOptions configures a run beyond message+history: ephemeral MCP servers,
// a response schema, and an optional task id used to rediscover task runs
// after a restart.
type RunOptions struct {
	Message        string
	History        []Message
	MCPServers     []MCPServer
	ResponseSchema *ResponseSchema
	TaskID         string
}

type PersistentRunOptions struct {
	MCPServers []MCPServer
	ClientKey  string
	History    []Message
}

func (c *Client) CreatePersistentRun(ctx context.Context, agentID string, opts PersistentRunOptions) (string, error) {
	data, err := json.Marshal(persistentRunRequest{MCPServers: opts.MCPServers, ClientKey: opts.ClientKey, History: opts.History})
	if err != nil {
		return "", fmt.Errorf("marshal persistent run request: %w", err)
	}
	u := c.baseURL.JoinPath("/api/v1/agents/" + url.PathEscape(agentID) + "/runs")
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
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return "", &HTTPError{StatusCode: resp.StatusCode, Status: resp.Status, Body: string(b)}
	}
	var out runResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", fmt.Errorf("decode persistent run response: %w", err)
	}
	if out.RunID == "" {
		return "", fmt.Errorf("persistent run response has no run_id")
	}
	return out.RunID, nil
}

// SendPersistentRunInput appends an input to a persistent run. inputID is an
// idempotency key for durable callers; it may be empty for direct UI input.
func (c *Client) SendPersistentRunInput(ctx context.Context, runID, message, inputID string) (string, error) {
	return c.SendPersistentRunInputWithMetadata(ctx, runID, message, inputID, nil)
}

// SendPersistentRunInputWithMetadata includes optional context about a message
// that may be injected after an agent has already started work.
func (c *Client) SendPersistentRunInputWithMetadata(ctx context.Context, runID, message, inputID string, metadata map[string]string) (string, error) {
	data, err := json.Marshal(runInputRequest{Message: message, InputID: inputID, Metadata: metadata})
	if err != nil {
		return "", fmt.Errorf("marshal run input: %w", err)
	}
	u := c.baseURL.JoinPath("/api/v1/runs/" + url.PathEscape(runID) + "/inputs")
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
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return "", &HTTPError{StatusCode: resp.StatusCode, Status: resp.Status, Body: string(b)}
	}
	var out runResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", fmt.Errorf("decode run input response: %w", err)
	}
	if out.RunID == "" {
		out.RunID = runID
	}
	return out.RunID, nil
}

func (c *Client) RunAgentWith(ctx context.Context, agentID string, opts RunOptions) (string, error) {
	body := runRequest{
		Message:        opts.Message,
		History:        opts.History,
		TaskID:         opts.TaskID,
		MCPServers:     opts.MCPServers,
		ResponseSchema: opts.ResponseSchema,
	}
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
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return "", &HTTPError{StatusCode: resp.StatusCode, Status: resp.Status, Body: string(b)}
	}
	var out runResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", fmt.Errorf("decode run response: %w", err)
	}
	if out.RunID == "" {
		return "", fmt.Errorf("agentfoundry run response has no run_id")
	}
	return out.RunID, nil
}

func (c *Client) GetRun(ctx context.Context, runID string) (*RunStatus, error) {
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
		return &RunStatus{Status: "unknown"}, nil
	}
	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, &HTTPError{StatusCode: resp.StatusCode, Status: resp.Status, Body: string(b)}
	}
	var rs RunStatus
	if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&rs); err != nil {
		return nil, fmt.Errorf("decode run status: %w", err)
	}
	return &rs, nil
}

func (c *Client) GetRunInputStatus(ctx context.Context, runID, inputID string) (string, error) {
	u := c.baseURL.JoinPath("/api/v1/runs/" + url.PathEscape(runID) + "/inputs/" + url.PathEscape(inputID))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return "", err
	}
	c.withAuth(req)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return "", nil
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return "", fmt.Errorf("agentfoundry input status: %s: %s", resp.Status, body)
	}
	var status struct {
		Status string `json:"status"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&status); err != nil {
		return "", err
	}
	return status.Status, nil
}

// CancelRun cancels a running agentfoundry run (terminates its workflow).
func (c *Client) CancelRun(ctx context.Context, runID string) error {
	u := c.baseURL.JoinPath("/api/v1/runs/" + url.PathEscape(runID) + "/cancel")
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), nil)
	if err != nil {
		return err
	}
	c.withAuth(req)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return &HTTPError{StatusCode: resp.StatusCode, Status: resp.Status, Body: string(b)}
	}
	return nil
}

// FindRunByTaskID returns the agentfoundry run associated with a background
// task id, or nil when none exists. Used to rediscover in-flight task runs
// after an eve restart.
func (c *Client) FindRunByTaskID(ctx context.Context, taskID string) (*RunStatus, error) {
	u := c.baseURL.JoinPath("/api/v1/runs")
	q := u.Query()
	q.Set("task_id", taskID)
	u.RawQuery = q.Encode()
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
		return nil, nil
	}
	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, &HTTPError{StatusCode: resp.StatusCode, Status: resp.Status, Body: string(b)}
	}
	var rs RunStatus
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
	streamClient := *c.httpClient
	streamClient.Timeout = 0
	resp, err := streamClient.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
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
			// A completed automation run may have no final text after a tool
			// call. Its empty-data done event is still the completion signal.
			if eventType != "" {
				select {
				case ch <- SSEEvent{Type: eventType, Data: dataLines}:
				case <-ctx.Done():
				}
			}
			eventType = ""
			dataLines = ""
		}
		for scanner.Scan() {
			if ctx.Err() != nil {
				return
			}
			line := scanner.Text()
			switch {
			case line == "":
				flush()
			case strings.HasPrefix(line, "id:"):
			case strings.HasPrefix(line, "event:"):
				eventType = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
			case strings.HasPrefix(line, "data:"):
				d := strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " ")
				if dataLines == "" {
					dataLines = d
				} else {
					dataLines += "\n" + d
				}
			}
		}
		flush()
		if err := scanner.Err(); err != nil && ctx.Err() == nil {
			select {
			case ch <- SSEEvent{Type: "error", Data: err.Error()}:
			case <-ctx.Done():
			}
		}
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
