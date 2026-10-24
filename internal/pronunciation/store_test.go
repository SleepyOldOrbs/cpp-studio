package pronunciation

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStoreExplicitValidatedImport(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rules.json")
	s := NewStore(path)
	initial := Document{Version: 1, Rules: []Rule{{"James", "Jaymz", "en"}}}
	if _, err := s.Replace(initial); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(path)
	for _, input := range []string{`{`, `{"version":2,"rules":[]}`, strings.Repeat(" ", MaxDocumentBytes+1)} {
		if _, err := s.Import(strings.NewReader(input), "replace"); err == nil {
			t.Fatal("accepted invalid import")
		}
		after, _ := os.ReadFile(path)
		if string(before) != string(after) {
			t.Fatal("invalid import changed store")
		}
	}
	data, _ := json.Marshal(Document{Version: 1, Rules: []Rule{{"York", "Yawk", ""}}})
	if _, err := s.Import(strings.NewReader(string(data)), ""); err == nil {
		t.Fatal("mode was implicit")
	}
	merged, err := s.Import(strings.NewReader(string(data)), "merge")
	if err != nil || len(merged.Rules) != 2 {
		t.Fatal(merged, err)
	}
	conflict, _ := json.Marshal(Document{Version: 1, Rules: []Rule{{"James", "other", "en"}}})
	if _, err := s.Import(strings.NewReader(string(conflict)), "merge"); err == nil {
		t.Fatal("conflicting merge accepted")
	}
	loaded, err := NewStore(path).Load()
	if err != nil || len(loaded.Rules) != 2 {
		t.Fatal(loaded, err)
	}
}
