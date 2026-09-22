package herdrx

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"path/filepath"
	"testing"
	"time"
)

func TestSendKeys(t *testing.T) {
	got := make(chan map[string]any, 1)
	sock := startServer(t, func(req map[string]any, w *bufio.Writer) {
		id, _ := req["id"].(string)
		params, _ := req["params"].(map[string]any)
		got <- params
		writeLine(t, w, map[string]any{
			"id":     id,
			"result": map[string]any{"type": "ok"},
		})
	})
	c, err := Dial(sock)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := c.SendKeys(ctx, "worker", []string{"1"}); err != nil {
		t.Fatal(err)
	}
	params := <-got
	if params["target"] != "worker" {
		t.Fatalf("target %+v", params)
	}
	keys, _ := params["keys"].([]any)
	if len(keys) != 1 || keys[0] != "1" {
		t.Fatalf("keys %+v", params["keys"])
	}
	if err := c.SendKeys(ctx, "", []string{"1"}); err == nil {
		t.Fatal("empty target")
	}
}

func TestResolveSocket(t *testing.T) {
	t.Setenv("HERDR_SOCKET_PATH", "")
	t.Setenv("HERDR_SESSION", "")
	t.Setenv("XDG_CONFIG_HOME", "/cfg")

	if got := ResolveSocket("/explicit.sock", "ignored"); got != "/explicit.sock" {
		t.Fatalf("explicit: %s", got)
	}
	t.Setenv("HERDR_SOCKET_PATH", "/env.sock")
	if got := ResolveSocket("", ""); got != "/env.sock" {
		t.Fatalf("env socket: %s", got)
	}
	t.Setenv("HERDR_SOCKET_PATH", "")
	if got := ResolveSocket("", "work"); got != "/cfg/herdr/sessions/work/herdr.sock" {
		t.Fatalf("session: %s", got)
	}
	if got := ResolveSocket("", ""); got != "/cfg/herdr/herdr.sock" {
		t.Fatalf("default: %s", got)
	}
}

func TestSessionKey(t *testing.T) {
	t.Setenv("HERDR_SESSION", "")
	if got := SessionKey(""); got != "default" {
		t.Fatalf("empty: %s", got)
	}
	if got := SessionKey("work/../x"); got != "work_.._x" {
		t.Fatalf("sanitize: %s", got)
	}
}

func TestCallAndSubscribe(t *testing.T) {
	sock := startServer(t, func(req map[string]any, w *bufio.Writer) {
		method, _ := req["method"].(string)
		id, _ := req["id"].(string)
		switch method {
		case "ping":
			writeLine(t, w, map[string]any{
				"id": id,
				"result": map[string]any{
					"type":     "pong",
					"version":  "0.8.0",
					"protocol": 19,
				},
			})
		case "agent.list":
			writeLine(t, w, map[string]any{
				"id": id,
				"result": map[string]any{
					"type": "agent_list",
					"agents": []map[string]any{{
						"agent":            "grok",
						"name":             "reviewer",
						"agent_status":     "blocked",
						"pane_id":          "w1:p2",
						"tab_id":           "w1:t1",
						"workspace_id":     "w1",
						"terminal_id":      "term_1",
						"focused":          false,
						"revision":         3,
						"state_change_seq": 7,
					}},
				},
			})
		case "agent.read":
			writeLine(t, w, map[string]any{
				"id": id,
				"result": map[string]any{
					"type": "pane_read",
					"read": map[string]any{
						"pane_id":      "w1:p2",
						"workspace_id": "w1",
						"tab_id":       "w1:t1",
						"source":       "detection",
						"format":       "text",
						"text":         "Allow edit to file?",
						"revision":     3,
						"truncated":    false,
					},
				},
			})
		case "notification.show":
			writeLine(t, w, map[string]any{
				"id": id,
				"result": map[string]any{
					"type":   "notification_show",
					"shown":  true,
					"reason": "shown",
				},
			})
		case "events.subscribe":
			writeLine(t, w, map[string]any{
				"id":     id,
				"result": map[string]any{"type": "subscription_started"},
			})
			writeLine(t, w, map[string]any{
				"event": "pane.agent_status_changed",
				"data": map[string]any{
					"pane_id":      "w1:p2",
					"workspace_id": "w1",
					"agent":        "grok",
					"agent_status": "blocked",
					"title":        "Allow edit?",
				},
			})
			writeLine(t, w, map[string]any{
				"event": "pane_created",
				"data": map[string]any{
					"type": "pane_created",
					"pane": map[string]any{"pane_id": "w1:p3", "workspace_id": "w1"},
				},
			})
		default:
			writeLine(t, w, map[string]any{
				"id":    id,
				"error": map[string]any{"code": "unknown", "message": method},
			})
		}
	})

	c, err := Dial(sock)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	pong, err := c.Ping(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if pong.Version != "0.8.0" || pong.Protocol != 19 {
		t.Fatalf("pong: %+v", pong)
	}

	agents, err := c.ListAgents(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(agents) != 1 || agents[0].Name != "reviewer" || agents[0].Status != "blocked" {
		t.Fatalf("agents: %+v", agents)
	}

	rd, err := c.ReadAgent(ctx, "reviewer", "detection", 0)
	if err != nil {
		t.Fatal(err)
	}
	if rd.Text != "Allow edit to file?" {
		t.Fatalf("read: %+v", rd)
	}

	n, err := c.Notify(ctx, "reviewer needs a decision", "Allow edit?", "request")
	if err != nil || !n.Shown {
		t.Fatalf("notify: %+v %v", n, err)
	}

	var events []Event
	subCtx, subCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer subCancel()
	err = c.Subscribe(subCtx, []Subscription{{Type: "pane.agent_status_changed", PaneID: "w1:p2"}}, func(ev Event) error {
		events = append(events, ev)
		if len(events) == 2 {
			subCancel()
		}
		return nil
	})
	if err != nil && err != context.Canceled {
		t.Fatal(err)
	}
	if len(events) != 2 {
		t.Fatalf("events: %+v", events)
	}
	if !events[0].StatusChanged() || events[0].PaneID != "w1:p2" || events[0].Status != "blocked" {
		t.Fatalf("status event: %+v", events[0])
	}
	if !events[1].NeedsReconcile() || events[1].PaneID != "w1:p3" {
		t.Fatalf("created event: %+v", events[1])
	}
}

func TestCreateStartPromptClose(t *testing.T) {
	var methods []string
	var lastParams map[string]any
	sock := startServer(t, func(req map[string]any, w *bufio.Writer) {
		method, _ := req["method"].(string)
		id, _ := req["id"].(string)
		methods = append(methods, method)
		if p, ok := req["params"].(map[string]any); ok {
			lastParams = p
		}
		switch method {
		case "workspace.create":
			writeLine(t, w, map[string]any{
				"id": id,
				"result": map[string]any{
					"type": "workspace_created",
					"workspace": map[string]any{
						"workspace_id": "w3",
						"label":        "fix-login",
						"number":       3,
					},
					"tab": map[string]any{
						"tab_id":       "w3:t1",
						"workspace_id": "w3",
						"label":        "main",
					},
					"root_pane": map[string]any{
						"pane_id":      "w3:p1",
						"workspace_id": "w3",
						"tab_id":       "w3:t1",
					},
				},
			})
		case "agent.start":
			writeLine(t, w, map[string]any{
				"id": id,
				"result": map[string]any{
					"type": "agent_started",
					"argv": []string{"claude", "--permission-mode", "auto"},
					"agent": map[string]any{
						"agent":        "claude",
						"name":         "fix-login",
						"agent_status": "idle",
						"pane_id":      "w3:p1",
						"tab_id":       "w3:t1",
						"workspace_id": "w3",
					},
				},
			})
		case "agent.prompt":
			writeLine(t, w, map[string]any{
				"id": id,
				"result": map[string]any{
					"type": "agent_prompted",
					"agent": map[string]any{
						"agent":        "claude",
						"name":         "fix-login",
						"agent_status": "working",
						"pane_id":      "w3:p1",
					},
				},
			})
		case "workspace.close":
			writeLine(t, w, map[string]any{
				"id":     id,
				"result": map[string]any{"type": "ok"},
			})
		default:
			writeLine(t, w, map[string]any{
				"id":    id,
				"error": map[string]any{"code": "unknown", "message": method},
			})
		}
	})

	c, err := Dial(sock)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	created, err := c.CreateWorkspace(ctx, WorkspaceCreate{
		Cwd: "/tmp/proj", Label: "fix-login", Focus: false,
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.Workspace.WorkspaceID != "w3" || created.RootPane.PaneID != "w3:p1" {
		t.Fatalf("created: %+v", created)
	}

	started, err := c.StartAgent(ctx, AgentStart{
		Name: "fix-login", Kind: "claude", PaneID: "w3:p1",
		Args: []string{"--permission-mode", "auto"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if started.Name != "fix-login" || started.Agent != "claude" {
		t.Fatalf("started: %+v", started)
	}

	prompted, err := c.PromptAgent(ctx, "fix-login", "fix the login")
	if err != nil {
		t.Fatal(err)
	}
	if prompted.Status != "working" {
		t.Fatalf("prompted: %+v", prompted)
	}
	if lastParams["target"] != "fix-login" || lastParams["text"] != "fix the login" {
		t.Fatalf("prompt params: %+v", lastParams)
	}
	if _, ok := lastParams["wait"]; ok {
		t.Fatalf("prompt should not wait: %+v", lastParams)
	}

	if err := c.CloseWorkspace(ctx, "w3"); err != nil {
		t.Fatal(err)
	}
	if lastParams["workspace_id"] != "w3" {
		t.Fatalf("close params: %+v", lastParams)
	}
	want := []string{"workspace.create", "agent.start", "agent.prompt", "workspace.close"}
	if len(methods) != len(want) {
		t.Fatalf("methods: %v", methods)
	}
	for i, m := range want {
		if methods[i] != m {
			t.Fatalf("methods: %v", methods)
		}
	}
}

func startServer(t *testing.T, handle func(req map[string]any, w *bufio.Writer)) string {
	t.Helper()
	sock := filepath.Join(t.TempDir(), "herdr.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				r := bufio.NewReader(c)
				w := bufio.NewWriter(c)
				for {
					line, err := r.ReadBytes('\n')
					if err != nil {
						return
					}
					var req map[string]any
					if err := json.Unmarshal(trimNL(line), &req); err != nil {
						return
					}
					handle(req, w)
				}
			}(conn)
		}
	}()
	return sock
}

func writeLine(t *testing.T, w *bufio.Writer, v any) {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write(append(b, '\n')); err != nil {
		return
	}
	_ = w.Flush()
}
