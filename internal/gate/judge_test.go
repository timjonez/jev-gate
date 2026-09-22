package gate

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRequestBodyAsksThreeQuestions(t *testing.T) {
	body, err := RequestBody(State{Agent: "grok", Screen: "go test ./..."}, "")
	if err != nil {
		t.Fatal(err)
	}
	var payload struct {
		Model     string         `json:"model"`
		State     State          `json:"state"`
		Questions map[string]any `json:"questions"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Model != "jev-latest" {
		t.Fatalf("model %s", payload.Model)
	}
	if payload.State.Screen != "go test ./..." || payload.State.Policy == "" {
		t.Fatalf("state %+v", payload.State)
	}
	for _, id := range []string{"prompt_kind", "appropriate", "needs_human"} {
		if _, ok := payload.Questions[id]; !ok {
			t.Fatalf("missing %s", id)
		}
	}
	if _, ok := payload.Questions["reads_secret"]; ok {
		t.Fatal("strict body includes a loose question")
	}
	if payload.State.Mode != ModeStrict {
		t.Fatalf("mode %s", payload.State.Mode)
	}
}

func TestRequestBodyLooseAsksRiskQuestions(t *testing.T) {
	body, err := RequestBody(State{Screen: "terraform apply", Mode: ModeLoose}, "")
	if err != nil {
		t.Fatal(err)
	}
	var payload struct {
		State     State          `json:"state"`
		Questions map[string]any `json:"questions"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.State.Mode != ModeLoose {
		t.Fatalf("mode %s", payload.State.Mode)
	}
	if !strings.Contains(payload.State.Policy, "without refusing") {
		t.Fatalf("policy %q", payload.State.Policy)
	}
	for _, id := range []string{"prompt_kind", "reads_secret", "deletes_outside", "infra_apply"} {
		if _, ok := payload.Questions[id]; !ok {
			t.Fatalf("missing %s", id)
		}
	}
	if _, ok := payload.Questions["appropriate"]; ok {
		t.Fatal("loose body still asks the strict appropriate question")
	}
	infra, _ := json.Marshal(payload.Questions["infra_apply"])
	if !strings.Contains(string(infra), "terraform destroy") {
		t.Fatalf("infra question %s", infra)
	}
	deletes, _ := json.Marshal(payload.Questions["deletes_outside"])
	if !strings.Contains(string(deletes), "session") {
		t.Fatalf("deletes question %s", deletes)
	}
}

func TestHTTPJudge(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"model": "jev-1.13.0",
			"answers": {
				"prompt_kind": {
					"type": "choice",
					"choice": "command_approval",
					"confidence": 0.91,
					"probabilities": {"command_approval": 0.86, "human_question": 0.1, "not_a_prompt": 0.04}
				},
				"appropriate": {"type": "noul", "noul": 0.94},
				"needs_human": {"type": "noul", "noul": 0.03}
			}
		}`))
	}))
	defer srv.Close()

	j := NewHTTPJudge("test-key")
	j.URL = srv.URL
	ans, err := j.Judge(context.Background(), State{Screen: "go test"})
	if err != nil {
		t.Fatal(err)
	}
	if gotAuth != "Bearer test-key" {
		t.Fatalf("auth %q", gotAuth)
	}
	if ans.Kind != "command_approval" || ans.KindConfidence != 0.91 || ans.CommandProbability != 0.86 {
		t.Fatalf("kind %+v", ans)
	}
	if ans.Appropriate != 0.94 || ans.NeedsHuman != 0.03 {
		t.Fatalf("nouls %+v", ans)
	}
}

func TestDecodeLooseAnswers(t *testing.T) {
	raw := []byte(`{
		"answers": {
			"prompt_kind": {
				"type": "choice",
				"choice": "command_approval",
				"confidence": 0.9,
				"probabilities": {"command_approval": 0.8, "human_question": 0.1, "not_a_prompt": 0.1}
			},
			"reads_secret": {"type": "noul", "noul": 0.91},
			"deletes_outside": {"type": "noul", "noul": 0.02},
			"infra_apply": {"type": "noul", "noul": 0.04}
		}
	}`)
	ans, err := decodeAnswers(raw, ModeLoose)
	if err != nil {
		t.Fatal(err)
	}
	if ans.ReadsSecret != 0.91 || ans.DeletesOutside != 0.02 || ans.InfraApply != 0.04 {
		t.Fatalf("answers %+v", ans)
	}
	if _, err := decodeAnswers(raw, ModeStrict); err == nil {
		t.Fatal("strict decode accepted a loose body")
	}
	if _, err := decodeAnswers([]byte(`{"answers":{"prompt_kind":{"type":"choice","choice":"command_approval","confidence":0.9,"probabilities":{"command_approval":0.8}}}}`), ModeLoose); err == nil {
		t.Fatal("loose decode accepted a body without the risk scores")
	}
}

func TestHTTPJudgeRejectsUnauthorized(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "no", http.StatusUnauthorized)
	}))
	defer srv.Close()
	j := NewHTTPJudge("nope")
	j.URL = srv.URL
	if _, err := j.Judge(context.Background(), State{Screen: "x"}); err == nil || !strings.Contains(err.Error(), "401") {
		t.Fatalf("err %v", err)
	}
}

func TestLoadAPIKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "key")
	if _, err := loadAPIKey("", path); err == nil {
		t.Fatal("expected missing key")
	}
	if err := os.WriteFile(path, []byte("  from-file \n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := loadAPIKey("", path)
	if err != nil || got != "from-file" {
		t.Fatalf("file key %q %v", got, err)
	}
	got, err = loadAPIKey(" from-env ", path)
	if err != nil || got != "from-env" {
		t.Fatalf("env wins %q %v", got, err)
	}
}
