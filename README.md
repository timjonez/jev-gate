# gate

`gate` watches live [Herdr](https://herdr.dev) agents and may press a single-shot allow on a tool permission card. TypeSafe Jev makes the judgment. The card stays up when the command should be left for a person.

This is a separate tool from `herd`. `herd` queues decisions and launches workspaces. `gate` is the command that types an allow.

## Install

```bash
go install github.com/timjonez/gate/cmd/gate@latest
# or from a checkout:
make install
```

Requires Go 1.25+ and a running Herdr server.

## Commands

```text
gate [--dry-run] [--loose] [--notify] [--ignore TARGET]...
     [--model jev-latest]
     [--min-appropriate 0.85] [--max-needs-human 0.20] [--max-risk 0.50]
     [--max-judges-per-minute 30]
     [--socket PATH] [--session NAME] [--json] [--quiet]
gate version
```

By default, allow once is pressed only when the invocation is an ordinary step of the work on screen (probability at least 0.85) and a person is unlikely to be needed (at most 0.20). Always-allow, "don't ask again", and open questions stay on screen. `git push`, deploys, and `rm -rf` stay with you even when that is what the agent was asked to do.

`--loose` allows almost every single invocation, including work that is off the visible task: edits, tests, installs, commits, and pushes. The card stays on screen when the command would print, copy, or send a secret's value, delete or destroy something outside the agent's worktree (this session's temp, cache, and session files excepted), or apply or destroy infrastructure (`terraform apply`, `terraform destroy`, and the same kind of command). If Jev cannot tell whether a command is in that set, the card stays up. A CLI that signs in with its own stored credentials (`gh`, `git push`, `slackcli`, `aws`) is not a secret read. Each of the three risks holds the card when Jev scores it above `--max-risk` (default 0.50); `--max-needs-human` applies to strict mode only. The hold reason names the risk and its score.

The only key `gate` sends is the single-shot allow. Held cards are left for you.

```bash
export TYPESAFE_API_KEY=...   # or put the key in ~/.typesafe_key
gate --dry-run                # log judgments, send nothing
gate                          # press allow once when Jev agrees
gate --loose                  # allow almost everything; leave the risky cards
```

`--notify` toasts a command left for you. It does not toast allows.

Each card is judged once per pane, keyed on the card's command and options rather than the whole screen, so spinners or output elsewhere on the pane do not trigger another TypeSafe request. `--max-judges-per-minute` caps requests across all panes as a backstop; when the cap is hit, cards wait and are retried once the minute clears.

## How it connects

Resolution order for the Herdr socket:

1. `--socket` / `HERDR_SOCKET_PATH`
2. `--session` / `HERDR_SESSION` → `~/.config/herdr/sessions/<name>/herdr.sock`
3. `~/.config/herdr/herdr.sock`

When `HERDR_PANE_ID` is set, that pane is ignored. `--ignore` accepts a pane id or a live agent name.

The worktree used by `--loose` is the project directory visible on the agent's screen. Herdr does not report the agent's directory. A delete whose path is not clearly inside that directory, and is not a session artifact, stays on screen.

## Development

```bash
make test
make build   # ./bin/gate
```
