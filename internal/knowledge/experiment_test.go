package knowledge

import (
	"bufio"
	"cmp"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"slices"
	"strings"
	"testing"
)

const (
	kbPath   = "../../data/knowledge/kb.jsonl"
	evalPath = "../../data/eval_questions.jsonl"
)

type variantResult struct {
	Name                           string
	Chosen                         bool
	Recall, Answerable             int
	Primary, PrimaryTotal          int
	Ev05First                      string
	MaxUnanswerable, MinAnswerable float64
}

func (r variantResult) separable() bool { return r.MinAnswerable > r.MaxUnanswerable }

type variant struct {
	name     string
	chosen   bool
	scope    func(Document) bool 
	analyzer analyzer
	weight   func(Document) float64 
}

func eligible(d Document) bool { return answerCategory(d.Category) && !isSuperseded(d) }

func trustWeight(d Document) float64 {
	switch d.Category {
	case "policy":
		return 1.0
	case "faq", "howto":
		return 0.8
	case "announcement":
		return 0.5
	default:
		return 0.2
	}
}

var variants = []variant{
	{name: "Whole corpus, filtered at query time", scope: all, analyzer: defaultAnalyzer},
	{name: "Whole corpus × trust weight (original plan)", scope: all, analyzer: defaultAnalyzer, weight: trustWeight},
	{name: "Eligible documents only", chosen: true, scope: eligible, analyzer: defaultAnalyzer},
	{name: "Eligible only, no stopwords", scope: eligible, analyzer: analyzer{titleBoost: 1}},
	{name: "Eligible only, stemming", scope: eligible, analyzer: analyzer{stopwords: true, stem: true, titleBoost: 1}},
	{name: "Eligible only, title ×2", scope: eligible, analyzer: analyzer{stopwords: true, titleBoost: 2}},
	{name: "Eligible only, policies ×1.5", scope: eligible, analyzer: defaultAnalyzer, weight: func(d Document) float64 {
		if d.Category == "policy" {
			return 1.5
		}
		return 1
	}},
}

func all(Document) bool { return true }

func runExperiment() ([]variantResult, error) {
	docs, _, err := readDocuments(kbPath)
	if err != nil {
		return nil, err
	}
	questions, err := readRetrievalQuestions(evalPath)
	if err != nil {
		return nil, err
	}
	byID := map[string]Document{}
	for _, d := range docs {
		byID[d.ID] = d
	}

	var results []variantResult
	for _, v := range variants {
		var indexed []Document
		for _, d := range docs {
			if v.scope(d) {
				indexed = append(indexed, d)
			}
		}
		ix := newIndex(indexed, v.analyzer)
		r := variantResult{Name: v.name, Chosen: v.chosen, MinAnswerable: math.Inf(1)}
		for _, q := range questions {
			top := v.rank(ix, byID, q.Q)
			best := 0.0
			if len(top) > 0 {
				best = top[0].score
			}
			if q.ID == "ev_05" && len(top) > 0 {
				r.Ev05First = top[0].id
			}
			if !q.Answerable {
				r.MaxUnanswerable = max(r.MaxUnanswerable, best)
				continue
			}
			r.Answerable++
			r.MinAnswerable = min(r.MinAnswerable, best)
			if slices.ContainsFunc(top, func(s scored) bool { return slices.Contains(q.Expected, s.id) }) {
				r.Recall++
			}
			if len(q.Primary) > 0 {
				r.PrimaryTotal++
				if len(top) > 0 && slices.Contains(q.Primary, top[0].id) {
					r.Primary++
				}
			}
		}
		results = append(results, r)
	}
	return results, nil
}

type scored struct {
	id    string
	score float64
}

func (v variant) rank(ix *index, byID map[string]Document, query string) []scored {
	var out []scored
	for i, s := range ix.score(query) {
		d := byID[ix.ids[i]]
		if s == 0 || !eligible(d) {
			continue
		}
		if v.weight != nil {
			s *= v.weight(d)
		}
		out = append(out, scored{d.ID, s})
	}
	slices.SortFunc(out, func(x, y scored) int { return cmp.Or(cmp.Compare(y.score, x.score), cmp.Compare(x.id, y.id)) })
	return out[:min(len(out), maxResults)]
}

type retrievalQuestion struct {
	ID         string   `json:"id"`
	Q          string   `json:"q"`
	Type       string   `json:"type"`
	Answerable bool     `json:"answerable"`
	Expected   []string `json:"expected_doc_ids"`
	Primary    []string `json:"primary_doc_ids"`
}

func readRetrievalQuestions(path string) ([]retrievalQuestion, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open eval questions: %w", err)
	}
	defer f.Close()
	var out []retrievalQuestion
	sc := bufio.NewScanner(f)
	for line := 1; sc.Scan(); line++ {
		var q retrievalQuestion
		if err := json.Unmarshal(sc.Bytes(), &q); err != nil {
			return nil, fmt.Errorf("eval questions line %d: %w", line, err)
		}
		if q.Type == "retrieval" {
			out = append(out, q)
		}
	}
	return out, sc.Err()
}

var keywordQueries = []retrievalQuestion{
	{Q: "free delivery threshold", Answerable: true, Expected: []string{"pol_delivery_fee_v2"}},
	{Q: "free delivery", Answerable: true, Expected: []string{"pol_delivery_fee_v2"}},
	{Q: "delivery fee", Answerable: true, Expected: []string{"pol_delivery_fee_v2"}},
	{Q: "cancellation policy", Answerable: true, Expected: []string{"pol_cancel", "faq_5"}},
	{Q: "cancel order", Answerable: true, Expected: []string{"pol_cancel", "faq_5"}},
	{Q: "tip courier", Answerable: true, Expected: []string{"pol_tip", "faq_18"}},
	{Q: "refund", Answerable: true, Expected: []string{"pol_refund", "faq_9"}},
	{Q: "minimum order", Answerable: true, Expected: []string{"pol_min_order"}},
	{Q: "energy drink age", Answerable: true, Expected: []string{"pol_age_restricted", "faq_14"}},
	{Q: "wallet insufficient", Answerable: true, Expected: []string{"pol_payment", "faq_11"}},
	{Q: "service area cities", Answerable: true, Expected: []string{"pol_service_area", "faq_20"}},
	{Q: "missing item refund", Answerable: true, Expected: []string{"faq_7", "howto_3"}},
	{Q: "calorie", Answerable: false},
	{Q: "Köfte calories", Answerable: false},
	{Q: "choose courier", Answerable: false},
	{Q: "loyalty points", Answerable: false},
	{Q: "gift card", Answerable: false},
	{Q: "vegan options", Answerable: false},
	{Q: "student discount", Answerable: false},
}

var liveQueries = []retrievalQuestion{
	{Q: "free delivery threshold delivery fee", Answerable: true, Expected: []string{"pol_delivery_fee_v2"}},
	{Q: "free delivery threshold", Answerable: true, Expected: []string{"pol_delivery_fee_v2"}},
	{Q: "delivery fee", Answerable: true, Expected: []string{"pol_delivery_fee_v2"}},
	{Q: "energy drink age restriction purchase requirements", Answerable: true, Expected: []string{"pol_age_restricted", "faq_14"}},
	{Q: "age-restricted items energy drink minimum age", Answerable: true, Expected: []string{"pol_age_restricted", "faq_14"}},
	{Q: "refund destination timing wallet card", Answerable: true, Expected: []string{"pol_refund", "faq_9"}},
	{Q: "card refund how long processing time", Answerable: true, Expected: []string{"pol_refund", "faq_9"}},
	{Q: "cancel order deadline", Answerable: true, Expected: []string{"pol_cancel", "faq_5"}},
	{Q: "order cancellation policy", Answerable: true, Expected: []string{"pol_cancel", "faq_5"}},
	{Q: "cancel order after restaurant started preparing refund", Answerable: true, Expected: []string{"pol_cancel", "faq_5"}},
	{Q: "minimum order value", Answerable: true, Expected: []string{"pol_min_order"}},
	{Q: "tip courier", Answerable: true, Expected: []string{"pol_tip", "faq_18"}},
	{Q: "tip courier commission", Answerable: true, Expected: []string{"pol_tip", "faq_18"}},
	{Q: "insufficient wallet balance payment order", Answerable: true, Expected: []string{"pol_payment", "faq_11"}},
	{Q: "restaurants not shown in list delivery area district", Answerable: true, Expected: []string{"pol_service_area", "faq_4"}},
	{Q: "cities Sofra operates delivery areas", Answerable: true, Expected: []string{"pol_service_area", "faq_20"}},
	{Q: "missing item refund", Answerable: true, Expected: []string{"faq_7", "howto_3"}},
	{Q: "security policy", Answerable: true, Expected: []string{"pol_security"}},
	{Q: "calorie nutrition information menu items", Answerable: false},
	{Q: "choose courier in advance", Answerable: false},
	{Q: "gift cards", Answerable: false},
	{Q: "loyalty program points rewards", Answerable: false},
	{Q: "student discount", Answerable: false},
}

type floorResult struct {
	Floor                          string
	Chosen                         bool
	Set                            string
	Recall, Answerable             int
	Silent, Unanswerable           int
	MinAnswerable, MaxUnanswerable float64
}

type floor struct {
	name    string
	chosen  bool
	measure func(score, coverage float64) float64
	min     float64
}

var floors = []floor{
	{name: "BM25 score ≥ 4.0 (first floor)", measure: func(s, _ float64) float64 { return s }, min: 4.0},
	{name: fmt.Sprintf("Query coverage ≥ %.2f", MinCoverage), chosen: true, measure: func(_, c float64) float64 { return c }, min: MinCoverage},
}

func runFloorExperiment() ([]floorResult, error) {
	b, err := Load(kbPath)
	if err != nil {
		return nil, err
	}
	eval, err := readRetrievalQuestions(evalPath)
	if err != nil {
		return nil, err
	}
	sets := []struct {
		name      string
		questions []retrievalQuestion
	}{{"eval questions (sentences)", eval}, {"keyword queries", keywordQueries}, {"live model queries", liveQueries}}

	var results []floorResult
	for _, f := range floors {
		for _, set := range sets {
			r := floorResult{Floor: f.name, Chosen: f.chosen, Set: set.name, MinAnswerable: math.Inf(1)}
			for _, q := range set.questions {
				best := b.bestMeasure(q.Q, f.measure)
				returned := b.retrieve(q.Q, func(s, c float64) bool { return f.measure(s, c) >= f.min })
				if !q.Answerable {
					r.Unanswerable++
					r.MaxUnanswerable = max(r.MaxUnanswerable, best)
					if len(returned) == 0 {
						r.Silent++
					}
					continue
				}
				r.Answerable++
				r.MinAnswerable = min(r.MinAnswerable, best)
				if slices.ContainsFunc(returned, func(d Result) bool { return slices.Contains(q.Expected, d.ID) }) {
					r.Recall++
				}
			}
			results = append(results, r)
		}
	}
	return results, nil
}

func (b *Base) bestMeasure(query string, measure func(score, coverage float64) float64) float64 {
	scores, coverage := b.index.score(query), b.index.coverage(query)
	var best float64
	for i, s := range scores {
		if s > 0 {
			best = max(best, measure(s, coverage[i]))
		}
	}
	return best
}

func TestRetrievalVariants(t *testing.T) {
	results, err := runExperiment()
	if err != nil {
		t.Fatal(err)
	}
	var tbl strings.Builder
	tbl.WriteString("| Variant | recall@5 | primary@1 | ev_05 first | unanswerable: best score | answerable: lowest best score | separable |\n|---|---|---|---|---|---|---|\n")
	for _, r := range results {
		name := r.Name
		if r.Chosen {
			name = "**" + name + " (chosen)**"
		}
		fmt.Fprintf(&tbl, "| %s | %d/%d | %d/%d | %s | %.2f | %.2f | %s |\n", name, r.Recall, r.Answerable,
			r.Primary, r.PrimaryTotal, r.Ev05First, r.MaxUnanswerable, r.MinAnswerable, yesNo(r.separable()))
	}
	t.Log("\n" + tbl.String())

	i := slices.IndexFunc(results, func(r variantResult) bool { return r.Chosen })
	if c := results[i]; c.Recall != c.Answerable || c.Primary != c.PrimaryTotal || !c.separable() {
		t.Errorf("chosen variant: recall@5 %d/%d, primary@1 %d/%d, separable %t; want all, all, true",
			c.Recall, c.Answerable, c.Primary, c.PrimaryTotal, c.separable())
	}
}

func TestChosenVariantIsProduction(t *testing.T) {
	b, err := Load(kbPath)
	if err != nil {
		t.Fatal(err)
	}
	questions, err := readRetrievalQuestions(evalPath)
	if err != nil {
		t.Fatal(err)
	}
	docs, _, err := readDocuments(kbPath)
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]Document{}
	var indexed []Document
	for _, d := range docs {
		byID[d.ID] = d
	}
	i := slices.IndexFunc(variants, func(v variant) bool { return v.chosen })
	v := variants[i]
	for _, d := range docs {
		if v.scope(d) {
			indexed = append(indexed, d)
		}
	}
	ix := newIndex(indexed, v.analyzer)
	all := append(questions, append(keywordQueries, liveQueries...)...)
	for _, q := range all {
		var got, want []string
		for _, s := range v.rank(ix, byID, q.Q) {
			got = append(got, s.id)
		}
		for _, r := range b.retrieve(q.Q, func(float64, float64) bool { return true }) {
			want = append(want, r.ID)
		}
		if !slices.Equal(got, want) {
			t.Errorf("%q: chosen variant ranks %v, production %v", q.Q, got, want)
		}
	}
}

func TestRelevanceFloor(t *testing.T) {
	results, err := runFloorExperiment()
	if err != nil {
		t.Fatal(err)
	}
	var tbl strings.Builder
	tbl.WriteString("| Floor | Queries | answerable: expected doc returned | unanswerable: nothing returned | answerable: lowest best | unanswerable: highest best | separable |\n|---|---|---|---|---|---|---|\n")
	for _, f := range results {
		name := f.Floor
		if f.Chosen {
			name = "**" + name + " (chosen)**"
		}
		fmt.Fprintf(&tbl, "| %s | %s | %d/%d | %d/%d | %.2f | %.2f | %s |\n", name, f.Set, f.Recall, f.Answerable,
			f.Silent, f.Unanswerable, f.MinAnswerable, f.MaxUnanswerable, yesNo(f.MinAnswerable > f.MaxUnanswerable))
	}
	t.Log("\n" + tbl.String())

	allowedMisses := map[string]int{"live model queries": 1}
	for _, f := range results {
		if !f.Chosen {
			continue
		}
		if f.Silent != f.Unanswerable {
			t.Errorf("%s: %d/%d unanswerable queries returned nothing, want all", f.Set, f.Silent, f.Unanswerable)
		}
		if misses := f.Answerable - f.Recall; misses > allowedMisses[f.Set] {
			t.Errorf("%s: %d answerable queries missed their document, at most %d allowed", f.Set, misses, allowedMisses[f.Set])
		}
	}
}

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}
