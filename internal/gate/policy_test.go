package gate

import "testing"

func allowAnswers() Answers {
	return Answers{
		Kind:               "command_approval",
		KindConfidence:     0.9,
		CommandProbability: 0.88,
		Appropriate:        0.93,
		NeedsHuman:         0.04,
	}
}

func TestDecide(t *testing.T) {
	th := DefaultThresholds()
	tests := []struct {
		name   string
		ans    Answers
		action string
		reason string
	}{
		{name: "ordinary command", ans: allowAnswers(), action: "allow", reason: "ordinary step of the visible task"},
		{
			name:   "push still needs a person",
			ans:    func() Answers { a := allowAnswers(); a.NeedsHuman = 0.81; return a }(),
			action: "hold",
			reason: "needs a person",
		},
		{
			name:   "off task",
			ans:    func() Answers { a := allowAnswers(); a.Appropriate = 0.4; return a }(),
			action: "hold",
			reason: "not an ordinary step of the visible task",
		},
		{
			name:   "question",
			ans:    func() Answers { a := allowAnswers(); a.Kind = "human_question"; return a }(),
			action: "hold",
			reason: "not a command approval",
		},
		{
			name:   "low confidence",
			ans:    func() Answers { a := allowAnswers(); a.KindConfidence = 0.4; return a }(),
			action: "hold",
			reason: "unsure this is a command approval",
		},
		{
			name:   "split kind probability",
			ans:    func() Answers { a := allowAnswers(); a.CommandProbability = 0.5; return a }(),
			action: "hold",
			reason: "unsure this is a command approval",
		},
		{
			name:   "exact appropriate boundary allows",
			ans:    func() Answers { a := allowAnswers(); a.Appropriate = th.MinAppropriate; return a }(),
			action: "allow",
		},
		{
			name:   "exact needs-human boundary allows",
			ans:    func() Answers { a := allowAnswers(); a.NeedsHuman = th.MaxNeedsHuman; return a }(),
			action: "allow",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			action, reason := Decide(tt.ans, th, ModeStrict)
			if action != tt.action {
				t.Fatalf("action %s (%s)", action, reason)
			}
			if tt.reason != "" && reason != tt.reason {
				t.Fatalf("reason %q", reason)
			}
		})
	}
}

func TestDecideLoose(t *testing.T) {
	th := DefaultThresholds()
	tests := []struct {
		name   string
		ans    Answers
		action string
		reason string
	}{
		{
			name: "off task command is allowed",
			ans: func() Answers {
				a := allowAnswers()
				a.Appropriate = 0.1
				a.NeedsHuman = 0.95
				return a
			}(),
			action: "allow",
			reason: "allowed under the loose policy",
		},
		{
			name: "secret stays on screen",
			ans: func() Answers {
				a := allowAnswers()
				a.ReadsSecret = 0.91
				return a
			}(),
			action: "hold",
			reason: "left for you: reads a secret 0.91",
		},
		{
			name: "delete outside the worktree stays on screen",
			ans: func() Answers {
				a := allowAnswers()
				a.DeletesOutside = 0.8
				return a
			}(),
			action: "hold",
			reason: "left for you: deletes outside the worktree 0.80",
		},
		{
			name: "terraform apply stays on screen",
			ans: func() Answers {
				a := allowAnswers()
				a.InfraApply = 0.77
				return a
			}(),
			action: "hold",
			reason: "left for you: infrastructure apply or destroy 0.77",
		},
		{
			name: "several risks are named and the card is not refused",
			ans: func() Answers {
				a := allowAnswers()
				a.ReadsSecret = 0.9
				a.InfraApply = 0.6
				return a
			}(),
			action: "hold",
			reason: "left for you: reads a secret 0.90; infrastructure apply or destroy 0.60",
		},
		{
			name:   "exact risk boundary allows",
			ans:    func() Answers { a := allowAnswers(); a.ReadsSecret = th.MaxLooseRisk; return a }(),
			action: "allow",
		},
		{
			name: "a mild secret hunch above the strict bar is allowed",
			ans: func() Answers {
				a := allowAnswers()
				a.ReadsSecret = 0.35
				return a
			}(),
			action: "allow",
			reason: "allowed under the loose policy",
		},
		{
			name: "question is left alone",
			ans: func() Answers {
				a := allowAnswers()
				a.Kind = "human_question"
				return a
			}(),
			action: "hold",
			reason: "not a command approval",
		},
		{
			name: "low confidence is left alone",
			ans: func() Answers {
				a := allowAnswers()
				a.KindConfidence = 0.2
				return a
			}(),
			action: "hold",
			reason: "unsure this is a command approval",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			action, reason := Decide(tt.ans, th, ModeLoose)
			if action != tt.action {
				t.Fatalf("action %s (%s)", action, reason)
			}
			if tt.reason != "" && reason != tt.reason {
				t.Fatalf("reason %q", reason)
			}
			if action == "hold" && (reason == "no" || reason == "deny") {
				t.Fatalf("loose hold must not be a refusal: %q", reason)
			}
		})
	}
}

func TestDecideLooseUsesItsOwnBarNotNeedsHuman(t *testing.T) {
	th := DefaultThresholds()
	th.MaxNeedsHuman = 0.9
	th.MaxLooseRisk = 0.1
	a := allowAnswers()
	a.ReadsSecret = 0.2
	if action, reason := Decide(a, th, ModeLoose); action != "hold" || reason != "left for you: reads a secret 0.20" {
		t.Fatalf("got %s %q", action, reason)
	}
}

func TestDecideFillsPartialThresholds(t *testing.T) {
	a := allowAnswers()
	a.ReadsSecret = 0.6
	if action, _ := Decide(a, Thresholds{MaxNeedsHuman: 0.3}, ModeLoose); action != "hold" {
		t.Fatalf("unset loose bar must fall back to the default, got %s", action)
	}
}

func TestFillThresholdsKeepsExplicitValues(t *testing.T) {
	got := fillThresholds(Thresholds{MinAppropriate: 0.5, MaxNeedsHuman: 0.1, MaxLooseRisk: 0.7})
	if got.MinAppropriate != 0.5 || got.MaxNeedsHuman != 0.1 || got.MaxLooseRisk != 0.7 {
		t.Fatalf("explicit fields changed: %+v", got)
	}
	if got.MinConfidence != DefaultThresholds().MinConfidence {
		t.Fatalf("confidence not filled: %+v", got)
	}
}
