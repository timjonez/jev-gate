package herdrx

import "encoding/json"

// Agent is a live Herdr agent record from agent.list / session.snapshot.
type Agent struct {
	Agent            string            `json:"agent"`
	Name             string            `json:"name"`
	DisplayAgent     string            `json:"display_agent"`
	Status           string            `json:"agent_status"`
	PaneID           string            `json:"pane_id"`
	TabID            string            `json:"tab_id"`
	WorkspaceID      string            `json:"workspace_id"`
	TerminalID       string            `json:"terminal_id"`
	Title            string            `json:"title"`
	TerminalTitle    string            `json:"terminal_title_stripped"`
	Focused          bool              `json:"focused"`
	Revision         uint64            `json:"revision"`
	StateChangeSeq   uint64            `json:"state_change_seq"`
	StateLabels      map[string]string `json:"state_labels"`
	LaunchPending    bool              `json:"launch_pending"`
	InteractiveReady bool              `json:"interactive_ready"`
}

// Label returns the best human identifier for an agent.
func (a Agent) Label() string {
	for _, s := range []string{a.Name, a.DisplayAgent, a.Agent, a.PaneID} {
		if s != "" {
			return s
		}
	}
	return a.PaneID
}

// DisplayTitle prefers the stripped terminal title, then the pane title.
func (a Agent) DisplayTitle() string {
	if a.TerminalTitle != "" {
		return a.TerminalTitle
	}
	return a.Title
}

// Read is a pane/agent screen snapshot.
type Read struct {
	PaneID    string `json:"pane_id"`
	Text      string `json:"text"`
	Source    string `json:"source"`
	Revision  uint64 `json:"revision"`
	Truncated bool   `json:"truncated"`
}

// Notification is the result of notification.show.
type Notification struct {
	Shown  bool   `json:"shown"`
	Reason string `json:"reason"`
}

// WorkspaceCreate is the input to workspace.create.
type WorkspaceCreate struct {
	Cwd   string
	Label string
	Focus bool
}

// Workspace is a Herdr workspace record.
type Workspace struct {
	WorkspaceID string `json:"workspace_id"`
	Label       string `json:"label"`
	Number      int    `json:"number"`
}

// Tab is a Herdr tab record.
type Tab struct {
	TabID       string `json:"tab_id"`
	WorkspaceID string `json:"workspace_id"`
	Label       string `json:"label"`
}

// Pane is a Herdr pane record.
type Pane struct {
	PaneID      string `json:"pane_id"`
	WorkspaceID string `json:"workspace_id"`
	TabID       string `json:"tab_id"`
}

// WorkspaceCreated is the result of workspace.create.
type WorkspaceCreated struct {
	Workspace Workspace `json:"workspace"`
	Tab       Tab       `json:"tab"`
	RootPane  Pane      `json:"root_pane"`
}

// AgentStart is the input to agent.start.
type AgentStart struct {
	Name   string
	Kind   string
	PaneID string
	Args   []string
}

// Pong is the ping response.
type Pong struct {
	Version  string `json:"version"`
	Protocol uint32 `json:"protocol"`
}

// Subscription is one events.subscribe filter.
type Subscription struct {
	Type        string `json:"type"`
	PaneID      string `json:"pane_id,omitempty"`
	AgentStatus string `json:"agent_status,omitempty"`
}

// Event is a normalized Herdr subscription or lifecycle event.
type Event struct {
	Kind        string          `json:"kind"`
	PaneID      string          `json:"pane_id,omitempty"`
	WorkspaceID string          `json:"workspace_id,omitempty"`
	Agent       string          `json:"agent,omitempty"`
	Status      string          `json:"agent_status,omitempty"`
	Title       string          `json:"title,omitempty"`
	Raw         json.RawMessage `json:"-"`
}

// NeedsReconcile reports whether the live agent set may have changed.
func (e Event) NeedsReconcile() bool {
	switch e.Kind {
	case "pane.created", "pane.closed", "pane.moved", "pane.agent_detected",
		"pane_created", "pane_closed", "pane_moved", "pane_agent_detected":
		return true
	default:
		return false
	}
}

// StatusChanged reports a per-pane agent status transition.
func (e Event) StatusChanged() bool {
	switch e.Kind {
	case "pane.agent_status_changed", "pane_agent_status_changed":
		return e.PaneID != ""
	default:
		return false
	}
}
