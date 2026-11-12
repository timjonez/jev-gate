// Package hcx talks to herdr-core (hc), the terminal runtime behind the Herdr
// desktop app. It adapts hc terminals to the herdrx shapes the gate loop uses,
// so one loop drives both the desktop app and the older herdr server.
package hcx

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"github.com/timjonez/gate/internal/herdrx"
)

// Conn is a client for one herdr-core socket. It dials per request.
type Conn struct {
	socket string
	seq    atomic.Uint64
	dial   func(ctx context.Context, network, address string) (net.Conn, error)
	// tty receives OSC 777 desktop notifications. Nil disables Notify.
	tty func() (io.WriteCloser, error)
}

// Dial returns a client for socket. It does not connect until a method is called.
func Dial(socket string) (*Conn, error) {
	if socket == "" {
		return nil, fmt.Errorf("%w: empty herdr-core socket path", herdrx.ErrUnavailable)
	}
	return &Conn{
		socket: socket,
		dial:   (&net.Dialer{}).DialContext,
		tty: func() (io.WriteCloser, error) {
			return os.OpenFile("/dev/tty", os.O_WRONLY, 0)
		},
	}, nil
}

// Socket returns the configured path.
func (c *Conn) Socket() string { return c.socket }

type request struct {
	JSONRPC string `json:"jsonrpc"`
	ID      uint64 `json:"id"`
	Method  string `json:"method"`
	Params  any    `json:"params"`
}

type response struct {
	ID     *uint64         `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Data    struct {
			Kind string `json:"kind"`
		} `json:"data"`
	} `json:"error"`
}

func (r response) apiError() error {
	if r.Error == nil {
		return nil
	}
	code := r.Error.Data.Kind
	if code == "" {
		code = r.Error.Message
	}
	return &herdrx.APIError{Code: code, Message: r.Error.Message}
}

func (c *Conn) open(ctx context.Context, method string, params any) (net.Conn, *bufio.Reader, response, error) {
	conn, err := c.dial(ctx, "unix", c.socket)
	if err != nil {
		return nil, nil, response{}, fmt.Errorf("%w: %s: %v", herdrx.ErrUnavailable, c.socket, err)
	}
	if params == nil {
		params = map[string]any{}
	}
	id := c.seq.Add(1)
	if err := writeLine(conn, request{JSONRPC: "2.0", ID: id, Method: method, Params: params}); err != nil {
		conn.Close()
		return nil, nil, response{}, err
	}
	r := bufio.NewReader(conn)
	for {
		raw, err := readLine(ctx, conn, r)
		if err != nil {
			conn.Close()
			return nil, nil, response{}, err
		}
		var resp response
		if err := json.Unmarshal(raw, &resp); err != nil {
			conn.Close()
			return nil, nil, response{}, fmt.Errorf("decode %s response: %w", method, err)
		}
		// Skip notifications that are not the reply to this request.
		if resp.ID == nil || *resp.ID != id {
			continue
		}
		if err := resp.apiError(); err != nil {
			conn.Close()
			return nil, nil, response{}, err
		}
		return conn, r, resp, nil
	}
}

func (c *Conn) call(ctx context.Context, method string, params any, out any) error {
	conn, _, resp, err := c.open(ctx, method, params)
	if err != nil {
		return err
	}
	conn.Close()
	if out == nil {
		return nil
	}
	if len(resp.Result) == 0 {
		return fmt.Errorf("empty result for %s", method)
	}
	return json.Unmarshal(resp.Result, out)
}

// Status is one OSC 7501 record.
type Status struct {
	App   string `json:"app"`
	Kind  string `json:"kind"`
	Msg   string `json:"msg"`
	State string `json:"state"`
	Title string `json:"title"`
}

// Terminal is a herdr-core terminal record.
type Terminal struct {
	Address    string            `json:"address"`
	Name       *string           `json:"name"`
	State      string            `json:"state"`
	Status     *Status           `json:"status"`
	StatusMS   *uint64           `json:"status_ms"`
	Reported   bool              `json:"reported"`
	Title      string            `json:"title"`
	Turn       uint64            `json:"turn"`
	Labels     map[string]string `json:"labels"`
	Foreground *struct {
		Name string `json:"name"`
		Cwd  string `json:"cwd"`
	} `json:"foreground"`
}

// Agent maps a terminal to the herdrx record the gate loop reads.
// The address stands in for Herdr's pane id.
func (t Terminal) Agent() herdrx.Agent {
	a := herdrx.Agent{
		PaneID:      t.Address,
		TerminalID:  t.Address,
		Title:       t.Title,
		StateLabels: t.Labels,
	}
	if t.Name != nil {
		a.Name = *t.Name
	}
	if t.Status != nil {
		a.Status = t.Status.State
		a.Agent = t.Status.App
		a.DisplayAgent = t.Status.App
	}
	if a.Agent == "" && t.Foreground != nil {
		a.Agent = t.Foreground.Name
	}
	if t.StatusMS != nil {
		a.StateChangeSeq = *t.StatusMS
	}
	return a
}

// ListAgents returns live terminals whose program reports agent status.
// Plain shells never report and cannot show a permission card.
func (c *Conn) ListAgents(ctx context.Context) ([]herdrx.Agent, error) {
	var out struct {
		Terminals []Terminal `json:"terminals"`
	}
	if err := c.call(ctx, "terminal.list", map[string]any{}, &out); err != nil {
		return nil, err
	}
	agents := []herdrx.Agent{}
	for _, t := range out.Terminals {
		if t.State != "live" || !t.Reported {
			continue
		}
		agents = append(agents, t.Agent())
	}
	return agents, nil
}

// ReadAgent reads a terminal's screen. Source "visible" reads only the
// screen; anything else adds lines of history above it.
func (c *Conn) ReadAgent(ctx context.Context, target, source string, lines int) (herdrx.Read, error) {
	if target == "" {
		return herdrx.Read{}, fmt.Errorf("%w: empty read target", herdrx.ErrInvalid)
	}
	params := map[string]any{"terminal": target}
	if source != "visible" && lines > 0 {
		params["lines"] = lines
	}
	var out struct {
		Text     string `json:"text"`
		Complete bool   `json:"complete"`
	}
	if err := c.call(ctx, "terminal.read", params, &out); err != nil {
		return herdrx.Read{}, err
	}
	return herdrx.Read{PaneID: target, Text: out.Text, Source: source, Truncated: !out.Complete}, nil
}

// SendKeys presses named keys in a terminal.
func (c *Conn) SendKeys(ctx context.Context, target string, keys []string) error {
	if target == "" {
		return fmt.Errorf("%w: empty send-keys target", herdrx.ErrInvalid)
	}
	if len(keys) == 0 {
		return fmt.Errorf("%w: no keys", herdrx.ErrInvalid)
	}
	return c.call(ctx, "terminal.keys", map[string]any{"terminal": target, "keys": keys}, nil)
}

// Notify writes an OSC 777 notification to gate's own terminal. herdr-core
// has no notification method; the desktop app shows OSC notifications from
// the terminal gate runs in.
func (c *Conn) Notify(ctx context.Context, title, body, sound string) (herdrx.Notification, error) {
	if title == "" {
		return herdrx.Notification{}, fmt.Errorf("%w: notification title is required", herdrx.ErrInvalid)
	}
	if c.tty == nil {
		return herdrx.Notification{Reason: "no terminal"}, nil
	}
	w, err := c.tty()
	if err != nil {
		return herdrx.Notification{Reason: "no terminal"}, nil
	}
	defer w.Close()
	if _, err := fmt.Fprintf(w, "\x1b]777;notify;%s;%s\x07", oscSafe(title), oscSafe(body)); err != nil {
		return herdrx.Notification{}, err
	}
	return herdrx.Notification{Shown: true}, nil
}

func oscSafe(s string) string {
	return strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f || r == ';' {
			return ' '
		}
		return r
	}, s)
}

type eventParams struct {
	Type     string          `json:"type"`
	Terminal string          `json:"terminal"`
	Data     json.RawMessage `json:"data"`
}

// Subscribe follows every terminal's fact changes. herdr-core needs no
// per-terminal filters, so subs is ignored. Events are translated to the
// Herdr kinds the loop understands: status changes become
// pane.agent_status_changed; created, lost, finished, removed, restarted,
// and a missed-events snapshot become pane.created or pane.closed.
func (c *Conn) Subscribe(ctx context.Context, subs []herdrx.Subscription, handle func(herdrx.Event) error) error {
	if handle == nil {
		return fmt.Errorf("%w: nil subscribe handler", herdrx.ErrInvalid)
	}
	conn, r, _, err := c.open(ctx, "events.subscribe", map[string]any{})
	if err != nil {
		return err
	}
	defer conn.Close()
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		raw, err := readLine(ctx, conn, r)
		if err != nil {
			return err
		}
		ev, ok := translateEvent(raw)
		if !ok {
			continue
		}
		if err := handle(ev); err != nil {
			return err
		}
	}
}

func translateEvent(raw []byte) (herdrx.Event, bool) {
	var msg response
	if err := json.Unmarshal(raw, &msg); err != nil {
		return herdrx.Event{}, false
	}
	switch msg.Method {
	case "missed":
		return herdrx.Event{Kind: "pane.created", Raw: raw}, true
	case "event":
	default:
		return herdrx.Event{}, false
	}
	var p eventParams
	if err := json.Unmarshal(msg.Params, &p); err != nil {
		return herdrx.Event{}, false
	}
	ev := herdrx.Event{PaneID: p.Terminal, Raw: append(json.RawMessage(nil), raw...)}
	switch p.Type {
	case "status":
		var d struct {
			Status *Status `json:"status"`
		}
		_ = json.Unmarshal(p.Data, &d)
		ev.Kind = "pane.agent_status_changed"
		if d.Status != nil {
			ev.Status = d.Status.State
			ev.Agent = d.Status.App
		}
	case "created", "restarted":
		ev.Kind = "pane.created"
	case "lost", "finished", "removed":
		ev.Kind = "pane.closed"
	default:
		return herdrx.Event{}, false
	}
	return ev, true
}

func writeLine(w io.Writer, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = w.Write(append(b, '\n'))
	return err
}

func readLine(ctx context.Context, conn net.Conn, r *bufio.Reader) ([]byte, error) {
	type result struct {
		b   []byte
		err error
	}
	ch := make(chan result, 1)
	go func() {
		if deadline, ok := ctx.Deadline(); ok {
			_ = conn.SetReadDeadline(deadline)
		} else {
			_ = conn.SetReadDeadline(time.Time{})
		}
		line, err := r.ReadBytes('\n')
		ch <- result{b: line, err: err}
	}()
	select {
	case <-ctx.Done():
		_ = conn.SetDeadline(time.Now())
		<-ch
		return nil, ctx.Err()
	case res := <-ch:
		line := []byte(strings.TrimRight(string(res.b), "\r\n"))
		if len(line) == 0 {
			if res.err != nil {
				return nil, res.err
			}
			return nil, io.EOF
		}
		if res.err != nil && !errors.Is(res.err, io.EOF) {
			return nil, res.err
		}
		return line, nil
	}
}
