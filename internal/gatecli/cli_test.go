package gatecli

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/timjonez/gate/internal/gate"
	"github.com/timjonez/gate/internal/herdrx"
)

type stubClient struct{}

func (stubClient) Socket() string { return "/tmp/fake.sock" }
func (stubClient) Ping(ctx context.Context) (herdrx.Pong, error) {
	return herdrx.Pong{}, nil
}
func (stubClient) ListAgents(ctx context.Context) ([]herdrx.Agent, error) {
	return nil, nil
}
func (stubClient) ReadAgent(ctx context.Context, target, source string, lines int) (herdrx.Read, error) {
	return herdrx.Read{}, nil
}
func (stubClient) Notify(ctx context.Context, title, body, sound string) (herdrx.Notification, error) {
	return herdrx.Notification{}, nil
}
func (stubClient) CreateWorkspace(ctx context.Context, in herdrx.WorkspaceCreate) (herdrx.WorkspaceCreated, error) {
	return herdrx.WorkspaceCreated{}, nil
}
func (stubClient) CloseWorkspace(ctx context.Context, workspaceID string) error { return nil }
func (stubClient) StartAgent(ctx context.Context, in herdrx.AgentStart) (herdrx.Agent, error) {
	return herdrx.Agent{}, nil
}
func (stubClient) GetAgent(ctx context.Context, target string) (herdrx.Agent, error) {
	return herdrx.Agent{}, nil
}
func (stubClient) RenameAgent(ctx context.Context, target, name string) (herdrx.Agent, error) {
	return herdrx.Agent{}, nil
}
func (stubClient) WaitAgent(ctx context.Context, target string, until []string, timeoutMS int) (herdrx.Agent, error) {
	return herdrx.Agent{}, nil
}
func (stubClient) PromptAgent(ctx context.Context, target, text string) (herdrx.Agent, error) {
	return herdrx.Agent{}, nil
}
func (stubClient) SendKeys(ctx context.Context, target string, keys []string) error { return nil }
func (stubClient) Subscribe(ctx context.Context, subs []herdrx.Subscription, handle func(herdrx.Event) error) error {
	<-ctx.Done()
	return ctx.Err()
}

type stubJudge struct{}

func (stubJudge) Judge(ctx context.Context, state gate.State) (gate.Answers, error) {
	return gate.Answers{}, nil
}

func newTestApp(t *testing.T) (*App, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	var out, errb bytes.Buffer
	app := NewApp()
	app.Stdout = &out
	app.Stderr = &errb
	app.now = func() time.Time { return time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC) }
	app.newClient = func(socket string) (herdrx.Client, error) { return stubClient{}, nil }
	return app, &out, &errb
}

func TestHelpMentionsLooseAndNeverRefuses(t *testing.T) {
	app, out, errb := newTestApp(t)
	if code := app.Execute([]string{"--help"}); code != 0 {
		t.Fatalf("help: %s", errb.String())
	}
	help := out.String()
	for _, want := range []string{"--loose", "never presses", "TYPESAFE_API_KEY", "worktree", "terraform"} {
		if !strings.Contains(help, want) {
			t.Fatalf("help missing %q:\n%s", want, help)
		}
	}
}

func TestJudgeSetupFailure(t *testing.T) {
	app, _, errb := newTestApp(t)
	app.newJudge = func() (gate.Judge, error) {
		return nil, errors.New("TYPESAFE_API_KEY is not set")
	}
	if code := app.Execute([]string{}); code == 0 {
		t.Fatal("expected judge setup to fail")
	}
	if !strings.Contains(errb.String(), "TYPESAFE_API_KEY") {
		t.Fatalf("stderr: %s", errb.String())
	}
}

func TestLooseFlagWiresModeAndDoesNotStartTheLoop(t *testing.T) {
	app, _, errb := newTestApp(t)
	app.newJudge = func() (gate.Judge, error) { return stubJudge{}, nil }
	var got gate.Options
	app.runLoop = func(loop *gate.Loop) error {
		got = loop.Opts
		return nil
	}
	if code := app.Execute([]string{"--loose", "--dry-run", "--notify", "--max-needs-human", "0.3"}); code != 0 {
		t.Fatalf("code err: %s", errb.String())
	}
	if got.Mode != gate.ModeLoose || !got.DryRun || !got.NotifyHold {
		t.Fatalf("opts %+v", got)
	}
	if got.Thresholds.MaxNeedsHuman != 0.3 {
		t.Fatalf("threshold %+v", got.Thresholds)
	}
}

func TestVersion(t *testing.T) {
	app, out, errb := newTestApp(t)
	if code := app.Execute([]string{"version"}); code != 0 {
		t.Fatalf("version: %s", errb.String())
	}
	if strings.TrimSpace(out.String()) != Version {
		t.Fatalf("version %q", out.String())
	}
}

func TestFormatDecision(t *testing.T) {
	got := formatDecision(gate.Decision{
		Action: "allow", DryRun: true, Name: "worker", PaneID: "w1:p2",
		Reason: "ordinary step of the visible task", Key: "1", Proposed: "go test ./...",
	})
	if !strings.Contains(got, "dry-run") || !strings.Contains(got, "key 1") || !strings.Contains(got, "go test ./...") {
		t.Fatalf("format %q", got)
	}
	held := formatDecision(gate.Decision{
		Action: "hold", Name: "worker", PaneID: "w1:p2",
		Reason: "left for you: reads a secret", Key: "1", Proposed: "cat .env",
	})
	if strings.Contains(held, "key 1") || !strings.Contains(held, "left for you") {
		t.Fatalf("held format %q", held)
	}
}
