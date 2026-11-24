package knowledge

import (
	"math"
	"slices"
	"strings"
	"unicode"
)

const (
	k1 = 1.2
	b  = 0.75
)

type analyzer struct {
	stopwords  bool
	stem       bool
	titleBoost int 
}

var defaultAnalyzer = analyzer{stopwords: true, titleBoost: 1}

type index struct {
	analyzer analyzer
	ids      []string
	tf       []map[string]int
	length   []int
	df       map[string]int
	avgLen   float64
}

func newIndex(docs []Document, a analyzer) *index {
	ix := &index{analyzer: a, df: map[string]int{}}
	total := 0
	for _, d := range docs {
		terms := a.tokenize(strings.Repeat(d.Title+" ", a.titleBoost) + d.Body + " " + strings.Join(d.Tags, " "))
		tf := map[string]int{}
		for _, t := range terms {
			tf[t]++
		}
		for t := range tf {
			ix.df[t]++
		}
		ix.ids = append(ix.ids, d.ID)
		ix.tf = append(ix.tf, tf)
		ix.length = append(ix.length, len(terms))
		total += len(terms)
	}
	if len(docs) > 0 {
		ix.avgLen = float64(total) / float64(len(docs))
	}
	return ix
}

func (ix *index) idf(t string) float64 {
	n, df := float64(len(ix.ids)), float64(ix.df[t])
	return math.Log(1 + (n-df+0.5)/(df+0.5))
}

func (ix *index) score(query string) []float64 {
	scores := make([]float64, len(ix.ids))
	for _, t := range ix.analyzer.tokenize(query) {
		if ix.df[t] == 0 {
			continue
		}
		idf := ix.idf(t)
		for i, tf := range ix.tf {
			f := float64(tf[t])
			if f == 0 {
				continue
			}
			norm := 1 - b + b*float64(ix.length[i])/ix.avgLen
			scores[i] += idf * f * (k1 + 1) / (f + k1*norm)
		}
	}
	return scores
}

func (ix *index) coverage(query string) []float64 {
	terms := ix.analyzer.tokenize(query)
	slices.Sort(terms)
	terms = slices.Compact(terms)
	cov := make([]float64, len(ix.ids))
	var total float64
	for _, t := range terms {
		total += ix.idf(t)
	}
	if total == 0 {
		return cov
	}
	for i, tf := range ix.tf {
		for _, t := range terms {
			if tf[t] > 0 {
				cov[i] += ix.idf(t)
			}
		}
		cov[i] /= total
	}
	return cov
}

func (a analyzer) tokenize(s string) []string {
	s = strings.ToLower(s)
	words := strings.FieldsFunc(s, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
	terms := words[:0]
	for _, w := range words {
		if a.stopwords && stopwords[w] {
			continue
		}
		if a.stem {
			w = stem(w)
		}
		terms = append(terms, w)
	}
	return terms
}

func stem(w string) string {
	if len(w) <= 4 {
		return w
	}
	for _, suffix := range []string{"ing", "ed", "es", "s"} {
		if strings.HasSuffix(w, suffix) {
			return strings.TrimSuffix(w, suffix)
		}
	}
	return w
}

var stopwords = func() map[string]bool {
	m := map[string]bool{}
	for _, w := range strings.Fields(`a an the is are was be to of in on for and or do does i my me
		can what how much when where which will it its this that with by from at as if not no
		your you we our there their than then so`) {
		m[w] = true
	}
	return m
}()
