package gate

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/timjonez/gate/internal/herdrx"
)

type fakeClient struct {
	mu     sync.Mutex
	agents []herdrx.Agent
	text   string
	seq    []string
	n      int
	keys   [][]string
	notes  []string
}

func (f *fakeClient) Socket() string { return "/tmp/fake.sock" }
func (f *fakeClient) Ping(ctx context.Context) (herdrx.Pong, error) {
	return herdrx.Pong{}, nil
}
func (f *fakeClient) ListAgents(ctx context.Context) ([]herdrx.Agent, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]herdrx.Agent, len(f.agents))
	copy(out, f.agents)
	return out, nil
}
func (f *fakeClient) ReadAgent(ctx context.Context, target, source string, lines int) (herdrx.Read, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	text := f.text
	if len(f.seq) > 0 {
		i := f.n
		if i >= len(f.seq) {
			i = len(f.seq) - 1
		}
		f.n++
		text = f.seq[i]
	}
	return herdrx.Read{Text: text}, nil
}
func (f *fakeClient) Notify(ctx context.Context, title, body, sound string) (herdrx.Notification, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.notes = append(f.notes, title)
	return herdrx.Notification{Shown: true}, nil
}
func (f *fakeClient) CreateWorkspace(ctx context.Context, in herdrx.WorkspaceCreate) (herdrx.WorkspaceCreated, error) {
	return herdrx.WorkspaceCreated{}, nil
}
func (f *fakeClient) CloseWorkspace(ctx context.Context, workspaceID string) error { return nil }
func (f *fakeClient) StartAgent(ctx context.Context, in herdrx.AgentStart) (herdrx.Agent, error) {
	return herdrx.Agent{}, nil
}
func (f *fakeClient) GetAgent(ctx context.Context, target string) (herdrx.Agent, error) {
	return herdrx.Agent{}, nil
}
func (f *fakeClient) RenameAgent(ctx context.Context, target, name string) (herdrx.Agent, error) {
	return herdrx.Agent{}, nil
}
func (f *fakeClient) WaitAgent(ctx context.Context, target string, until []string, timeoutMS int) (herdrx.Agent, error) {
	return herdrx.Agent{}, nil
}
func (f *fakeClient) PromptAgent(ctx context.Context, target, text string) (herdrx.Agent, error) {
	return herdrx.Agent{}, nil
}
func (f *fakeClient) SendKeys(ctx context.Context, target string, keys []string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.keys = append(f.keys, append([]string{target}, keys...))
	return nil
}
func (f *fakeClient) Subscribe(ctx context.Context, subs []herdrx.Subscription, handle func(herdrx.Event) error) error {
	<-ctx.Done()
	return ctx.Err()
}

type scriptJudge struct {
	mu    sync.Mutex
	ans   Answers
	err   error
	calls int
	last  State
}

func (s *scriptJudge) Judge(ctx context.Context, state State) (Answers, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	s.last = state
	return s.ans, s.err
}

func claudeCard() string {
	return "" +
		"Fix the login redirect\n" +
		"Bash command\n" +
		"\n" +
		"  go test ./...\n" +
		"\n" +
		"Do you want to proceed?\n" +
		"❯ 1. Yes\n" +
		"  2. Yes, and don't ask again for go test commands in this project\n" +
		"  3. No\n"
}

func newLoop(fc *fakeClient, j *scriptJudge) (*Loop, *[]Decision) {
	var got []Decision
	l := &Loop{
		Client: fc,
		Judge:  j,
		Opts: Options{
			Settle:     -1,
			Thresholds: DefaultThresholds(),
			Now:        func() time.Time { return time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC) },
		},
		Emit: func(d Decision) error {
			got = append(got, d)
			return nil
		},
	}
	return l, &got
}

func blocked(name string) herdrx.Agent {
	return herdrx.Agent{
		Agent: "claude", Name: name, Status: "blocked",
		PaneID: "w1:p2", StateChangeSeq: 4,
		Title: "fix the login redirect",
	}
}

func TestConsiderAllowsOnce(t *testing.T) {
	fc := &fakeClient{text: claudeCard()}
	j := &scriptJudge{ans: allowAnswers()}
	l, got := newLoop(fc, j)
	a := blocked("worker")
	if err := l.consider(context.Background(), a); err != nil {
		t.Fatal(err)
	}
	if err := l.consider(context.Background(), a); err != nil {
		t.Fatal(err)
	}
	if j.calls != 1 {
		t.Fatalf("judge calls %d", j.calls)
	}
	if len(fc.keys) != 1 || fc.keys[0][0] != "worker" || fc.keys[0][1] != "1" {
		t.Fatalf("keys %+v", fc.keys)
	}
	if len(*got) != 1 || (*got)[0].Action != "allow" || !(*got)[0].Judged {
		t.Fatalf("decisions %+v", *got)
	}
	if !strings.Contains(j.last.Screen, "go test ./...") {
		t.Fatalf("screen %q", j.last.Screen)
	}
}

func TestConsiderDryRunDoesNotSend(t *testing.T) {
	fc := &fakeClient{text: claudeCard()}
	j := &scriptJudge{ans: allowAnswers()}
	l, got := newLoop(fc, j)
	l.Opts.DryRun = true
	if err := l.consider(context.Background(), blocked("worker")); err != nil {
		t.Fatal(err)
	}
	if len(fc.keys) != 0 {
		t.Fatalf("keys %+v", fc.keys)
	}
	if len(*got) != 1 || !(*got)[0].DryRun || (*got)[0].Action != "allow" {
		t.Fatalf("decisions %+v", *got)
	}
}

func TestConsiderHoldsDestructive(t *testing.T) {
	fc := &fakeClient{text: claudeCard()}
	ans := allowAnswers()
	ans.NeedsHuman = 0.9
	j := &scriptJudge{ans: ans}
	l, got := newLoop(fc, j)
	l.Opts.NotifyHold = true
	if err := l.consider(context.Background(), blocked("worker")); err != nil {
		t.Fatal(err)
	}
	if len(fc.keys) != 0 {
		t.Fatalf("keys %+v", fc.keys)
	}
	if (*got)[0].Action != "hold" || (*got)[0].Reason != "needs a person" {
		t.Fatalf("decisions %+v", *got)
	}
	if len(fc.notes) != 1 {
		t.Fatalf("notes %+v", fc.notes)
	}
}

func TestConsiderLooseAllowsOffTaskWithoutPressingNo(t *testing.T) {
	fc := &fakeClient{text: claudeCard()}
	ans := allowAnswers()
	ans.Appropriate = 0.05
	ans.NeedsHuman = 0.99
	ans.ReadsSecret = 0.01
	ans.DeletesOutside = 0.02
	ans.InfraApply = 0.03
	j := &scriptJudge{ans: ans}
	l, got := newLoop(fc, j)
	l.Opts.Mode = ModeLoose
	if err := l.consider(context.Background(), blocked("worker")); err != nil {
		t.Fatal(err)
	}
	if len(fc.keys) != 1 || len(fc.keys[0]) != 2 || fc.keys[0][1] != "1" {
		t.Fatalf("keys %+v", fc.keys)
	}
	if (*got)[0].Action != "allow" || (*got)[0].Reason != "allowed under the loose policy" {
		t.Fatalf("decisions %+v", *got)
	}
	if j.last.Mode != ModeLoose {
		t.Fatalf("judge mode %s", j.last.Mode)
	}
}

func TestConsiderLooseLeavesSecretAndPressesNothing(t *testing.T) {
	fc := &fakeClient{text: claudeCard()}
	ans := allowAnswers()
	ans.ReadsSecret = 0.93
	j := &scriptJudge{ans: ans}
	l, got := newLoop(fc, j)
	l.Opts.Mode = ModeLoose
	l.Opts.NotifyHold = true
	if err := l.consider(context.Background(), blocked("worker")); err != nil {
		t.Fatal(err)
	}
	if len(fc.keys) != 0 {
		t.Fatalf("keys %+v", fc.keys)
	}
	if (*got)[0].Action != "hold" || (*got)[0].Reason != "left for you: reads a secret" {
		t.Fatalf("decisions %+v", *got)
	}
	if len(fc.notes) != 1 || !strings.Contains(fc.notes[0], "held") {
		t.Fatalf("notes %+v", fc.notes)
	}
}

func TestConsiderStaleScreenDoesNotSend(t *testing.T) {
	fc := &fakeClient{seq: []string{claudeCard(), "Ready.\nAll tests passed."}}
	j := &scriptJudge{ans: allowAnswers()}
	l, got := newLoop(fc, j)
	if err := l.consider(context.Background(), blocked("worker")); err != nil {
		t.Fatal(err)
	}
	if len(fc.keys) != 0 {
		t.Fatalf("keys %+v", fc.keys)
	}
	if (*got)[0].Action != "hold" || (*got)[0].Reason != "screen changed before allow" {
		t.Fatalf("decisions %+v", *got)
	}
}

func TestConsiderSkipsNonCardsAndSelf(t *testing.T) {
	fc := &fakeClient{text: "Which database?\n❯ 1. Postgres\n  2. SQLite\n"}
	j := &scriptJudge{ans: allowAnswers()}
	l, got := newLoop(fc, j)
	a := blocked("worker")
	if err := l.consider(context.Background(), a); err != nil {
		t.Fatal(err)
	}
	idle := a
	idle.Status = "idle"
	idle.StateChangeSeq = 5
	fc.text = "Ready."
	if err := l.consider(context.Background(), idle); err != nil {
		t.Fatal(err)
	}
	l.Opts.SelfPane = "w9:p1"
	self := blocked("me")
	self.PaneID = "w9:p1"
	fc.text = claudeCard()
	if err := l.consider(context.Background(), self); err != nil {
		t.Fatal(err)
	}
	if j.calls != 0 {
		t.Fatalf("judge calls %d", j.calls)
	}
	if len(fc.keys) != 0 {
		t.Fatalf("keys %+v", fc.keys)
	}
	if len(*got) != 1 || (*got)[0].Reason != "not a command prompt" {
		t.Fatalf("decisions %+v", *got)
	}
}

func TestConsiderSettleObservesCancel(t *testing.T) {
	fc := &fakeClient{text: claudeCard()}
	j := &scriptJudge{ans: allowAnswers()}
	l, _ := newLoop(fc, j)
	l.Opts.Settle = time.Hour
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- l.consider(ctx, blocked("worker"))
	}()
	time.Sleep(20 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("settle did not observe cancel")
	}
	if j.calls != 0 || len(fc.keys) != 0 {
		t.Fatalf("calls %d keys %+v", j.calls, fc.keys)
	}
}

func TestConsiderRetriesJudgeLater(t *testing.T) {
	fc := &fakeClient{text: claudeCard()}
	j := &scriptJudge{err: errors.New("typesafe: 529")}
	l, _ := newLoop(fc, j)
	now := time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC)
	l.Opts.Now = func() time.Time { return now }
	a := blocked("worker")
	if err := l.consider(context.Background(), a); err != nil {
		t.Fatal(err)
	}
	if err := l.consider(context.Background(), a); err != nil {
		t.Fatal(err)
	}
	if j.calls != 1 {
		t.Fatalf("calls before retry %d", j.calls)
	}
	now = now.Add(16 * time.Second)
	j.err = nil
	j.ans = allowAnswers()
	if err := l.consider(context.Background(), a); err != nil {
		t.Fatal(err)
	}
	if j.calls != 2 || len(fc.keys) != 1 {
		t.Fatalf("calls %d keys %+v", j.calls, fc.keys)
	}
}
