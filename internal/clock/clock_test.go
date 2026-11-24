package clock

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"sofra/internal/domain"
)

func TestRunningMovesFromReference(t *testing.T) {
	ref, err := ParseReference("")
	if err != nil {
		t.Fatal(err)
	}
	c := NewRunning(ref)
	first := c.Now()
	if first.Before(ref) || first.Sub(ref) > time.Second {
		t.Fatalf("starts at %v, want the reference %v", first, ref)
	}
	time.Sleep(20 * time.Millisecond)
	if later := c.Now(); !later.After(first) {
		t.Errorf("did not move: %v then %v", first, later)
	}
}

func TestParseReference(t *testing.T) {
	istanbul := func(y int, m time.Month, d, h, min int) time.Time {
		return time.Date(y, m, d, h, min, 0, 0, domain.Istanbul)
	}
	cases := []struct {
		name, value string
		want        time.Time
		wantDate    string
	}{
		{"empty: the case study's today, at midday", "", istanbul(2026, 8, 20, 12, 0), "2026-08-20"},
		{"bare date: midday in Istanbul", "2026-08-27", istanbul(2026, 8, 27, 12, 0), "2026-08-27"},
		{"RFC 3339 in Istanbul", "2026-08-20T09:30:00+03:00", istanbul(2026, 8, 20, 9, 30), "2026-08-20"},
		{"RFC 3339 in another zone", "2026-08-20T22:30:00Z", istanbul(2026, 8, 21, 1, 30), "2026-08-21"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := ParseReference(c.value)
			if err != nil {
				t.Fatal(err)
			}
			if !got.Equal(c.want) || got.Location() != domain.Istanbul {
				t.Errorf("got %v, want %v in Istanbul", got, c.want)
			}
			if date := domain.FormatDate(got); date != c.wantDate {
				t.Errorf("shown as %s, want %s", date, c.wantDate)
			}
		})
	}
	for _, bad := range []string{"tomorrow", "2026-13-01", "2026-08-20T12:00"} {
		if _, err := ParseReference(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestFixedDoesNotGoBack(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("Advance with a negative duration moved the clock back")
		}
	}()
	NewFixed(time.Now()).Advance(-time.Minute)
}

func TestNoSystemClockOutsideClock(t *testing.T) {
	banned := map[string]bool{"Now": true, "Since": true, "Until": true}
	root := filepath.Join("..", "..")
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			hidden := path != root && strings.HasPrefix(d.Name(), ".")
			if hidden || d.Name() == "vendor" || path == filepath.Join(root, "internal", "clock") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		f, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.SkipObjectResolution)
		if err != nil {
			return err
		}
		name := ""
		for _, imp := range f.Imports {
			if p, _ := strconv.Unquote(imp.Path.Value); p == "time" {
				name = "time"
				if imp.Name != nil {
					name = imp.Name.Name
				}
			}
		}
		if name == "" {
			return nil
		}
		ast.Inspect(f, func(n ast.Node) bool {
			if sel, ok := n.(*ast.SelectorExpr); ok && banned[sel.Sel.Name] {
				if x, ok := sel.X.(*ast.Ident); ok && x.Name == name {
					t.Errorf("%s calls time.%s; read an injected clock.Clock instead", path, sel.Sel.Name)
				}
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
