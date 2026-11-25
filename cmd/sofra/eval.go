package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"

	"sofra/internal/agent"
	"sofra/internal/clock"
	"sofra/internal/domain"
	"sofra/internal/ui"
)

const (
	evalQuestionsPath = "data/eval_questions.jsonl"
	evalDir           = "samples/eval"
	evalUser = domain.UserID("u_ok")
)

type evalQuestion struct {
	ID               string        `json:"id"`
	Q                string        `json:"q"`
	Type             string        `json:"type"`
	PrimaryDocIDs    []string      `json:"primary_doc_ids"`
	MustContain      []string      `json:"must_contain"`
	MustNotContain   []string      `json:"must_not_contain"`
	MustNotMatch     string        `json:"must_not_match"`
	ExpectedDecision []ui.Decision `json:"expected_audit_decision"`
	ExpectedBehavior string        `json:"expected_behavior"`
}

type evalResult struct {
	q        evalQuestion
	got      delivered
	changes  []string
	problems []string
	err      error
}

func runEval(ctx context.Context, args []string) error {
	flags := flag.NewFlagSet("eval", flag.ContinueOnError)
	only := flags.String("only", "", "comma-separated row ids to run, e.g. ev_05,ev_12")
	if err := flags.Parse(args); err != nil {
		return err
	}
	questions, err := loadEvalQuestions(*only)
	if err != nil {
		return err
	}
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(evalDir, 0o755); err != nil {
		return err
	}

	results := make([]evalResult, len(questions))
	var wg sync.WaitGroup
	for i, q := range questions {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i] = runEvalRow(ctx, cfg, q)
		}()
	}
	wg.Wait()

	failed := 0
	for _, r := range results {
		status := "PASS"
		if r.err != nil || len(r.problems) > 0 {
			status, failed = "FAIL", failed+1
		}
		fmt.Printf("%s %s decision=%s kb=%v\n", r.q.ID, status, r.got.Audit.Decision, r.got.Audit.KBDocIDs)
		if r.err != nil {
			fmt.Printf("   error: %v\n", r.err)
		}
		for _, p := range r.problems {
			fmt.Printf("   - %s\n", p)
		}
	}
	fmt.Printf("\n%d/%d passed\n", len(results)-failed, len(results))
	if failed > 0 {
		return fmt.Errorf("%d eval rows failed", failed)
	}
	return nil
}

func loadEvalQuestions(only string) ([]evalQuestion, error) {
	f, err := os.Open(evalQuestionsPath)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var ids []string
	if only != "" {
		for _, id := range strings.Split(only, ",") {
			ids = append(ids, strings.TrimSpace(id))
		}
	}
	var out []evalQuestion
	in := bufio.NewScanner(f)
	for in.Scan() {
		if strings.TrimSpace(in.Text()) == "" {
			continue
		}
		var q evalQuestion
		if err := json.Unmarshal(in.Bytes(), &q); err != nil {
			return nil, fmt.Errorf("%s: %w", evalQuestionsPath, err)
		}
		if ids == nil || slices.Contains(ids, q.ID) {
			out = append(out, q)
		}
	}
	if err := in.Err(); err != nil {
		return nil, err
	}
	for _, id := range ids {
		if !slices.ContainsFunc(out, func(q evalQuestion) bool { return q.ID == id }) {
			return nil, fmt.Errorf("-only: no eval row %q", id)
		}
	}
	return out, nil
}

func runEvalRow(ctx context.Context, cfg config, q evalQuestion) evalResult {
	r := evalResult{q: q}
	a, err := build(cfg, setup{clock: clock.NewFixed(cfg.now)})
	if err != nil {
		r.err = err
		return r
	}
	defer a.close()

	before, err := observe(a.store, evalUser)
	if err != nil {
		r.err = err
		return r
	}
	resp, err := a.agent.Handle(ctx, agent.NewSession(evalUser), q.Q)
	if err != nil {
		r.err = err
		return r
	}
	rendered, err := a.renderer.Render(resp)
	if err != nil {
		r.err = err
		return r
	}
	if err := os.WriteFile(filepath.Join(evalDir, q.ID+".json"), rendered, 0o644); err != nil {
		r.err = err
		return r
	}
	if err := json.Unmarshal(rendered, &r.got); err != nil {
		r.err = err
		return r
	}
	after, err := observe(a.store, evalUser)
	if err != nil {
		r.err = err
		return r
	}
	r.changes = before.changes(after)
	r.problems = q.check(r.got, r.changes)
	return r
}

func (q evalQuestion) check(d delivered, changes []string) []string {
	var problems []string
	if !slices.Contains(q.ExpectedDecision, d.Audit.Decision) {
		problems = append(problems, fmt.Sprintf("decision %q, want one of %v", d.Audit.Decision, q.ExpectedDecision))
	}

	text := strings.ToLower(d.text())
	for _, s := range q.MustContain {
		if !strings.Contains(text, strings.ToLower(s)) {
			problems = append(problems, fmt.Sprintf("text does not contain %q", s))
		}
	}
	for _, s := range q.MustNotContain {
		if strings.Contains(text, strings.ToLower(s)) {
			problems = append(problems, fmt.Sprintf("text contains %q", s))
		}
	}
	if q.MustNotMatch != "" {
		re, err := regexp.Compile("(?i)" + q.MustNotMatch)
		if err != nil {
			problems = append(problems, fmt.Sprintf("must_not_match: %v", err))
		} else if m := re.FindString(text); m != "" {
			problems = append(problems, fmt.Sprintf("text matches %q: %q", q.MustNotMatch, m))
		}
	}

	if len(q.PrimaryDocIDs) > 0 {
		if ids := d.Audit.KBDocIDs; len(ids) == 0 || !slices.Contains(q.PrimaryDocIDs, ids[0]) {
			problems = append(problems, fmt.Sprintf("kb_doc_ids %v, want one of %v first", d.Audit.KBDocIDs, q.PrimaryDocIDs))
		}
	}

	types := d.blockTypes()
	if slices.Contains(types, "order_summary") {
		problems = append(problems, "order_summary present: an action was presented as done")
	}
	if len(changes) > 0 {
		problems = append(problems, "the mock tool layer changed: "+strings.Join(changes, ", "))
	}
	if q.ExpectedBehavior == "confirmation_required" && !slices.Contains(types, "confirmation_prompt") {
		problems = append(problems, "no confirmation_prompt")
	}
	return problems
}

func (d delivered) text() string {
	var parts []string
	for _, b := range d.Blocks {
		if b.Type == "text" {
			parts = append(parts, b.Markdown)
		}
	}
	return strings.Join(parts, "\n")
}
