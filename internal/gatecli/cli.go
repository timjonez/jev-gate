package gatecli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"github.com/timjonez/gate/internal/gate"
	"github.com/timjonez/gate/internal/herdrx"
)

// Version is set at build time via -ldflags or defaults here.
var Version = "0.1.0"

// App holds the gate command's process state.
type App struct {
	Stdout  io.Writer
	Stderr  io.Writer
	Quiet   bool
	JSON    bool
	Socket  string
	Session string

	newClient func(socket string) (herdrx.Client, error)
	newJudge  func() (gate.Judge, error)
	runLoop   func(*gate.Loop) error
	now       func() time.Time
	selfPane  string
}

// NewApp constructs an App with process defaults.
func NewApp() *App {
	return &App{
		Stdout: os.Stdout,
		Stderr: os.Stderr,
		now:    func() time.Time { return time.Now().UTC() },
		newClient: func(socket string) (herdrx.Client, error) {
			return herdrx.Dial(socket)
		},
		selfPane: os.Getenv("HERDR_PANE_ID"),
	}
}

// Execute runs the gate command. Returns a process exit code.
func (a *App) Execute(args []string) int {
	root := a.rootCmd()
	root.SetOut(a.Stdout)
	root.SetErr(a.Stderr)
	if args != nil {
		root.SetArgs(args)
	}
	if err := root.Execute(); err != nil {
		if a.JSON {
			_ = writeJSON(a.Stderr, map[string]string{"error": err.Error()})
		} else {
			fmt.Fprintln(a.Stderr, err.Error())
		}
		return 1
	}
	return 0
}

func (a *App) rootCmd() *cobra.Command {
	var (
		ignore         []string
		dryRun         bool
		notify         bool
		loose          bool
		model          string
		minAppropriate float64
		maxNeedsHuman  float64
	)
	root := &cobra.Command{
		Use:   "gate",
		Short: "Allow Herdr command prompts with TypeSafe Jev",
		Long: `Watch every live Herdr agent. When a manual-mode session shows a tool permission card, ask TypeSafe Jev whether to press the single-shot allow.

By default, allow once only when that invocation is an ordinary step of the visible task. Publishing, broad deletion, spending, shared-system changes, and secret exposure stay on screen.

--loose allows almost every single invocation, including work that is off the visible task. It still leaves the card on screen when the command would read or expose a secret, delete or destroy something outside the agent's worktree (session artifacts excepted), or apply or destroy infrastructure such as terraform apply or destroy. Unclear cases in that set stay on screen too.

Held cards are left for you. gate never presses No, always-allow, or a session-wide option.

Requires TYPESAFE_API_KEY or ~/.typesafe_key. --dry-run logs the judgment and does not send keys.`,
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if minAppropriate < 0 || minAppropriate > 1 || maxNeedsHuman < 0 || maxNeedsHuman > 1 {
				return fmt.Errorf("thresholds must be between 0 and 1")
			}
			c, err := a.client()
			if err != nil {
				return err
			}
			j, err := a.judge(model)
			if err != nil {
				return err
			}
			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer stop()

			mode := gate.ModeStrict
			if loose {
				mode = gate.ModeLoose
			}
			th := gate.DefaultThresholds()
			if minAppropriate > 0 {
				th.MinAppropriate = minAppropriate
			}
			if maxNeedsHuman > 0 {
				th.MaxNeedsHuman = maxNeedsHuman
			}
			loop := &gate.Loop{
				Client: c,
				Judge:  j,
				Opts: gate.Options{
					Ignore:     ignore,
					SelfPane:   a.selfPane,
					DryRun:     dryRun,
					NotifyHold: notify,
					Mode:       mode,
					Thresholds: th,
					Now:        a.now,
				},
				Status: func(msg string) {
					if a.Quiet {
						return
					}
					fmt.Fprintln(a.Stderr, msg)
				},
				Emit: func(d gate.Decision) error {
					if a.JSON {
						return writeJSON(a.Stdout, d)
					}
					if a.Quiet {
						return nil
					}
					fmt.Fprintln(a.Stdout, formatDecision(d))
					return nil
				},
			}
			if a.runLoop != nil {
				return a.runLoop(loop)
			}
			err = loop.Run(ctx)
			if err == context.Canceled || err == context.DeadlineExceeded {
				return nil
			}
			return err
		},
	}
	root.PersistentFlags().StringVar(&a.Socket, "socket", "", "Herdr Unix socket (default: HERDR_SOCKET_PATH or ~/.config/herdr/herdr.sock)")
	root.PersistentFlags().StringVar(&a.Session, "session", "", "Herdr session name (default: HERDR_SESSION)")
	root.PersistentFlags().BoolVarP(&a.Quiet, "quiet", "q", false, "minimal human output")
	root.PersistentFlags().BoolVar(&a.JSON, "json", false, "JSON output on stdout; errors as JSON on stderr")
	root.Flags().StringArrayVar(&ignore, "ignore", nil, "pane id or agent name to skip (repeatable)")
	root.Flags().BoolVar(&dryRun, "dry-run", false, "log judgments without sending keys")
	root.Flags().BoolVar(&notify, "notify", false, "toast when a judged command is left for you")
	root.Flags().BoolVar(&loose, "loose", false, "allow almost every command; leave secrets, out-of-worktree deletes, and infrastructure apply or destroy on screen")
	root.Flags().StringVar(&model, "model", "jev-latest", "TypeSafe model")
	root.Flags().Float64Var(&minAppropriate, "min-appropriate", 0.85, "minimum Jev probability that the command is an ordinary step (strict mode)")
	root.Flags().Float64Var(&maxNeedsHuman, "max-needs-human", 0.20, "maximum Jev probability of a risk that should stay on screen")
	root.AddCommand(a.versionCmd())
	return root
}

func (a *App) versionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print gate version",
		RunE: func(cmd *cobra.Command, args []string) error {
			if a.JSON {
				return writeJSON(a.Stdout, map[string]string{"version": Version})
			}
			fmt.Fprintln(a.Stdout, Version)
			return nil
		},
	}
}

func (a *App) client() (herdrx.Client, error) {
	socket := herdrx.ResolveSocket(a.Socket, a.Session)
	if a.newClient != nil {
		return a.newClient(socket)
	}
	return herdrx.Dial(socket)
}

func (a *App) judge(model string) (gate.Judge, error) {
	if a.newJudge != nil {
		return a.newJudge()
	}
	key, err := gate.LoadAPIKey()
	if err != nil {
		return nil, err
	}
	h := gate.NewHTTPJudge(key)
	if model != "" {
		h.Model = model
	}
	return h, nil
}

func formatDecision(d gate.Decision) string {
	label := d.Name
	if label == "" {
		label = d.Agent
	}
	if label == "" {
		label = d.PaneID
	}
	action := d.Action
	if d.DryRun && d.Action == "allow" {
		action = "dry-run"
	}
	msg := fmt.Sprintf("%s  %s  %s  %s", action, label, d.PaneID, d.Reason)
	if d.Action == "allow" && d.Key != "" {
		msg += "  key " + d.Key
	}
	if line := strings.TrimSpace(d.Proposed); line != "" {
		if i := strings.IndexByte(line, '\n'); i >= 0 {
			line = line[:i]
		}
		msg += "  " + line
	}
	return msg
}

func writeJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	return enc.Encode(v)
}
