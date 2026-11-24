package ui

import (
	"encoding/json"
	"math"
	"testing"
	"time"

	"sofra/internal/action"
	"sofra/internal/confirm"
	"sofra/internal/domain"
	"sofra/internal/gate"
	"sofra/internal/knowledge"
	"sofra/internal/pricing"
)

func renderer(t *testing.T) *Renderer {
	t.Helper()
	rd, err := NewRenderer("../../schema/ui_spec.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	return rd
}

func TestEveryBlockRendersValid(t *testing.T) {
	quote := pricing.Quote{
		Lines:    []pricing.QuotedLine{{ItemID: "itm_013", Name: "Cheeseburger", Qty: 2, UnitPrice: tl(195)}},
		Subtotal: tl(390), Total: tl(390), MinOrder: tl(100), MeetsMinimum: true, FreeDeliveryThreshold: tl(250),
	}
	order := domain.Order{ID: "u_ok_o9", Date: time.Date(2026, 8, 19, 0, 0, 0, 0, domain.Istanbul),
		RestaurantName: "Burger Stop", Total: tl(260), Status: domain.OrderReceived}
	prompt := action.Outcome{
		Action: domain.PlaceOrder, Status: action.NeedsConfirmation, Restaurant: "Burger Stop",
		Rejection: confirm.ParamsMismatch,
		Token:     &confirm.Token{Value: "tok", ExpiresAt: time.Date(2026, 8, 20, 12, 5, 0, 0, domain.Istanbul)},
		Quote:     &quote, Payment: pricing.PaymentPlan{FromWallet: tl(100), FromCard: tl(290)},
		Cart: &action.CartAssumption{Kind: action.CartIncluded, CartRestaurant: "Burger Stop",
			Lines: []action.AssumedLine{{Name: "Patates Kızartması", InCart: 1, Requested: 1}}},
	}
	minOrder := action.Outcome{Action: domain.PlaceOrder, Status: action.Blocked, Quote: &quote,
		Violation: &gate.Violation{Requirement: gate.MinOrder, Detail: "below"}}
	funds := action.Outcome{Action: domain.AddTip, Status: action.Blocked, Quote: &quote,
		Violation: &gate.Violation{Requirement: gate.SufficientFunds, Detail: "short"}}
	invalid := action.Outcome{Action: domain.PlaceOrder, Status: action.Invalid,
		Violation: &gate.Violation{Requirement: gate.InvalidRequest, Detail: "no items"}}
	cancelled := action.Outcome{Action: domain.CancelOrder, Status: action.Executed, Receipt: &action.Receipt{
		Event: action.EventCancelled, OrderID: "u_ok_o9", RestaurantName: "Burger Stop", Amount: tl(260),
		OrderStatus: domain.OrderCancelled, Refund: &action.Refund{ToWallet: tl(20), ToCard: tl(30), OrderUnrecorded: true}}}
	tipped := action.Outcome{Action: domain.AddTip, Status: action.Executed, Receipt: &action.Receipt{
		Event: action.EventTipAdded, OrderID: "u_ok_o9", RestaurantName: "Burger Stop", Amount: tl(50),
		OrderStatus: domain.OrderReceived}}

	blocks := []Block{
		NewText("hello"),
		NewRestaurantCard(domain.Restaurant{ID: "rst_04", Name: "Burger Stop", Cuisine: "Burger", Rating: 4.1,
			DeliveryFee: tl(30), MinOrder: tl(100), ETAMinutes: 30, District: "Kadıköy"}),
		NewMenuItem(domain.MenuItem{ID: "itm_013", Name: "Cheeseburger", Price: tl(195), Category: "main", Available: true}),
		NewCartSummary("rst_04", quote),
		NewOrderHistory([]domain.Order{order}),
		NewCitation(knowledge.Document{ID: "pol_tip", Title: "Tips", Category: "policy", Date: "2026-05-05"}),
		NewSuggestedActions("Show my cart"),
		Error{Type: "error", Code: "c", Message: "m"},
	}
	rd := renderer(t)
	outcomes := []action.Outcome{prompt, minOrder, funds, invalid, cancelled, tipped}
	for _, r := range []confirm.Reason{confirm.Malformed, confirm.Unknown, confirm.BadSignature, confirm.WrongUser,
		confirm.AlreadyUsed, confirm.Superseded, confirm.Expired, confirm.SameTurn, confirm.ParamsMismatch, confirm.StateChanged} {
		renewed := prompt
		renewed.Rejection = r
		outcomes = append(outcomes, renewed,
			action.Outcome{Action: domain.PlaceOrder, Status: action.Rejected, Rejection: r, Used: "ord_x"})
	}
	for _, o := range outcomes {
		b, oa := FromOutcome(o, map[string]any{"restaurant_id": "rst_04"})
		blocks = append(blocks, b...)
		if _, err := rd.Render(Response{Version: "1", Blocks: b, Audit: oa}); err != nil {
			t.Errorf("%s %s: %v", o.Status, o.Rejection, err)
		}
	}
	_, audit := FromOutcome(prompt, nil)
	audit.UserID, audit.Intent, audit.ToolsCalled, audit.Reason = "u_ok", "place_order", []string{"place_order"}, "r"
	audit.KBDocIDs = []string{"pol_tip"}

	for _, b := range blocks {
		if _, err := rd.Render(Response{Version: "1", Blocks: []Block{b}, Audit: Audit{Decision: Answered}}); err != nil {
			t.Errorf("%T: %v", b, err)
		}
	}
	if _, err := rd.Render(Response{Version: "1", Blocks: []Block{NewText("x")}, Audit: audit}); err != nil {
		t.Errorf("full audit: %v", err)
	}
}

func TestFallbackValidAndSchemaStrict(t *testing.T) {
	rd := renderer(t)
	if err := rd.validate(fallback); err != nil {
		t.Errorf("fallback: %v", err)
	}
	doc, err := json.Marshal(map[string]any{
		"version": "1", "audit": map[string]any{"decision": "answered"},
		"blocks": []any{map[string]any{"type": "text", "markdown": "x", "extra": 1}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if rd.validate(doc) == nil {
		t.Error("a block with an unknown field passed the schema")
	}
}

func tl(lira float64) domain.Money { return domain.Money(math.Round(lira * 100)) }
