---
name: gate
description: Allow Herdr tool-permission cards with the gate command and TypeSafe Jev. Use when the user mentions gate, gate --loose, or allowing agent command prompts. herd pending, herd watch, and herd new belong to the herd skill.
---

# gate

`gate` is its own command, from the `gate` repo. It watches live Herdr agents and may press a single-shot allow on a tool permission card when TypeSafe Jev agrees. It does not answer questions, and it does not select always-allow or No.

By default it allows one ordinary local step. `gate --loose` allows almost every single invocation, and leaves the card on screen when the command would read a secret, delete something outside the agent's worktree (session artifacts excepted), or apply or destroy infrastructure.

`--dry-run` logs the judgment and sends nothing. The key is `TYPESAFE_API_KEY` or `~/.typesafe_key`.

Do not start `gate` unless the user asked. Do not send "continue" or other input to keep an agent moving.

```bash
gate --dry-run
gate
gate --loose
```
