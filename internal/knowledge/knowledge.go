package knowledge

import (
	"bufio"
	"cmp"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"slices"
	"strings"
)

const (
	maxResults = 5
	MinCoverage = 0.35
)

type Document struct {
	ID       string
	Title    string
	Body     string
	Category string
	Tags     []string
	Date     string 
}

type Result struct {
	Document
	Score    float64
	Outdated []Document
}

type Base struct {
	docs     map[string]Document
	index    *index
	outdated map[string][]Document 
}

func Load(path string) (*Base, error) {
	docs, invalid, err := readDocuments(path)
	if err != nil {
		return nil, err
	}

	b := &Base{docs: map[string]Document{}, outdated: map[string][]Document{}}
	var current, superseded []Document
	for _, d := range docs {
		if _, dup := b.docs[d.ID]; dup {
			invalid = append(invalid, fmt.Sprintf("%s appears twice", d.ID))
			continue
		}
		b.docs[d.ID] = d
		switch {
		case !answerCategory(d.Category):
		case isSuperseded(d):
			superseded = append(superseded, d)
		default:
			current = append(current, d)
		}
	}
	for _, old := range superseded {
		for _, c := range current {
			if family(c.ID) == family(old.ID) {
				b.outdated[c.ID] = append(b.outdated[c.ID], old)
			}
		}
	}
	if len(invalid) > 0 {
		return nil, fmt.Errorf("invalid knowledge base %s, %d line(s):\n  %s", path, len(invalid), strings.Join(invalid, "\n  "))
	}
	b.index = newIndex(current, defaultAnalyzer)
	return b, nil
}

func (b *Base) Search(query string) []Result {
	return b.retrieve(query, func(_, coverage float64) bool { return coverage >= MinCoverage })
}

func (b *Base) retrieve(query string, keep func(score, coverage float64) bool) []Result {
	scores, coverage := b.index.score(query), b.index.coverage(query)
	var results []Result
	for i, s := range scores {
		if s > 0 && keep(s, coverage[i]) {
			id := b.index.ids[i]
			results = append(results, Result{Document: b.docs[id], Score: s, Outdated: b.outdated[id]})
		}
	}
	slices.SortFunc(results, func(x, y Result) int {
		return cmp.Or(cmp.Compare(y.Score, x.Score), cmp.Compare(x.ID, y.ID))
	})
	return results[:min(len(results), maxResults)]
}

func (b *Base) CurrentPolicy(id string) (string, bool) {
	d, ok := b.docs[id]
	if !ok || d.Category != "policy" || isSuperseded(d) {
		return "", false
	}
	return d.Body, true
}

func answerCategory(category string) bool {
	return category == "policy" || category == "faq" || category == "howto"
}

var (
	supersededID  = regexp.MustCompile(`(_v0|_old|_archive)$`)
	supersededTag = regexp.MustCompile(`(?i)\((legacy|archive)`)
	versionSuffix = regexp.MustCompile(`(_v\d+)?(_old|_archive)?$`)
)

func isSuperseded(d Document) bool {
	return supersededID.MatchString(d.ID) || slices.Contains(d.Tags, "archive") || supersededTag.MatchString(d.Title)
}

func family(id string) string {
	return versionSuffix.ReplaceAllString(id, "")
}

func readDocuments(path string) (docs []Document, invalid []string, err error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, fmt.Errorf("open knowledge base: %w", err)
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for line := 1; sc.Scan(); line++ {
		if strings.TrimSpace(sc.Text()) == "" {
			continue
		}
		var raw struct {
			ID       string   `json:"id"`
			Title    string   `json:"title"`
			Body     string   `json:"body"`
			Category string   `json:"category"`
			Tags     []string `json:"tags"`
			Date     *string  `json:"date"`
		}
		if err := json.Unmarshal(sc.Bytes(), &raw); err != nil || raw.ID == "" {
			if err == nil {
				err = fmt.Errorf("no id")
			}
			invalid = append(invalid, fmt.Sprintf("line %d: %v", line, err))
			continue
		}
		d := Document{ID: raw.ID, Title: raw.Title, Body: raw.Body, Category: raw.Category, Tags: raw.Tags}
		if raw.Date != nil {
			d.Date = *raw.Date
		}
		docs = append(docs, d)
	}
	if err := sc.Err(); err != nil {
		return nil, nil, fmt.Errorf("read knowledge base: %w", err)
	}
	return docs, invalid, nil
}
