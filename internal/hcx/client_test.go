package hcx

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/timjonez/gate/internal/herdrx"
)

// startServer answers each request line with reply(method, params). A reply
// that is a []any is written as several lines (notifications, then the reply).
func startServer(t *testing.T, reply func(method string, params map[string]any) []any) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "hcx")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	sock := filepath.Join(dir, "c.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(conn net.Conn) {
				defer conn.Close()
				r := bufio.NewReader(conn)
				for {
					line, err := r.ReadBytes('\n')
					if err != nil {
						return
					}
					var req struct {
						JSONRPC string         `json:"jsonrpc"`
						ID      uint64         `json:"id"`
						Method  string         `json:"method"`
						Params  map[string]any `json:"params"`
					}
					if err := json.Unmarshal(line, &req); err != nil || req.JSONRPC != "2.0" {
						return
					}
					for _, msg := range reply(req.Method, req.Params) {
						if m, ok := msg.(map[string]any); ok {
							if _, has := m["method"]; !has {
								m["jsonrpc"] = "2.0"
								m["id"] = req.ID
							}
						}
						b, _ := json.Marshal(msg)
						conn.Write(append(b, '\n'))
					}
				}
			}(conn)
		}
	}()
	return sock
}

func ctx(t *testing.T) context.Context {
	c, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	t.Cleanup(cancel)
	return c
}

func TestListAgentsKeepsLiveReportingTerminals(t *testing.T) {
	sock := startServer(t, func(method string, params map[string]any) []any {
		if method != "terminal.list" {
			t.Errorf("method %s", method)
		}
		return []any{map[string]any{"result": map[string]any{
			"hidden_finished": 0,
			"machines":        []any{},
			"terminals": []any{
				map[string]any{"address": "t1", "state": "live", "reported": false, "status": nil,
					"foreground": map[string]any{"name": "bash", "cwd": "/"}},
				map[string]any{"address": "t7", "name": "review", "state": "live", "reported": true,
					"title": "Fix login", "status_ms": 42,
					"status": map[string]any{"app": "claude-code", "state": "blocked", "kind": "permission"}},
				map[string]any{"address": "t8", "state": "finished", "reported": true},
			},
		}}}
	})
	c, _ := Dial(sock)
	agents, err := c.ListAgents(ctx(t))
	if err != nil {
		t.Fatal(err)
	}
	if len(agents) != 1 {
		t.Fatalf("agents %+v", agents)
	}
	a := agents[0]
	if a.PaneID != "t7" || a.Name != "review" || a.Agent != "claude-code" || a.Status != "blocked" ||
		a.DisplayTitle() != "Fix login" || a.StateChangeSeq != 42 {
		t.Fatalf("agent %+v", a)
	}
}

func TestReadAgentScreenOrHistory(t *testing.T) {
	got := make(chan map[string]any, 2)
	sock := startServer(t, func(method string, params map[string]any) []any {
		got <- params
		return []any{map[string]any{"result": map[string]any{"text": "screen", "complete": true, "how": "screen"}}}
	})
	c, _ := Dial(sock)
	r, err := c.ReadAgent(ctx(t), "t7", "visible", 50)
	if err != nil || r.Text != "screen" {
		t.Fatalf("read %+v %v", r, err)
	}
	p := <-got
	if p["terminal"] != "t7" || p["lines"] != nil {
		t.Fatalf("visible params %+v", p)
	}
	if _, err := c.ReadAgent(ctx(t), "t7", "recent_unwrapped", 80); err != nil {
		t.Fatal(err)
	}
	p = <-got
	if p["lines"] != float64(80) {
		t.Fatalf("history params %+v", p)
	}
}

func TestSendKeysAndErrors(t *testing.T) {
	sock := startServer(t, func(method string, params map[string]any) []any {
		if params["terminal"] == "gone" {
			return []any{map[string]any{"error": map[string]any{
				"code": -32000, "message": "not_found", "data": map[string]any{"kind": "not_found"},
			}}}
		}
		keys, _ := params["keys"].([]any)
		if method != "terminal.keys" || len(keys) != 1 || keys[0] != "1" {
			t.Errorf("keys %s %+v", method, params)
		}
		return []any{
			// A stray notification before the reply must be skipped.
			map[string]any{"jsonrpc": "2.0", "method": "heartbeat", "params": map[string]any{"seq": 1}},
			map[string]any{"result": map[string]any{"turn": 3, "status": "working"}},
		}
	})
	c, _ := Dial(sock)
	if err := c.SendKeys(ctx(t), "t7", []string{"1"}); err != nil {
		t.Fatal(err)
	}
	err := c.SendKeys(ctx(t), "gone", []string{"1"})
	if !errors.Is(err, herdrx.ErrNotFound) {
		t.Fatalf("want not found, got %v", err)
	}
	if err := c.SendKeys(ctx(t), "", []string{"1"}); err == nil {
		t.Fatal("empty target")
	}
}

func TestSubscribeTranslatesEvents(t *testing.T) {
	sock := startServer(t, func(method string, params map[string]any) []any {
		if method != "events.subscribe" {
			t.Errorf("method %s", method)
		}
		ev := func(typ, term string, data any) map[string]any {
			return map[string]any{"jsonrpc": "2.0", "method": "event",
				"params": map[string]any{"type": typ, "terminal": term, "seq": 2, "data": data}}
		}
		return []any{
			map[string]any{"result": map[string]any{"seq": 1, "terminals": []any{}}},
			map[string]any{"jsonrpc": "2.0", "method": "heartbeat", "params": map[string]any{"seq": 1}},
			ev("title", "t7", map[string]any{"title": "x"}),
			ev("status", "t7", map[string]any{"status": map[string]any{"app": "claude-code", "state": "blocked"}}),
			ev("created", "t9", map[string]any{}),
			ev("finished", "t9", map[string]any{}),
			map[string]any{"jsonrpc": "2.0", "method": "missed", "params": map[string]any{}},
		}
	})
	c, _ := Dial(sock)
	cctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var got []herdrx.Event
	err := c.Subscribe(cctx, nil, func(ev herdrx.Event) error {
		got = append(got, ev)
		if len(got) == 4 {
			return io.EOF
		}
		return nil
	})
	if !errors.Is(err, io.EOF) {
		t.Fatalf("subscribe: %v", err)
	}
	if !got[0].StatusChanged() || got[0].PaneID != "t7" || got[0].Status != "blocked" {
		t.Fatalf("status event %+v", got[0])
	}
	for _, ev := range got[1:] {
		if !ev.NeedsReconcile() {
			t.Fatalf("want reconcile: %+v", ev)
		}
	}
}

type buf struct{ bytes.Buffer }

func (*buf) Close() error { return nil }

func TestNotifyWritesOSC777(t *testing.T) {
	var b buf
	c, _ := Dial("/unused.sock")
	c.tty = func() (io.WriteCloser, error) { return &b, nil }
	n, err := c.Notify(ctx(t), "review held", "rm -rf /; bad\nthing", "")
	if err != nil || !n.Shown {
		t.Fatalf("notify %+v %v", n, err)
	}
	if got := b.String(); got != "\x1b]777;notify;review held;rm -rf /  bad thing\x07" {
		t.Fatalf("osc %q", got)
	}
	c.tty = func() (io.WriteCloser, error) { return nil, errors.New("no tty") }
	if n, err := c.Notify(ctx(t), "x", "", ""); err != nil || n.Shown {
		t.Fatalf("no tty: %+v %v", n, err)
	}
}

func TestResolveSocket(t *testing.T) {
	for _, k := range []string{"HC_SOCKET", "HC_TERMINAL_SOCKET", "HC_SESSION", "HC_RUNTIME"} {
		t.Setenv(k, "")
	}
	t.Setenv("XDG_RUNTIME_DIR", "/run/user/7")
	if got := ResolveSocket("/x.sock"); got != "/x.sock" {
		t.Fatal(got)
	}
	if got := ResolveSocket(""); got != "/run/user/7/herdr-core/default.sock" {
		t.Fatal(got)
	}
	t.Setenv("HC_SESSION", "test")
	if got := ResolveSocket(""); got != "/run/user/7/herdr-core/test.sock" {
		t.Fatal(got)
	}
	t.Setenv("HC_TERMINAL_SOCKET", "/term.sock")
	if got := ResolveSocket(""); got != "/term.sock" {
		t.Fatal(got)
	}
	t.Setenv("HC_SOCKET", "/env.sock")
	if got := ResolveSocket(""); got != "/env.sock" {
		t.Fatal(got)
	}
	if Available("/definitely/missing.sock") || strings.Contains(ResolveSocket(""), "missing") {
		t.Fatal("missing socket reported available")
	}
}
