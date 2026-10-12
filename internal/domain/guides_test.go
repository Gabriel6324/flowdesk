package domain_test

import (
	"encoding/json"
	"os"
	"regexp"
	"strings"
	"testing"
)

func TestEveryClassifiedErrorHasAnIndividualActionableGuide(t *testing.T) {
	raw, e := os.ReadFile("guides.json")
	if e != nil {
		t.Fatal(e)
	}
	var guides map[string]struct {
		Title string   `json:"title"`
		Steps []string `json:"steps"`
	}
	if e = json.Unmarshal(raw, &guides); e != nil {
		t.Fatal(e)
	}
	for code, g := range guides {
		if strings.TrimSpace(g.Title) == "" || len(g.Steps) < 2 {
			t.Errorf("insufficient guide %s", code)
		}
	}
	pattern := regexp.MustCompile(`(?:domain\.)?E\("([A-Z][A-Z0-9_]+)"\)`)
	matches := 0
	for _, dir := range []string{"../service", "../worker", "../ai", "../repository", "../httpapi", "../platform", "../jsonx"} {
		entries, e := os.ReadDir(dir)
		if e != nil {
			t.Fatal(e)
		}
		for _, entry := range entries {
			if !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
				continue
			}
			raw, e := os.ReadFile(dir + "/" + entry.Name())
			if e != nil {
				t.Fatal(e)
			}
			for _, m := range pattern.FindAllSubmatch(raw, -1) {
				matches++
				if _, ok := guides[string(m[1])]; !ok {
					t.Errorf("missing guide %s in %s/%s", m[1], dir, entry.Name())
				}
			}
		}
	}
	if matches < 50 {
		t.Fatalf("error guide scan unexpectedly found only %d occurrences", matches)
	}
}
