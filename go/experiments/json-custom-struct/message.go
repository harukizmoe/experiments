package jsoncustomstruct

import (
	"encoding/json"
	"fmt"
)

const (
	RoleUser       = "user"
	RoleAssistant  = "assistant"
	RoleToolResult = "toolResult"
)

type Message interface {
	isMessage()
	// Role returns the discriminant ("user" | "assistant" | "toolResult").
	Role() string
}

type AgentMessage = Message

type UserMessage struct {
	RoleField string          `json:"role"`
	Content   json.RawMessage `json:"content"`
	Timestamp int64           `json:"timestamp"`
}

func (UserMessage) isMessage()     {}
func (m UserMessage) Role() string { return RoleUser }

type AssistantMessage struct {
	RoleField    string          `json:"role"`
	Content      json.RawMessage `json:"content"`
	API          string          `json:"api,omitempty"`
	Provider     string          `json:"provider,omitempty"`
	Model        string          `json:"model,omitempty"`
	StopReason   string          `json:"stopReason,omitempty"`
	ErrorMessage string          `json:"errorMessage,omitempty"`
	Timestamp    int64           `json:"timestamp"`

	// Optional diagnostics, kept for cross-provider replay/observability.
	ResponseModel string `json:"responseModel,omitempty"`
	ResponseID    string `json:"responseId,omitempty"`
}

func (AssistantMessage) isMessage()     {}
func (m AssistantMessage) Role() string { return RoleAssistant }

type ToolResultMessage struct {
	RoleField  string          `json:"role"`
	ToolCallID string          `json:"toolCallId"`
	ToolName   string          `json:"toolName"`
	Content    json.RawMessage `json:"content"`
	Details    any             `json:"details,omitempty"`
	IsError    bool            `json:"isError"`
	Timestamp  int64           `json:"timestamp"`
}

func (ToolResultMessage) isMessage()     {}
func (m ToolResultMessage) Role() string { return RoleToolResult }

type MessageList []Message

func (ml *MessageList) UnmarshalJSON(data []byte) error {
	var raws []json.RawMessage
	if err := json.Unmarshal(data, &raws); err != nil {
		return err
	}
	out := make(MessageList, 0, len(raws))
	for i, raw := range raws {
		m, err := decodeMessage(raw)
		if err != nil {
			return fmt.Errorf("message[%d]: %w", i, err)
		}
		out = append(out, m)
	}
	*ml = out
	return nil
}

func decodeMessage(raw json.RawMessage) (Message, error) {
	var probe struct {
		Role string `json:"role"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		return nil, fmt.Errorf("peek role: %w", err)
	}
	switch probe.Role {
	case RoleUser:
		var m UserMessage
		if err := json.Unmarshal(raw, &m); err != nil {
			return nil, err
		}
		return m, nil
	case RoleAssistant:
		var m AssistantMessage
		if err := json.Unmarshal(raw, &m); err != nil {
			return nil, err
		}
		return m, nil
	case RoleToolResult:
		var m ToolResultMessage
		if err := json.Unmarshal(raw, &m); err != nil {
			return nil, err
		}
		return m, nil
	case "":
		return nil, fmt.Errorf("missing role discriminant")
	default:
		return nil, fmt.Errorf("unknown role %q", probe.Role)
	}
}
