package llm

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

type evalFile struct {
	Cases []evalCase `json:"cases"`
}

type evalCase struct {
	ID                string   `json:"id"`
	Headlines         []string `json:"headlines"`
	ExpectPromptError bool     `json:"expect_prompt_error"`
}

func TestEvalCases_promptConstruction(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller")
	}
	path := filepath.Join(filepath.Dir(thisFile), "..", "..", "eval", "cases.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var file evalFile
	if err := json.Unmarshal(raw, &file); err != nil {
		t.Fatal(err)
	}
	if n := len(file.Cases); n < 10 || n > 20 {
		t.Fatalf("eval set size=%d, want 10–20", n)
	}

	var sawEmpty, sawDup, sawInject bool
	for _, tc := range file.Cases {
		prompt, err := BuildPrompt(tc.Headlines)
		if tc.ExpectPromptError {
			if err == nil {
				t.Errorf("%s: expected BuildPrompt error", tc.ID)
			}
			if tc.ID == "empty" || tc.ID == "whitespace_only" {
				sawEmpty = true
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: BuildPrompt: %v", tc.ID, err)
			continue
		}
		if !strings.Contains(prompt, "<untrusted-data>") || !strings.Contains(prompt, "</untrusted-data>") {
			t.Errorf("%s: missing untrusted wrapper", tc.ID)
		}
		if strings.Contains(prompt, "You are Neuro-Occultist") {
			t.Errorf("%s: system instruction leaked into user prompt", tc.ID)
		}
		body := between(prompt, "<untrusted-data>", "</untrusted-data>")
		if strings.Contains(body, "</untrusted-data>") {
			t.Errorf("%s: delimiter breakout in body", tc.ID)
		}
		if tc.ID == "duplicates" {
			sawDup = true
			if strings.Count(prompt, "Go 1.23 released with range-over-func") < 2 {
				t.Errorf("%s: duplicates should be passed through (no silent dedup)", tc.ID)
			}
		}
		if strings.HasPrefix(tc.ID, "injection_") {
			sawInject = true
		}
		if tc.ID == "nine_items_capped" && strings.Contains(prompt, "Story 9") {
			t.Errorf("%s: prompt cap of 8 items not applied", tc.ID)
		}
	}
	if !sawEmpty || !sawDup || !sawInject {
		t.Fatalf("eval set must include empty, duplicates, and injection cases")
	}
}
