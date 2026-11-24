package knowledge

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadRefusesInvalidLines(t *testing.T) {
	raw, err := os.ReadFile(kbPath)
	if err != nil {
		t.Fatal(err)
	}
	extra := "{not json\n" +
		`{"id": "pol_tip", "title": "Copy", "body": "A different text.", "category": "policy", "tags": [], "date": null}` + "\n"
	path := filepath.Join(t.TempDir(), "kb.jsonl")
	if err := os.WriteFile(path, append(raw, []byte(extra)...), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err = Load(path)
	if err == nil || !strings.Contains(err.Error(), "2 line(s)") || !strings.Contains(err.Error(), "line ") ||
		!strings.Contains(err.Error(), "pol_tip appears twice") {
		t.Errorf("error %v, want the broken line and the repeated id", err)
	}
	if _, err := Load(kbPath); err != nil {
		t.Errorf("the shipped knowledge base was refused: %v", err)
	}
	if _, err := Load(filepath.Join(t.TempDir(), "missing.jsonl")); err == nil {
		t.Error("a missing knowledge base was accepted")
	}
}
