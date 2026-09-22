package gate

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const defaultModel = "jev-latest"
const defaultURL = "https://api.typesafe.ai/v1/systemone"

// State is the screen Jev judges. Question ids are not part of the prompt.
type State struct {
	Agent  string `json:"agent,omitempty"`
	Name   string `json:"name,omitempty"`
	Title  string `json:"title,omitempty"`
	Screen string `json:"screen"`
	Policy string `json:"policy"`
	Mode   Mode   `json:"mode,omitempty"`
}

// Judge asks Jev whether the visible card is an ordinary single allow.
type Judge interface {
	Judge(ctx context.Context, state State) (Answers, error)
}

// HTTPJudge calls the System One endpoint.
type HTTPJudge struct {
	Key    string
	Model  string
	URL    string
	Client *http.Client
}

// NewHTTPJudge builds a judge for key. Model and URL fall back to the defaults.
func NewHTTPJudge(key string) *HTTPJudge {
	return &HTTPJudge{
		Key:   strings.TrimSpace(key),
		Model: defaultModel,
		URL:   defaultURL,
		Client: &http.Client{
			Timeout: 20 * time.Second,
		},
	}
}

func (h *HTTPJudge) Judge(ctx context.Context, state State) (Answers, error) {
	if strings.TrimSpace(h.Key) == "" {
		return Answers{}, fmt.Errorf("typesafe: empty API key")
	}
	model := h.Model
	if model == "" {
		model = defaultModel
	}
	url := h.URL
	if url == "" {
		url = defaultURL
	}
	body, err := RequestBody(state, model)
	if err != nil {
		return Answers{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return Answers{}, err
	}
	req.Header.Set("Authorization", "Bearer "+h.Key)
	req.Header.Set("Content-Type", "application/json")
	client := h.Client
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return Answers{}, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return Answers{}, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return Answers{}, fmt.Errorf("typesafe: %s", resp.Status)
	}
	return decodeAnswers(raw, state.Mode)
}

// RequestBody is the System One JSON for state.
func RequestBody(state State, model string) ([]byte, error) {
	if model == "" {
		model = defaultModel
	}
	if state.Mode == "" {
		state.Mode = ModeStrict
	}
	if strings.TrimSpace(state.Policy) == "" {
		state.Policy = policyText(state.Mode)
	}
	state.Screen = Tail(state.Screen, 80, 6000)
	payload := map[string]any{
		"model":     model,
		"state":     state,
		"questions": questionsFor(state.Mode),
	}
	return json.Marshal(payload)
}

func decodeAnswers(raw []byte, mode Mode) (Answers, error) {
	var resp struct {
		Answers map[string]json.RawMessage `json:"answers"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return Answers{}, fmt.Errorf("typesafe: decode response: %w", err)
	}
	if resp.Answers == nil {
		return Answers{}, fmt.Errorf("typesafe: response has no answers")
	}
	var kind struct {
		Type          string             `json:"type"`
		Choice        string             `json:"choice"`
		Confidence    float64            `json:"confidence"`
		Probabilities map[string]float64 `json:"probabilities"`
	}
	if err := json.Unmarshal(resp.Answers["prompt_kind"], &kind); err != nil {
		return Answers{}, fmt.Errorf("typesafe: prompt_kind: %w", err)
	}
	if kind.Type != "choice" || kind.Choice == "" {
		return Answers{}, fmt.Errorf("typesafe: prompt_kind missing choice")
	}
	p, ok := kind.Probabilities["command_approval"]
	if !ok {
		return Answers{}, fmt.Errorf("typesafe: prompt_kind missing command_approval probability")
	}
	out := Answers{
		Kind:               kind.Choice,
		KindConfidence:     kind.Confidence,
		CommandProbability: p,
	}
	if mode == ModeLoose {
		var err error
		if out.ReadsSecret, err = decodeNoul(resp.Answers["reads_secret"], "reads_secret"); err != nil {
			return Answers{}, err
		}
		if out.DeletesOutside, err = decodeNoul(resp.Answers["deletes_outside"], "deletes_outside"); err != nil {
			return Answers{}, err
		}
		if out.InfraApply, err = decodeNoul(resp.Answers["infra_apply"], "infra_apply"); err != nil {
			return Answers{}, err
		}
		return out, nil
	}
	var err error
	if out.Appropriate, err = decodeNoul(resp.Answers["appropriate"], "appropriate"); err != nil {
		return Answers{}, err
	}
	if out.NeedsHuman, err = decodeNoul(resp.Answers["needs_human"], "needs_human"); err != nil {
		return Answers{}, err
	}
	return out, nil
}

func decodeNoul(raw json.RawMessage, name string) (float64, error) {
	if len(raw) == 0 {
		return 0, fmt.Errorf("typesafe: %s missing", name)
	}
	var n struct {
		Type string  `json:"type"`
		Noul float64 `json:"noul"`
	}
	if err := json.Unmarshal(raw, &n); err != nil {
		return 0, fmt.Errorf("typesafe: %s: %w", name, err)
	}
	if n.Type != "noul" {
		return 0, fmt.Errorf("typesafe: %s is not a noul", name)
	}
	return n.Noul, nil
}

// LoadAPIKey reads TYPESAFE_API_KEY, then ~/.typesafe_key.
func LoadAPIKey() (string, error) {
	path := ""
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		path = filepath.Join(home, ".typesafe_key")
	}
	return loadAPIKey(os.Getenv("TYPESAFE_API_KEY"), path)
}

func loadAPIKey(envVal, path string) (string, error) {
	if key := strings.TrimSpace(envVal); key != "" {
		return key, nil
	}
	if path == "" {
		return "", fmt.Errorf("TYPESAFE_API_KEY is not set")
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("TYPESAFE_API_KEY is not set and %s is unreadable: %w", path, err)
	}
	key := strings.TrimSpace(string(b))
	if key == "" {
		return "", fmt.Errorf("TYPESAFE_API_KEY is not set and %s is empty", path)
	}
	return key, nil
}
