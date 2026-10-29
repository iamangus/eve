package agentfoundry

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
)

func (c *Client) RunInference(ctx context.Context, runID, inputID, cursor string) (json.RawMessage, error) {
	return c.inferenceJSON(ctx, "/api/v1/runs/"+url.PathEscape(runID)+"/inference", url.Values{"input_id": {inputID}, "cursor": {cursor}})
}

func (c *Client) ProviderInference(ctx context.Context, providerID string) (json.RawMessage, error) {
	return c.inferenceJSON(ctx, "/api/v1/inference/providers", url.Values{"provider_id": {providerID}})
}

func (c *Client) inferenceJSON(ctx context.Context, path string, query url.Values) (json.RawMessage, error) {
	u := c.baseURL.JoinPath(path)
	u.RawQuery = query.Encode()
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
	if resp.StatusCode != http.StatusOK {
		return nil, &HTTPError{StatusCode: resp.StatusCode, Status: resp.Status}
	}
	var body json.RawMessage
	err = json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&body)
	return body, err
}
