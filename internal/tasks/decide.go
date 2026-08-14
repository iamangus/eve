package tasks

import (
	"encoding/json"
	"strings"

	"github.com/iamangus/eve/internal/agentfoundry"
)

type outcome struct {
	Status string `json:"status"`
	Text   string `json:"text"`
}

// taskSchema constrains the background-task child agent's reply so it can
// natively report completion or request user input.
func taskSchema() *agentfoundry.ResponseSchema {
	raw := json.RawMessage(`{
  "type": "object",
  "properties": {
    "status": {"type": "string", "enum": ["completed", "needs_input"]},
    "text": {"type": "string"}
  },
  "required": ["status", "text"],
  "additionalProperties": false
}`)
	return &agentfoundry.ResponseSchema{Name: "task_result", Schema: raw, Strict: true}
}

// parseOutcome extracts {status, text} from a child run's response. If the
// response is not JSON, the run is treated as a plain completed result.
func parseOutcome(resp string) outcome {
	resp = strings.TrimSpace(resp)
	if resp == "" {
		return outcome{Status: StatusCompleted, Text: ""}
	}
	var o outcome
	if err := json.Unmarshal([]byte(resp), &o); err == nil && o.Status != "" {
		if o.Status != StatusCompleted && o.Status != StatusNeedsInput {
			o.Status = StatusCompleted
		}
		return o
	}
	return outcome{Status: StatusCompleted, Text: resp}
}
