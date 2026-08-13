package io

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/iamangus/eve/internal/tasks"
)

// MCP is Eve's tool surface for agentfoundry. It is exposed as an ephemeral
// MCP server attached to every assistant run, giving Eve the ability to
// proactively message the user (send_message) and inspect what channels she
// can reach (list_channels).
type MCP struct {
	mgr *Manager
	srv *server.StreamableHTTPServer
}

func NewMCP(mgr *Manager) *MCP {
	mcpServer := server.NewMCPServer("eve", "1.0.0", server.WithToolCapabilities(true))
	m := &MCP{mgr: mgr, srv: server.NewStreamableHTTPServer(mcpServer)}

	sendTool := mcp.NewTool("send_message",
		mcp.WithDescription("Deliver a message to the user. Use this to proactively send the user a message (task results, notifications, reminders, questions) outside of the current conversation turn. The system routes the message to the best channel based on the user's presence and preferences."),
		mcp.WithString("message", mcp.Description("The message content to deliver to the user."), mcp.Required()),
		mcp.WithString("channel", mcp.Description("Optional explicit channel to deliver through (e.g. 'web', 'email', 'matrix'). Omit to let the router decide.")),
		mcp.WithString("purpose", mcp.Description("Purpose: 'notification' (default), 'question', or 'reminder'.")),
		mcp.WithString("conversation_id", mcp.Description("Optional conversation id. Defaults to the primary conversation.")),
	)
	mcpServer.AddTool(sendTool, m.handleSendMessage)

	listTool := mcp.NewTool("list_channels",
		mcp.WithDescription("List the communication channels available for sending the user messages, with their capabilities and whether the user is currently reachable on them."),
	)
	mcpServer.AddTool(listTool, m.handleListChannels)

	m.addTaskTools(mcpServer)
	m.addCalendarTools(mcpServer)

	return m
}

// addTaskTools registers the background-task tools. They are no-ops (error)
// when the task manager is not attached.
func (m *MCP) addTaskTools(s *server.MCPServer) {
	spawn := mcp.NewTool("spawn_task",
		mcp.WithDescription("Kick off a task in the background so you are free to do other things. Returns immediately with a task id; the system tracks it, and you will be able to check on it later. The task runs an agentfoundry agent with structured output so it can report completion or ask the user a question."),
		mcp.WithString("agent", mcp.Description("The agent id of the agentfoundry agent to run."), mcp.Required()),
		mcp.WithString("agent_name", mcp.Description("A human-readable name for the task (shown on the task board).")),
		mcp.WithString("message", mcp.Description("The task to accomplish."), mcp.Required()),
		mcp.WithString("conversation_id", mcp.Description("Optional conversation id. Defaults to the primary conversation.")),
	)
	s.AddTool(spawn, m.handleSpawnTask)

	list := mcp.NewTool("list_tasks",
		mcp.WithDescription("List background tasks and their statuses (running, needs_input, completed, failed, cancelled)."),
		mcp.WithString("conversation_id", mcp.Description("Optional conversation id to filter by.")),
	)
	s.AddTool(list, m.handleListTasks)

	get := mcp.NewTool("get_task",
		mcp.WithDescription("Get the full detail of a background task, including its result and any question it is waiting on."),
		mcp.WithString("task_id", mcp.Description("The task id."), mcp.Required()),
	)
	s.AddTool(get, m.handleGetTask)

	reply := mcp.NewTool("reply_task",
		mcp.WithDescription("Reply to a background task that is waiting on user input (status needs_input). The task re-runs with your reply."),
		mcp.WithString("task_id", mcp.Description("The task id."), mcp.Required()),
		mcp.WithString("content", mcp.Description("The reply to pass to the task agent."), mcp.Required()),
	)
	s.AddTool(reply, m.handleReplyTask)

	cancel := mcp.NewTool("cancel_task",
		mcp.WithDescription("Cancel a background task."),
		mcp.WithString("task_id", mcp.Description("The task id."), mcp.Required()),
	)
	s.AddTool(cancel, m.handleCancelTask)
}

func (m *MCP) handleSpawnTask(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	tm := m.mgr.Tasks
	if tm == nil {
		return mcp.NewToolResultError("task manager is not available"), nil
	}
	args := req.GetArguments()
	agentID, _ := args["agent"].(string)
	message, _ := args["message"].(string)
	if agentID == "" || message == "" {
		return mcp.NewToolResultError("agent and message are required"), nil
	}
	agentName, _ := args["agent_name"].(string)
	if agentName == "" {
		agentName = agentID
	}
	convID, _ := args["conversation_id"].(string)
	if convID == "" {
		convID = m.mgr.store.PrimaryConversationID()
	}
	t, err := tm.SpawnTask(ctx, convID, agentID, agentName, message)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("spawn failed: %v", err)), nil
	}
	data, err := json.Marshal(t)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return mcp.NewToolResultText("Task spawned: " + string(data)), nil
}

func (m *MCP) handleListTasks(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	tm := m.mgr.Tasks
	if tm == nil {
		return mcp.NewToolResultError("task manager is not available"), nil
	}
	args := req.GetArguments()
	var list []tasks.Task
	if convID, _ := args["conversation_id"].(string); convID != "" {
		list = tm.ListByConversation(convID)
	} else {
		list = tm.List()
	}
	data, err := json.Marshal(list)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return mcp.NewToolResultText(string(data)), nil
}

func (m *MCP) handleGetTask(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	tm := m.mgr.Tasks
	if tm == nil {
		return mcp.NewToolResultError("task manager is not available"), nil
	}
	id, _ := req.GetArguments()["task_id"].(string)
	t, err := tm.Get(id)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("task %q not found", id)), nil
	}
	data, err := json.Marshal(t)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return mcp.NewToolResultText(string(data)), nil
}

func (m *MCP) handleReplyTask(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	tm := m.mgr.Tasks
	if tm == nil {
		return mcp.NewToolResultError("task manager is not available"), nil
	}
	args := req.GetArguments()
	id, _ := args["task_id"].(string)
	content, _ := args["content"].(string)
	if id == "" || content == "" {
		return mcp.NewToolResultError("task_id and content are required"), nil
	}
	if err := tm.Reply(ctx, id, content); err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("reply failed: %v", err)), nil
	}
	return mcp.NewToolResultText("Task updated; re-running the agent with your reply"), nil
}

func (m *MCP) handleCancelTask(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	tm := m.mgr.Tasks
	if tm == nil {
		return mcp.NewToolResultError("task manager is not available"), nil
	}
	id, _ := req.GetArguments()["task_id"].(string)
	if id == "" {
		return mcp.NewToolResultError("task_id is required"), nil
	}
	if err := tm.Cancel(id); err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("cancel failed: %v", err)), nil
	}
	return mcp.NewToolResultText("Task cancelled"), nil
}

func (m *MCP) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	m.srv.ServeHTTP(w, r)
}

func (m *MCP) handleSendMessage(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := req.GetArguments()
	message, _ := args["message"].(string)
	if message == "" {
		return mcp.NewToolResultError("message is required"), nil
	}
	channel, _ := args["channel"].(string)
	purpose, _ := args["purpose"].(string)
	if purpose == "" {
		purpose = PurposeNotification
	}
	convID, _ := args["conversation_id"].(string)
	if convID == "" {
		convID = m.mgr.store.PrimaryConversationID()
	}

	// Fire-and-forget: return to Eve immediately; delivery proceeds in the
	// background through the routing pipeline. The store append is serialized.
	go func() {
		if err := m.mgr.Router.Notify(context.Background(), convID, message, purpose, channel); err != nil {
			return
		}
	}()

	return mcp.NewToolResultText("Message accepted for delivery"), nil
}

func (m *MCP) handleListChannels(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	snaps := m.mgr.Reg.Snapshot()
	out := make([]EndpointSnapshot, 0, len(snaps))
	for _, s := range snaps {
		if s.Output {
			out = append(out, s)
		}
	}
	data, err := json.Marshal(out)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return mcp.NewToolResultText(string(data)), nil
}
