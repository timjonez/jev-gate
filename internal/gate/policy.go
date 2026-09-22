package gate

import "strings"

// Mode selects which policy the gate asks Jev to apply.
type Mode string

const (
	// ModeStrict allows one ordinary local step and leaves everything else.
	ModeStrict Mode = "strict"
	// ModeLoose allows almost every single invocation. Secret reads, deletes
	// outside the worktree, and infrastructure apply or destroy stay on screen.
	ModeLoose Mode = "loose"
)

// Answers is one Jev response, reduced to the fields the gate acts on.
type Answers struct {
	Kind               string
	KindConfidence     float64
	CommandProbability float64
	Appropriate        float64
	NeedsHuman         float64
	// Loose-mode risks. A high score leaves the card for the person.
	ReadsSecret    float64
	DeletesOutside float64
	InfraApply     float64
}

// Thresholds are the code-owned bars for pressing allow once.
type Thresholds struct {
	MinAppropriate float64
	MaxNeedsHuman  float64
	MinConfidence  float64
	MinKindProb    float64
}

// DefaultThresholds leave anything uncertain, off-task, or harmful on screen.
func DefaultThresholds() Thresholds {
	return Thresholds{
		MinAppropriate: 0.85,
		MaxNeedsHuman:  0.20,
		MinConfidence:  0.65,
		MinKindProb:    0.70,
	}
}

// fillThresholds replaces unset (zero) fields with the defaults. A zero max
// would otherwise allow nothing through the needs-human check by accident
// when the caller only set the other fields.
func fillThresholds(th Thresholds) Thresholds {
	def := DefaultThresholds()
	if th.MinAppropriate <= 0 {
		th.MinAppropriate = def.MinAppropriate
	}
	if th.MaxNeedsHuman <= 0 {
		th.MaxNeedsHuman = def.MaxNeedsHuman
	}
	if th.MinConfidence <= 0 {
		th.MinConfidence = def.MinConfidence
	}
	if th.MinKindProb <= 0 {
		th.MinKindProb = def.MinKindProb
	}
	return th
}

// Decide turns Jev's answers into allow or hold. Hold leaves the card on
// screen; it is not a refusal. In strict mode a high needs-human score wins
// over an on-task score. In loose mode only the held-action scores count, so
// an off-task command is allowed when it is not one of those.
func Decide(a Answers, th Thresholds, mode Mode) (action, reason string) {
	if th == (Thresholds{}) {
		th = DefaultThresholds()
	}
	if mode == "" {
		mode = ModeStrict
	}
	if a.Kind != "command_approval" {
		return "hold", "not a command approval"
	}
	if a.KindConfidence < th.MinConfidence || a.CommandProbability < th.MinKindProb {
		return "hold", "unsure this is a command approval"
	}
	if mode == ModeLoose {
		if why, ok := looseHold(a, th); ok {
			return "hold", why
		}
		return "allow", "allowed under the loose policy"
	}
	if a.NeedsHuman > th.MaxNeedsHuman {
		return "hold", "needs a person"
	}
	if a.Appropriate < th.MinAppropriate {
		return "hold", "not an ordinary step of the visible task"
	}
	return "allow", "ordinary step of the visible task"
}

// looseHold reports the risks that crossed the bar, in a stable order.
// Several can fire on one card; the person still sees a single untouched card.
func looseHold(a Answers, th Thresholds) (string, bool) {
	var whys []string
	if a.ReadsSecret > th.MaxNeedsHuman {
		whys = append(whys, "reads a secret")
	}
	if a.DeletesOutside > th.MaxNeedsHuman {
		whys = append(whys, "deletes outside the worktree")
	}
	if a.InfraApply > th.MaxNeedsHuman {
		whys = append(whys, "infrastructure apply or destroy")
	}
	if len(whys) == 0 {
		return "", false
	}
	return "left for you: " + strings.Join(whys, "; "), true
}
