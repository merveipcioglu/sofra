package agent

import (
	"encoding/json"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/anthropics/anthropic-sdk-go/v2"

	"sofra/internal/action"
	"sofra/internal/clock"
	"sofra/internal/confirm"
	"sofra/internal/domain"
	"sofra/internal/gate"
	"sofra/internal/knowledge"
	"sofra/internal/store"
	"sofra/internal/tools"
	"sofra/internal/ui"
)

func newAgent(t *testing.T) (*Agent, *store.Store) {
	t.Helper()
	st, err := store.Load("../../data")
	if err != nil {
		t.Fatal(err)
	}
	kb, err := knowledge.Load("../../data/knowledge/kb.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	now, err := clock.ParseReference("")
	if err != nil {
		t.Fatal(err)
	}
	clk := clock.NewFixed(now)
	secret, err := confirm.RandomSecret()
	if err != nil {
		t.Fatal(err)
	}
	tokens, err := confirm.NewService(secret, clk)
	if err != nil {
		t.Fatal(err)
	}
	return &Agent{exec: tools.NewExecutor(st, kb, action.NewService(st, tokens, clk))}, st
}

func toolUses(t *testing.T, calls ...string) *anthropic.BetaMessage {
	t.Helper()
	var content []string
	for i := 0; i < len(calls); i += 2 {
		content = append(content, fmt.Sprintf(`{"type":"tool_use","id":"tu_%d","name":%q,"input":%s}`, i/2, calls[i], calls[i+1]))
	}
	var m anthropic.BetaMessage
	raw := `{"id":"msg","type":"message","role":"assistant","model":"m","stop_reason":"tool_use","content":[` + strings.Join(content, ",") + `]}`
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		t.Fatal(err)
	}
	return &m
}

func newTurn(s *Session) *turnState {
	s.turn++
	return &turnState{reads: map[string]string{}}
}

func TestOneActionPerTurn(t *testing.T) {
	a, st := newAgent(t)
	s := NewSession("u_ok")

	prepare := newTurn(s)
	a.runTools(s, prepare, toolUses(t, "cancel_order", `{"order_id":"u_ok_o9","confirm":false}`))
	tok := prepare.calls[0].res.Outcome.Token
	if tok == nil {
		t.Fatalf("no confirmation: %s", prepare.calls[0].res.Content)
	}

	turn := newTurn(s)
	results := a.runTools(s, turn, toolUses(t,
		"cancel_order", `{"order_id":"u_ok_o9","confirm":true}`,
		"add_tip", `{"order_id":"u_ok_o9","amount_try":20,"confirm":false}`))

	if len(results) != 2 || !turn.calls[1].res.IsError || !strings.Contains(turn.calls[1].res.Content, "one action per turn") {
		t.Fatalf("second action not refused: %+v", turn.calls[1].res)
	}
	if turn.calls[1].res.Outcome != nil {
		t.Error("the refused action ran")
	}
	if o, err := st.Order("u_ok", "u_ok_o9"); err != nil || o.Status != domain.OrderCancelled {
		t.Fatalf("cancellation: %v, %+v", err, o)
	}

	r := compose(s, turn, finalReply{Markdown: "Cancelled.", Decision: string(ui.Answered)})
	var receipt bool
	for _, b := range r.Blocks {
		if sum, ok := b.(ui.OrderSummary); ok && sum.Event == "cancelled" && sum.Refund != nil {
			receipt = true
		}
	}
	if !receipt {
		t.Errorf("the cancellation's receipt is not shown: %+v", r.Blocks)
	}
	if r.Audit.Decision != ui.Answered || !strings.Contains(r.Audit.Reason, "add_tip") {
		t.Errorf("audit %+v, want the executed cancellation and the refused tip", r.Audit)
	}
}

func TestInvalidRequestCanBeCorrected(t *testing.T) {
	a, _ := newAgent(t)
	s := NewSession("u_ok")
	turn := newTurn(s)
	a.runTools(s, turn, toolUses(t,
		"place_order", `{"restaurant_id":"rst_nope","items":[{"item_id":"itm_013","qty":2}],"cart_mode":null,"confirm":false}`,
		"place_order", `{"restaurant_id":"rst_04","items":[{"item_id":"itm_013","qty":2}],"cart_mode":null,"confirm":false}`))

	if o := turn.calls[0].res.Outcome; o == nil || o.Status != action.Invalid {
		t.Fatalf("first call: %+v, want an invalid request", turn.calls[0].res)
	}
	if o := turn.calls[1].res.Outcome; o == nil || o.Status != action.NeedsConfirmation {
		t.Fatalf("corrected call: %+v, want a confirmation", turn.calls[1].res)
	}
	if r := compose(s, turn, finalReply{Decision: string(ui.Answered)}); r.Audit.Decision != ui.NeedsConfirmation {
		t.Errorf("decision %s, want needs_confirmation", r.Audit.Decision)
	}
}

func TestRepeatedReadIsNotRun(t *testing.T) {
	a, _ := newAgent(t)
	s := NewSession("u_ok")
	turn := newTurn(s)
	a.runTools(s, turn, toolUses(t,
		"get_menu", `{"restaurant_id":"rst_04"}`,
		"get_menu", `{ "restaurant_id" : "rst_04" }`))
	if !strings.Contains(turn.calls[1].res.Content, "already made this exact call") {
		t.Errorf("second call ran again: %.80s", turn.calls[1].res.Content)
	}
}

func TestDecision(t *testing.T) {
	blocked := action.Outcome{Action: domain.AddTip, Status: action.Blocked,
		Violation: &gate.Violation{Requirement: gate.TipWindowExpired}}
	cases := []struct {
		name     string
		called   []string
		res      tools.Result
		proposed ui.Decision
		want     ui.Decision
	}{
		{"answered with no tool behind it", nil, tools.Result{}, ui.Answered, ui.Unknown},
		{"answered from a lookup", []string{"get_cart"}, tools.Result{Content: "{}"}, ui.Answered, ui.Answered},
		{"clarify is the model's to make", nil, tools.Result{}, ui.Clarify, ui.Clarify},
		{"a gate overrides the model", []string{"add_tip"}, tools.Result{Outcome: &blocked}, ui.Answered, ui.Blocked},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			st := &turnState{called: c.called}
			if len(c.called) > 0 {
				st.calls = []call{{id: "tu_0", res: c.res}}
			}
			r := compose(NewSession("u_ok"), st, finalReply{Markdown: "x", Decision: string(c.proposed)})
			if r.Audit.Decision != c.want {
				t.Errorf("decision %s, want %s", r.Audit.Decision, c.want)
			}
		})
	}
}

func TestPromptMatchesCode(t *testing.T) {
	props := map[string]map[string]any{finalTool: finalProperties}
	for _, d := range tools.Definitions() {
		props[d.Name] = d.Properties
	}
	params := []struct{ tool, param string }{
		{"place_order", "confirm"}, {"cancel_order", "confirm"}, {"add_tip", "confirm"},
		{"place_order", "cart_mode"},
		{"search_knowledge", "query"}, {"get_menu", "restaurant_id"}, {"search_restaurants", "q"},
		{finalTool, "cited_doc_ids"}, {finalTool, "suggested_actions"}, {finalTool, "decision"},
	}
	for _, p := range params {
		if !strings.Contains(systemPrompt, p.tool) {
			t.Errorf("the prompt no longer names %s; update this test", p.tool)
		}
		tool, ok := props[p.tool]
		if !ok {
			t.Errorf("the prompt names tool %s, which does not exist", p.tool)
			continue
		}
		if _, ok := tool[p.param]; !ok {
			t.Errorf("tool %s has no parameter %s", p.tool, p.param)
		}
	}
	for _, v := range []string{"request_only", "answered", "clarify", "refused", "unknown"} {
		if !strings.Contains(systemPrompt, `"`+v+`"`) {
			t.Errorf("the prompt no longer names %q; update this test", v)
		}
		if !strings.Contains(fmt.Sprint(props["place_order"]["cart_mode"], finalProperties["decision"]), v) {
			t.Errorf("the prompt names value %q, which no schema has", v)
		}
	}

	a, _ := newAgent(t)
	s := NewSession("u_ok")
	prepare := newTurn(s)
	a.runTools(s, prepare, toolUses(t,
		"place_order", `{"restaurant_id":"rst_04","items":[{"item_id":"itm_013","qty":2}],"cart_mode":null,"confirm":false}`,
		tools.SearchKnowledge, `{"query":"delivery fee"}`))
	execute := newTurn(s)
	a.runTools(s, execute, toolUses(t, "place_order",
		`{"restaurant_id":"rst_04","items":[{"item_id":"itm_013","qty":2}],"cart_mode":null,"confirm":true}`))
	for _, c := range []struct{ name, in, result string }{
		{"confirm_with_params", "confirm_with_params", prepare.calls[0].res.Content},
		{"<untrusted_document>", "<untrusted_document>", prepare.calls[1].res.Content},
		{"marked outdated", `"status":"outdated`, prepare.calls[1].res.Content},
		{`status "executed"`, `"status":"executed"`, execute.calls[0].res.Content},
		{"[screen]", screenNote, screenNote},
	} {
		if !strings.Contains(systemPrompt, c.name) {
			t.Errorf("the prompt no longer names %s; update this test", c.name)
		}
		if !strings.Contains(c.result, c.in) {
			t.Errorf("the prompt names %s, which the tool result lacks: %.120s", c.name, c.result)
		}
	}
}

func TestConfirmOnScreen(t *testing.T) {
	a, st := newAgent(t)
	rd, err := ui.NewRenderer("../../schema/ui_spec.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	s := NewSession("u_ok")
	prepare := newTurn(s)
	a.runTools(s, prepare, toolUses(t, "cancel_order", `{"order_id":"u_ok_o9","confirm":false}`))
	tok := prepare.calls[0].res.Outcome.Token
	if tok == nil {
		t.Fatalf("no confirmation: %s", prepare.calls[0].res.Content)
	}

	cases := []struct {
		name, token string
		decision    ui.Decision
		block       string
		rejection   string
	}{
		{"the confirmation's token", tok.Value, ui.Answered, "order_summary", ""},
		{"a fabricated token (prompt 15)", "abc123", ui.Refused, "error", "malformed"},
	}
	for _, c := range cases {
		resp, err := a.ConfirmOnScreen(s, c.token)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if _, err := rd.Render(resp); err != nil {
			t.Errorf("%s: response not valid against the schema: %v", c.name, err)
		}
		if resp.Audit.Decision != c.decision || resp.Audit.TokenRejection != c.rejection || resp.Audit.Intent != "confirm_on_screen" {
			t.Errorf("%s: audit %+v, want %s %q", c.name, resp.Audit, c.decision, c.rejection)
		}
		if got := blockType(resp.Blocks[0]); got != c.block {
			t.Errorf("%s: block %s, want %s", c.name, got, c.block)
		}
		last := s.messages[len(s.messages)-1].OfUser.Content
		note := *last[len(last)-1].GetText()
		if !strings.HasPrefix(note, screenNote) || strings.Contains(note, tok.Value) {
			t.Errorf("%s: history note %q: want a %s note without the token", c.name, note, screenNote)
		}
	}
	if o, _ := st.Order("u_ok", "u_ok_o9"); o.Status != domain.OrderCancelled {
		t.Errorf("order status %s, want cancelled by the screen confirmation", o.Status)
	}
}

func blockType(b ui.Block) string {
	raw, _ := json.Marshal(b)
	var v struct{ Type string }
	_ = json.Unmarshal(raw, &v)
	return v.Type
}

func TestFinalReplyMatchesSchema(t *testing.T) {
	var fields []string
	rt := reflect.TypeOf(finalReply{})
	for i := range rt.NumField() {
		fields = append(fields, rt.Field(i).Tag.Get("json"))
	}
	slices.Sort(fields)
	if want := slices.Sorted(maps.Keys(finalProperties)); !slices.Equal(fields, want) {
		t.Errorf("finalReply fields %v, schema properties %v", fields, want)
	}
}

func TestSuggestionsCapped(t *testing.T) {
	st := &turnState{called: []string{"get_cart"}, calls: []call{{id: "tu_0", res: tools.Result{Content: "{}"}}}}
	r := compose(NewSession("u_ok"), st, finalReply{Markdown: "x", Decision: string(ui.Answered),
		SuggestedActions: []string{"a", "b", "c", "d", "e"}})
	for _, b := range r.Blocks {
		if chips, ok := b.(ui.SuggestedActions); ok && len(chips.Chips) != maxSuggestions {
			t.Errorf("%d suggestions shown, want %d", len(chips.Chips), maxSuggestions)
		}
	}
}

func TestNoLookupCardsForAnUnknownAnswer(t *testing.T) {
	cases := []struct {
		name     string
		calls    []string
		proposed ui.Decision
		want     ui.Decision
		cards    bool
	}{
		{"calories: no document", []string{"get_menu", `{"restaurant_id":"rst_01"}`,
			tools.SearchKnowledge, `{"query":"calorie kofte"}`}, ui.Unknown, ui.Unknown, false},
		{"the menu asked for", []string{"get_menu", `{"restaurant_id":"rst_01"}`}, ui.Answered, ui.Answered, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a, _ := newAgent(t)
			s := NewSession("u_ok")
			st := newTurn(s)
			a.runTools(s, st, toolUses(t, c.calls...))
			r := compose(s, st, finalReply{Markdown: "x", Decision: string(c.proposed)})
			var cards int
			for _, b := range r.Blocks {
				if _, ok := b.(ui.MenuItem); ok {
					cards++
				}
			}
			if r.Audit.Decision != c.want || (cards > 0) != c.cards {
				t.Errorf("decision %s with %d menu cards; want %s, cards %t", r.Audit.Decision, cards, c.want, c.cards)
			}
		})
	}
}

func TestPaymentShownForSpendingActions(t *testing.T) {
	cases := []struct {
		tool, input string
		spends      bool
	}{
		{"place_order", `{"restaurant_id":"rst_04","items":[{"item_id":"itm_013","qty":2}],"cart_mode":null,"confirm":false}`, true},
		{"add_tip", `{"order_id":"u_ok_o9","amount_try":20,"confirm":false}`, true},
		{"cancel_order", `{"order_id":"u_ok_o9","confirm":false}`, false},
	}
	for _, c := range cases {
		t.Run(c.tool, func(t *testing.T) {
			a, _ := newAgent(t)
			s := NewSession("u_ok")
			turn := newTurn(s)
			a.runTools(s, turn, toolUses(t, c.tool, c.input))
			res := turn.calls[0].res
			if res.Outcome == nil || res.Outcome.Status != action.NeedsConfirmation {
				t.Fatalf("no confirmation: %s", res.Content)
			}
			if got := strings.Contains(res.Content, `"payment"`); got != c.spends {
				t.Errorf("tool result states the payment: %t, want %t", got, c.spends)
			}
			r := compose(s, turn, finalReply{Decision: string(ui.Answered)})
			for _, b := range r.Blocks {
				if p, ok := b.(ui.ConfirmationPrompt); ok && (p.Payment != nil) != c.spends {
					t.Errorf("confirmation shows the payment: %t, want %t", p.Payment != nil, c.spends)
				}
			}
		})
	}
}

func TestModelAmountsChecked(t *testing.T) {
	a, _ := newAgent(t)
	s := NewSession("u_ok")
	turn := newTurn(s)
	a.runTools(s, turn, toolUses(t,
		"add_tip", `{"order_id":"u_ok_o9","amount_try":20.005,"confirm":false}`,
		"quote_checkout", `{"restaurant_id":"rst_04","items":[{"item_id":"itm_013","qty":2246718829490265904}]}`))
	for i, want := range []string{"finer than a kuruş", "invalid quantity"} {
		if res := turn.calls[i].res; !res.IsError || res.Outcome != nil || !strings.Contains(res.Content, want) {
			t.Errorf("%s: %+v, want an error saying %q", turn.called[i], res, want)
		}
	}
}

func TestDistrictAsTyped(t *testing.T) {
	a, _ := newAgent(t)
	s := NewSession("u_new")
	search := func(district string) tools.Result {
		turn := newTurn(s)
		a.runTools(s, turn, toolUses(t, "search_restaurants", fmt.Sprintf(`{"q":null,"cuisine":null,"near_district":%q}`, district)))
		return turn.calls[0].res
	}
	for _, d := range []string{"Kadıköy", "kadıköy", "Kadikoy", "KADIKÖY", " kadikoy "} {
		if n := strings.Count(search(d).Content, `"delivers_to_district":true`); n != 6 {
			t.Errorf("%q: %d restaurants deliver, want 6", d, n)
		}
	}
	res := search("Moda")
	if !res.IsError || !strings.Contains(res.Content, "unknown district") || !strings.Contains(res.Content, "Kadıköy") {
		t.Errorf("unknown district: %s, want an error listing the known districts", res.Content)
	}
}

func TestSearchFollowsReadme(t *testing.T) {
	a, _ := newAgent(t)
	search := func(user domain.UserID, input string) tools.Result {
		s := NewSession(user)
		turn := newTurn(s)
		a.runTools(s, turn, toolUses(t, "search_restaurants", input))
		return turn.calls[0].res
	}
	cards := func(res tools.Result) []string {
		var ids []string
		for _, b := range res.Blocks {
			if c, ok := b.(ui.RestaurantCard); ok {
				ids = append(ids, c.RestaurantID)
			}
		}
		return ids
	}

	named := search("u_new", `{"q":"Ege Balık","cuisine":null,"near_district":null}`)
	if !strings.Contains(named.Content, `"name":"Ege Balık"`) || !strings.Contains(named.Content, `"delivers_to_district":false`) || len(cards(named)) != 0 {
		t.Errorf("named restaurant: %s, cards %v; want it found, not delivering, no card", named.Content, cards(named))
	}

	filtered := search("u_new", `{"q":"Ege Balık","cuisine":null,"near_district":"Üsküdar"}`)
	if strings.Contains(filtered.Content, "Ege Balık") {
		t.Errorf("near_district given: %s; want only restaurants that deliver there", filtered.Content)
	}

	if got := cards(search("u_ok", `{"q":"pizza","cuisine":null,"near_district":null}`)); !slices.Equal(got, []string{"rst_11", "rst_02"}) {
		t.Errorf("pizza near me: cards %v, want [rst_11 rst_02]", got)
	}
}
