package knowledge

import (
	"math"
	"slices"
	"strings"
	"testing"
)

func doc(id, body string) Document { return Document{ID: id, Body: body} }

func scoreOf(ix *index, query, id string) float64 {
	return ix.score(query)[slices.Index(ix.ids, id)]
}

func TestTokenize(t *testing.T) {
	got := defaultAnalyzer.tokenize("Sofra operates only in İstanbul, Kadıköy!")
	want := []string{"sofra", "operates", "only", "istanbul", "kadıköy"}
	if !slices.Equal(got, want) {
		t.Errorf("tokens %q, want %q", got, want)
	}
	ix := newIndex([]Document{doc("city", "Sofra operates only in İstanbul."), doc("other", "Tips are optional.")}, defaultAnalyzer)
	if scoreOf(ix, "Istanbul", "city") == 0 {
		t.Error(`"Istanbul" does not match a document about "İstanbul"`)
	}
}

func TestBM25(t *testing.T) {
	ix := newIndex([]Document{
		doc("short", "refund"),
		doc("long", "refund alpha bravo charlie delta echo foxtrot golf hotel"),
		doc("third", "wallet"),
	}, defaultAnalyzer)
	if scoreOf(ix, "refund", "short") <= scoreOf(ix, "refund", "long") {
		t.Error("a long document scores as high as a short one with the same term once")
	}

	ix = newIndex([]Document{doc("a", "card refund"), doc("b", "card wallet"), doc("c", "card tip")}, defaultAnalyzer)
	if scoreOf(ix, "refund", "a") <= scoreOf(ix, "card", "a") {
		t.Error("a rare term weighs no more than a common one")
	}

	ix = newIndex([]Document{
		doc("once", "tip a1 a2 a3 a4 a5 a6 a7 a8 a9"),
		doc("ten", strings.Repeat("tip ", 10)),
		doc("none", "wallet"),
	}, defaultAnalyzer)
	ratio := scoreOf(ix, "tip", "ten") / scoreOf(ix, "tip", "once")
	if ratio <= 1 || ratio >= 3 {
		t.Errorf("tf 10 scores %.2f× tf 1, want more than 1× and well under 10×", ratio)
	}
}

func TestCoverage(t *testing.T) {
	ix := newIndex([]Document{
		doc("refund", "card refund"), doc("card", "card voucher"), doc("tip", "courier tip"),
	}, defaultAnalyzer)
	at := func(id, query string) float64 { return ix.coverage(query)[slices.Index(ix.ids, id)] }

	if a, b := at("card", "card refund"), at("card", "card card card refund"); math.Abs(a-b) > 1e-9 {
		t.Errorf("coverage %.3f, with the word repeated %.3f", a, b)
	}
	if got := at("refund", "card refund"); math.Abs(got-1) > 1e-9 {
		t.Errorf("full coverage %.3f, want 1", got)
	}
	if got := at("refund", "lottery card"); got >= 0.5 {
		t.Errorf("coverage of a query half about nothing indexed: %.3f, want under 0.5", got)
	}
}
