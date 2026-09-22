package gate

func policyText(mode Mode) string {
	if mode == ModeLoose {
		return loosePolicy
	}
	return strictPolicy
}

const strictPolicy = "Coding agents are in manual permission mode. Allow one invocation only when it is an ordinary next step of the work visible on the screen: local reads, searches, tests, builds, formatting, edits to those files, git inspection, or installing this project's dependencies. Leave publishing, broad deletion, spending, shared or production changes, secret exposure, and anything unclear for the person. Never treat always-allow as the decision."

const loosePolicy = "Coding agents are in manual permission mode. Allow one invocation of almost any visible tool call, including edits, tests, installs, git commands, and work that is not the task on screen. Leave the card for the person, without refusing it, when that invocation would read or expose secret material, delete or destroy something outside the agent's worktree (session artifacts excepted), or apply or destroy infrastructure. If it is unclear whether the command does one of those, leave it. Never treat always-allow as the decision, and never choose a refusal option."

func questionsFor(mode Mode) map[string]any {
	if mode == ModeLoose {
		return map[string]any{
			"prompt_kind":     promptKindQuestion(),
			"reads_secret":    readsSecretQuestion(),
			"deletes_outside": deletesOutsideQuestion(),
			"infra_apply":     infraApplyQuestion(),
		}
	}
	return map[string]any{
		"prompt_kind": promptKindQuestion(),
		"appropriate": appropriateQuestion(),
		"needs_human": needsHumanQuestion(),
	}
}

func promptKindQuestion() map[string]any {
	return map[string]any{
		"type": "choice",
		"instructions": map[string]any{
			"question": "What is the live prompt at the end of `screen` asking the person to do?",
			"focus":    "Judge the open card only. An approval mentioned earlier in the scrollback is not live.",
		},
		"criteria": map[string]any{
			"command_approval": map[string]any{
				"what":    "A live card asking to allow one shell command, file edit, or other tool call. The proposed action is visible.",
				"not_for": "A planning question, a design choice, or a transcript that only mentions an old approval.",
			},
			"human_question": map[string]any{
				"what": "The agent wants a human choice, a plan confirmation, or a written answer. No single tool invocation is waiting to be allowed.",
			},
			"not_a_prompt": map[string]any{
				"what": "No live card. Idle transcript, a status footer, or finished work.",
			},
		},
	}
}

func appropriateQuestion() map[string]any {
	return map[string]any{
		"type": "noul",
		"instructions": map[string]any{
			"question": "Is the proposed single invocation an ordinary next step of the work visible on `screen`?",
			"inspect":  []string{"`screen`", "`title`"},
			"focus":    "On-task versus off-task. Destructiveness is a separate question.",
		},
		"criteria": map[string]any{
			"true": map[string]any{
				"what": "Local reads, searches, tests, lint, builds, formatting, edits to files the visible task is about, git inspection, or installing this project's dependencies.",
				"examples": []string{
					"go test ./... while the screen is about a failing test",
					"an edit to the login redirect named in `title`",
					"git diff",
				},
			},
			"false": map[string]any{
				"what": "The action is unrelated to the visible task, or the screen does not show what would run.",
				"examples": []string{
					"curl to a paste site while the task is a unit test",
					"a card with no command and no path",
				},
			},
		},
	}
}

func needsHumanQuestion() map[string]any {
	return map[string]any{
		"type": "noul",
		"instructions": map[string]any{
			"question": "Should a person decide this single invocation because allowing it could destroy data, publish, spend, change a shared system, or expose a secret?",
			"inspect":  []string{"`screen`"},
			"focus":    "The effect of allowing once. Work that is on-task can still need a person.",
		},
		"criteria": map[string]any{
			"true": map[string]any{
				"what": "Broad delete, force push, deploy, infra apply, spending money, sending messages, production or shared-system changes, or printing or shipping secrets. Also yes when the effect is unclear.",
				"examples": []string{
					"git push",
					"rm -rf",
					"terraform apply",
					"a curl that posts a token",
					"kubectl delete",
				},
			},
			"false": map[string]any{
				"what": "Ordinary local development whose effect stays on this machine and in this project.",
				"examples": []string{
					"go test",
					"npm test",
					"an edit to a source file the task is changing",
					"git status",
					"cargo build",
				},
			},
		},
	}
}

func readsSecretQuestion() map[string]any {
	return map[string]any{
		"type": "noul",
		"instructions": map[string]any{
			"question": "Would this single invocation read, print, copy, or send secret material?",
			"inspect":  []string{"`screen`"},
			"focus":    "Credentials, tokens, private keys, and files that hold them. Ordinary source is not secret material.",
		},
		"criteria": map[string]any{
			"true": map[string]any{
				"what": "Reading, printing, copying, or transmitting credentials, tokens, private keys, or secret files such as .env, cloud credentials, or ssh private keys. Also yes when the command might expose a secret and the screen does not show enough to rule that out.",
				"examples": []string{
					"cat .env",
					"printing a cloud credential or an ssh private key",
					"a curl that posts a token",
				},
			},
			"false": map[string]any{
				"what": "Reading or diffing ordinary source, tests, logs, or project config that is not secret material.",
				"examples": []string{
					"cat main.go",
					"git diff",
					"reading a README",
				},
			},
		},
	}
}

func deletesOutsideQuestion() map[string]any {
	return map[string]any{
		"type": "noul",
		"instructions": map[string]any{
			"question": "Would this single invocation delete or destroy data outside the agent's worktree, other than session artifacts?",
			"inspect":  []string{"`screen`"},
			"focus":    "The worktree is the project directory visible on the screen. A relative path is inside it. Session artifacts are this session's temp files, caches, and session directories, including under /tmp or the agent's own cache, even when that path is outside the worktree.",
		},
		"criteria": map[string]any{
			"true": map[string]any{
				"what": "Deleting, shredding, truncating, or wiping a path outside that worktree, or destroying data that is not a file in the worktree. Also yes when an absolute path is not shown clearly enough to place it inside the worktree or among session artifacts. Wiping a shared directory such as all of /tmp, rather than this session's files, is yes.",
				"examples": []string{
					"rm -rf ~",
					"rm of a path in another project",
					"shred /etc/hosts",
					"a delete whose path is not visible",
				},
			},
			"false": map[string]any{
				"what": "A delete inside the worktree, including a relative path such as a build directory or node_modules. Also removing this session's own temp, cache, or session files.",
				"examples": []string{
					"rm a file under the repo the screen is working in",
					"rm -rf node_modules",
					"rm /tmp/claude-session-abc or the agent's own cache",
				},
			},
		},
	}
}

func infraApplyQuestion() map[string]any {
	return map[string]any{
		"type": "noul",
		"instructions": map[string]any{
			"question": "Would this single invocation apply or destroy shared infrastructure?",
			"inspect":  []string{"`screen`"},
			"focus":    "Terraform, OpenTofu, and Terragrunt apply or destroy, and the same kind of command against shared cloud or cluster resources. A read-only plan is not this.",
		},
		"criteria": map[string]any{
			"true": map[string]any{
				"what": "terraform, tofu, opentofu, or terragrunt apply or destroy. Also the same kind of action that creates or destroys shared infrastructure or cloud resources, such as pulumi up, pulumi destroy, or kubectl delete. Also yes when the command might be one of these and the screen does not show enough to rule that out.",
				"examples": []string{
					"terraform apply",
					"terraform destroy",
					"tofu destroy",
					"terragrunt apply",
					"pulumi destroy",
					"kubectl delete",
				},
			},
			"false": map[string]any{
				"what": "A read-only or local infrastructure command, or any command that does not apply or destroy shared infrastructure.",
				"examples": []string{
					"terraform plan",
					"terraform fmt",
					"kubectl get pods",
					"go test",
				},
			},
		},
	}
}
