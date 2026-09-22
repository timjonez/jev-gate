package gate

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/timjonez/gate/internal/herdrx"
)

// Options control the gate loop.
type Options struct {
	Ignore         []string
	SelfPane       string
	DryRun         bool
	NotifyHold     bool
	Mode           Mode
	Thresholds     Thresholds
	Settle         time.Duration
	ReconcileEvery time.Duration
	Now            func() time.Time
}

// Decision is one judged card, or a blocked screen that is not a command card.
type Decision struct {
	Action         string  `json:"action"`
	Reason         string  `json:"reason"`
	Agent          string  `json:"agent,omitempty"`
	Name           string  `json:"name,omitempty"`
	PaneID         string  `json:"pane_id"`
	Key            string  `json:"key,omitempty"`
	Label          string  `json:"label,omitempty"`
	Proposed       string  `json:"proposed,omitempty"`
	Appropriate    float64 `json:"appropriate,omitempty"`
	NeedsHuman     float64 `json:"needs_human,omitempty"`
	ReadsSecret    float64 `json:"reads_secret,omitempty"`
	DeletesOutside float64 `json:"deletes_outside,omitempty"`
	InfraApply     float64 `json:"infra_apply,omitempty"`
	Mode           string  `json:"mode,omitempty"`
	Kind           string  `json:"kind,omitempty"`
	KindConfidence float64 `json:"kind_confidence,omitempty"`
	Judged         bool    `json:"judged"`
	DryRun         bool    `json:"dry_run"`
}

// Loop watches Herdr agents and presses a single-shot allow when the judgment
// says to. It never presses a refusal.
type Loop struct {
	Client herdrx.Client
	Judge  Judge
	Opts   Options
	Status func(string)
	Emit   func(Decision) error

	mu         sync.Mutex
	mem        map[string]memory
	subscribed map[string]struct{}
	reconnect  chan struct{}
}

type memory struct {
	status  string
	seq     uint64
	hash    string
	settled bool
	retryAt time.Time
}

const retryAfter = 15 * time.Second

// Run blocks until ctx is cancelled or a fatal error occurs.
func (l *Loop) Run(ctx context.Context) error {
	if l.Client == nil {
		return fmt.Errorf("gate: nil client")
	}
	if l.Judge == nil {
		return fmt.Errorf("gate: nil judge")
	}
	l.Opts.Thresholds = fillThresholds(l.Opts.Thresholds)
	if l.Opts.Mode == "" {
		l.Opts.Mode = ModeStrict
	}
	if l.Opts.Mode == ModeLoose {
		l.statusf("loose: almost every command is allowed; secrets, deletes outside the worktree, and infrastructure apply or destroy stay on screen")
	}
	if l.Opts.ReconcileEvery <= 0 {
		l.Opts.ReconcileEvery = 2 * time.Second
	}
	if l.Opts.Now == nil {
		l.Opts.Now = func() time.Time { return time.Now().UTC() }
	}
	if l.Opts.SelfPane == "" {
		l.Opts.SelfPane = os.Getenv("HERDR_PANE_ID")
	}
	if l.mem == nil {
		l.mem = map[string]memory{}
	}
	if l.reconnect == nil {
		l.reconnect = make(chan struct{}, 1)
	}

	l.statusf("watching %s", l.Client.Socket())
	if l.Opts.DryRun {
		l.statusf("dry-run: judgments are logged and no keys are sent")
	}
	if err := l.reconcile(ctx); err != nil && !errors.Is(err, context.Canceled) {
		l.statusf("initial list: %v", err)
	}

	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		subCtx, cancel := context.WithCancel(ctx)
		subErr := make(chan error, 1)
		go func() {
			subErr <- l.subscribe(subCtx)
		}()

		ticker := time.NewTicker(l.Opts.ReconcileEvery)
		var runErr error
	inner:
		for {
			select {
			case <-ctx.Done():
				cancel()
				<-subErr
				ticker.Stop()
				return ctx.Err()
			case <-l.reconnect:
				l.statusf("agent set changed; resubscribing")
				cancel()
				<-subErr
				ticker.Stop()
				break inner
			case err := <-subErr:
				ticker.Stop()
				cancel()
				if err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, io.EOF) {
					l.statusf("subscribe: %v", err)
					runErr = err
				}
				break inner
			case <-ticker.C:
				if err := l.reconcile(ctx); err != nil {
					if errors.Is(err, context.Canceled) {
						cancel()
						<-subErr
						ticker.Stop()
						return err
					}
					l.statusf("reconcile: %v", err)
				}
			}
		}
		if runErr != nil {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(time.Second):
			}
		}
	}
}

func (l *Loop) subscribe(ctx context.Context) error {
	agents, err := l.Client.ListAgents(ctx)
	if err != nil {
		return err
	}
	subs := sessionSubscriptions(agents)
	l.mu.Lock()
	l.subscribed = paneSet(agents)
	l.mu.Unlock()
	l.statusf("subscribed to %d filters", len(subs))
	return l.Client.Subscribe(ctx, subs, func(ev herdrx.Event) error {
		if ev.NeedsReconcile() {
			return l.reconcile(ctx)
		}
		if ev.StatusChanged() {
			return l.handlePane(ctx, ev.PaneID)
		}
		return nil
	})
}

func sessionSubscriptions(agents []herdrx.Agent) []herdrx.Subscription {
	subs := []herdrx.Subscription{
		{Type: "pane.created"},
		{Type: "pane.closed"},
		{Type: "pane.moved"},
		{Type: "pane.agent_detected"},
	}
	seen := map[string]struct{}{}
	for _, a := range agents {
		if a.PaneID == "" {
			continue
		}
		if _, ok := seen[a.PaneID]; ok {
			continue
		}
		seen[a.PaneID] = struct{}{}
		subs = append(subs, herdrx.Subscription{
			Type:   "pane.agent_status_changed",
			PaneID: a.PaneID,
		})
	}
	return subs
}

func (l *Loop) reconcile(ctx context.Context) error {
	agents, err := l.Client.ListAgents(ctx)
	if err != nil {
		return err
	}
	live := paneSet(agents)
	sort.SliceStable(agents, func(i, j int) bool {
		return agents[i].PaneID < agents[j].PaneID
	})
	for _, a := range agents {
		if err := l.consider(ctx, a); err != nil {
			return err
		}
	}
	l.mu.Lock()
	for pane := range l.mem {
		if _, ok := live[pane]; !ok {
			delete(l.mem, pane)
		}
	}
	needReconnect := l.subscribed != nil && !sameSet(l.subscribed, live)
	l.mu.Unlock()
	if needReconnect {
		l.requestReconnect()
	}
	return nil
}

func (l *Loop) requestReconnect() {
	select {
	case l.reconnect <- struct{}{}:
	default:
	}
}

func (l *Loop) handlePane(ctx context.Context, paneID string) error {
	if paneID == "" {
		return nil
	}
	agents, err := l.Client.ListAgents(ctx)
	if err != nil {
		return err
	}
	for _, a := range agents {
		if a.PaneID == paneID {
			return l.consider(ctx, a)
		}
	}
	l.mu.Lock()
	delete(l.mem, paneID)
	l.mu.Unlock()
	return nil
}

func (l *Loop) consider(ctx context.Context, a herdrx.Agent) error {
	if a.PaneID == "" || l.ignored(a) {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.mem == nil {
		l.mem = map[string]memory{}
	}

	prev, had := l.mem[a.PaneID]
	if strings.EqualFold(a.Status, "working") || strings.TrimSpace(a.Status) == "" {
		l.mem[a.PaneID] = memory{status: a.Status, seq: a.StateChangeSeq}
		return nil
	}
	needSettle := !had || prev.status == "working" || prev.status == ""
	if needSettle && l.settle() > 0 {
		l.mu.Unlock()
		timer := time.NewTimer(l.settle())
		select {
		case <-ctx.Done():
			timer.Stop()
			l.mu.Lock()
			return ctx.Err()
		case <-timer.C:
		}
		l.mu.Lock()
		prev, had = l.mem[a.PaneID]
	}

	screen, err := l.readScreen(ctx, a)
	if err != nil {
		l.statusf("read %s: %v", agentLabel(a), err)
		return nil
	}
	hash := fingerprint(screen)
	now := l.Opts.Now()
	if had && prev.hash == hash && prev.settled {
		l.mem[a.PaneID] = memory{status: a.Status, seq: a.StateChangeSeq, hash: hash, settled: true}
		return nil
	}
	if had && prev.hash == hash && !prev.retryAt.IsZero() && now.Before(prev.retryAt) {
		return nil
	}

	card, ok := ParseCard(screen)
	base := Decision{
		Agent:  a.Agent,
		Name:   a.Name,
		PaneID: a.PaneID,
		DryRun: l.Opts.DryRun,
	}
	if !ok {
		l.mem[a.PaneID] = memory{status: a.Status, seq: a.StateChangeSeq, hash: hash, settled: true}
		if strings.EqualFold(a.Status, "blocked") {
			base.Action = "hold"
			base.Reason = "not a command prompt"
			return l.emit(ctx, base)
		}
		return nil
	}

	state := State{
		Agent:  a.Agent,
		Name:   a.Name,
		Title:  a.DisplayTitle(),
		Screen: screen,
		Mode:   l.Opts.Mode,
	}
	answers, err := l.Judge.Judge(ctx, state)
	if err != nil {
		l.mem[a.PaneID] = memory{
			status:  a.Status,
			seq:     a.StateChangeSeq,
			hash:    hash,
			retryAt: now.Add(retryAfter),
		}
		base.Action = "error"
		base.Reason = err.Error()
		base.Proposed = card.Proposed
		l.statusf("judge %s: %v", agentLabel(a), err)
		return l.emit(ctx, base)
	}

	action, reason := Decide(answers, fillThresholds(l.Opts.Thresholds), l.Opts.Mode)
	base.Action = action
	base.Reason = reason
	base.Key = card.Key
	base.Label = card.Label
	base.Proposed = card.Proposed
	base.Appropriate = answers.Appropriate
	base.NeedsHuman = answers.NeedsHuman
	base.ReadsSecret = answers.ReadsSecret
	base.DeletesOutside = answers.DeletesOutside
	base.InfraApply = answers.InfraApply
	base.Mode = string(l.Opts.Mode)
	base.Kind = answers.Kind
	base.KindConfidence = answers.KindConfidence
	base.Judged = true

	if action == "allow" && !l.Opts.DryRun {
		again, err := l.readScreen(ctx, a)
		if err != nil {
			l.mem[a.PaneID] = memory{
				status: a.Status, seq: a.StateChangeSeq, hash: hash,
				retryAt: now.Add(retryAfter),
			}
			base.Action = "error"
			base.Reason = err.Error()
			return l.emit(ctx, base)
		}
		againCard, againOK := ParseCard(again)
		if fingerprint(again) != hash || !againOK || againCard.Key != card.Key {
			l.mem[a.PaneID] = memory{status: a.Status, seq: a.StateChangeSeq, hash: fingerprint(again)}
			base.Action = "hold"
			base.Reason = "screen changed before allow"
			base.Judged = true
			return l.emit(ctx, base)
		}
		target := a.PaneID
		if a.Name != "" {
			target = a.Name
		}
		if err := l.Client.SendKeys(ctx, target, []string{card.Key}); err != nil {
			l.mem[a.PaneID] = memory{
				status: a.Status, seq: a.StateChangeSeq, hash: hash,
				retryAt: now.Add(retryAfter),
			}
			base.Action = "error"
			base.Reason = err.Error()
			l.statusf("send %s: %v", agentLabel(a), err)
			return l.emit(ctx, base)
		}
	}

	l.mem[a.PaneID] = memory{status: a.Status, seq: a.StateChangeSeq, hash: hash, settled: true}
	return l.emit(ctx, base)
}

func (l *Loop) readScreen(ctx context.Context, a herdrx.Agent) (string, error) {
	target := a.PaneID
	if a.Name != "" {
		target = a.Name
	}
	visible, err := l.Client.ReadAgent(ctx, target, "visible", 50)
	if err == nil && strings.TrimSpace(visible.Text) != "" {
		return visible.Text, nil
	}
	recent, err2 := l.Client.ReadAgent(ctx, target, "recent_unwrapped", 80)
	if err2 != nil {
		if err != nil {
			return "", err
		}
		return "", err2
	}
	return recent.Text, nil
}

func (l *Loop) emit(ctx context.Context, d Decision) error {
	if l.Opts.NotifyHold && (d.Action == "error" || (d.Action == "hold" && d.Judged)) {
		body := d.Reason
		if line := firstLine(d.Proposed); line != "" {
			body = line + " — " + d.Reason
		}
		if _, err := l.Client.Notify(ctx, agentLabel(herdrx.Agent{Name: d.Name, Agent: d.Agent, PaneID: d.PaneID})+" held", body, "request"); err != nil {
			l.statusf("notify %s: %v", d.PaneID, err)
		}
	}
	if l.Emit != nil {
		return l.Emit(d)
	}
	l.statusf("%s %s %s (%s)", d.Action, d.PaneID, d.Reason, d.Proposed)
	return nil
}

func (l *Loop) settle() time.Duration {
	if l.Opts.Settle < 0 {
		return 0
	}
	if l.Opts.Settle == 0 {
		return 300 * time.Millisecond
	}
	return l.Opts.Settle
}

func (l *Loop) ignored(a herdrx.Agent) bool {
	if l.Opts.SelfPane != "" && a.PaneID == l.Opts.SelfPane {
		return true
	}
	for _, raw := range l.Opts.Ignore {
		t := strings.TrimSpace(raw)
		if t == "" {
			continue
		}
		if a.PaneID == t || a.Name == t || a.Agent == t {
			return true
		}
	}
	return false
}

func (l *Loop) statusf(format string, args ...any) {
	if l.Status == nil {
		return
	}
	l.Status(fmt.Sprintf(format, args...))
}

func agentLabel(a herdrx.Agent) string {
	if a.Name != "" {
		return a.Name
	}
	if a.Agent != "" {
		return a.Agent
	}
	return a.PaneID
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}

func paneSet(agents []herdrx.Agent) map[string]struct{} {
	out := make(map[string]struct{}, len(agents))
	for _, a := range agents {
		if a.PaneID != "" {
			out[a.PaneID] = struct{}{}
		}
	}
	return out
}

func sameSet(a, b map[string]struct{}) bool {
	if len(a) != len(b) {
		return false
	}
	for k := range a {
		if _, ok := b[k]; !ok {
			return false
		}
	}
	return true
}
