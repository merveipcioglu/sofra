package ui

import (
	"cmp"
	"fmt"
	"slices"
	"time"

	"sofra/internal/action"
	"sofra/internal/confirm"
	"sofra/internal/domain"
	"sofra/internal/gate"
	"sofra/internal/knowledge"
	"sofra/internal/pricing"
)

func tlPtr(m domain.Money) *float64 { v := m.TL(); return &v }

func NewText(markdown string) Text { return Text{Type: "text", Markdown: markdown} }

func NewSuggestedActions(chips ...string) SuggestedActions {
	return SuggestedActions{Type: "suggested_actions", Chips: chips}
}

func NewRestaurantCard(r domain.Restaurant) RestaurantCard {
	return RestaurantCard{
		Type: "restaurant_card", RestaurantID: string(r.ID), Name: r.Name, Cuisine: r.Cuisine,
		Rating: r.Rating, DeliveryFeeTRY: r.DeliveryFee.TL(), MinOrderTRY: r.MinOrder.TL(),
		ETAMin: r.ETAMinutes, District: r.District,
	}
}

func NewMenuItem(m domain.MenuItem) MenuItem {
	return MenuItem{
		Type: "menu_item", ItemID: string(m.ID), Name: m.Name, PriceTRY: m.Price.TL(),
		Available: m.Available, AgeRestricted: m.AgeRestricted, Category: m.Category,
	}
}

func NewCartSummary(restaurantID domain.RestaurantID, restaurantName string, q pricing.Quote) CartSummary {
	lines := make([]CartLine, 0, len(q.Lines))
	for _, l := range q.Lines {
		lines = append(lines, CartLine{Name: l.Name, Qty: l.Qty, PriceTRY: l.UnitPrice.TL()})
	}
	return CartSummary{
		Type: "cart_summary", RestaurantID: string(restaurantID), RestaurantName: restaurantName, Items: lines,
		SubtotalTRY: q.Subtotal.TL(), DeliveryFeeTRY: q.DeliveryFee.TL(), TotalTRY: q.Total.TL(),
		MinOrderTRY: q.MinOrder.TL(), MeetsMinimum: q.MeetsMinimum,
	}
}

func NewOrderHistory(orders []domain.Order) OrderHistory {
	sorted := slices.Clone(orders)
	slices.SortFunc(sorted, func(a, b domain.Order) int {
		return cmp.Or(b.Date.Compare(a.Date), cmp.Compare(a.ID, b.ID))
	})
	entries := make([]HistoryEntry, 0, len(sorted))
	for _, o := range sorted {
		entries = append(entries, HistoryEntry{
			OrderID: string(o.ID), Date: domain.FormatDate(o.Date), Restaurant: o.RestaurantName,
			TotalTRY: o.Total.TL(), Status: string(o.Status),
		})
	}
	return OrderHistory{Type: "order_history", Orders: entries}
}

func NewCitation(d knowledge.Document) Citation {
	return Citation{Type: "citation", DocID: d.ID, Title: d.Title, Category: d.Category, Date: d.Date}
}

func GroundKnowledgeAnswer(proposed Decision, cited []string, returned []knowledge.Result) ([]string, Decision) {
	var sources []string
	for _, r := range returned {
		if slices.Contains(cited, r.ID) && !slices.Contains(sources, r.ID) {
			sources = append(sources, r.ID)
		}
	}
	if len(sources) == 0 || proposed == Unknown {
		return nil, Unknown
	}
	return sources, Answered
}

func FromOutcome(o action.Outcome, params map[string]any) ([]Block, Audit) {
	audit := Audit{TokenRejection: string(o.Rejection)}
	switch o.Status {
	case action.Blocked:
		audit.Decision = Blocked
		return []Block{newVerificationGate(o)}, audit
	case action.Invalid:
		audit.Decision = Refused
		return []Block{Error{Type: "error", Code: string(o.Violation.Requirement), Message: o.Violation.Detail}}, audit
	case action.NeedsConfirmation:
		audit.Decision = NeedsConfirmation
		var blocks []Block
		if o.Quote != nil && o.Action == domain.PlaceOrder {
			blocks = append(blocks, NewCartSummary("", o.Restaurant, *o.Quote))
		}
		return append(blocks, newConfirmationPrompt(o, params)), audit
	case action.Executed:
		audit.Decision = Answered
		r := o.Receipt
		sum := OrderSummary{
			Type: "order_summary", OrderID: string(r.OrderID), Restaurant: r.RestaurantName,
			Status: string(r.OrderStatus), Event: string(r.Event), Refund: newRefund(r.Refund),
		}
		if r.Event == action.EventTipAdded {
			sum.TipTRY = tlPtr(r.Amount)
		} else {
			sum.TotalTRY = tlPtr(r.Amount)
		}
		return []Block{sum}, audit
	case action.Rejected:
		audit.Decision = Refused
		return []Block{Error{Type: "error", Code: string(o.Rejection), Message: rejectionMessage(o)}}, audit
	default:
		panic(fmt.Sprintf("ui: outcome status %q has no rendering", o.Status))
	}
}

func rejectionMessage(o action.Outcome) string {
	switch o.Rejection {
	case confirm.AlreadyUsed:
		return fmt.Sprintf("This confirmation was already used (%s); nothing more was done.", o.Used)
	case confirm.Superseded:
		return "A newer confirmation replaced this one, so nothing was done. Use the latest one."
	case confirm.SameTurn:
		return "Confirm in a separate step; nothing was done."
	default:
		return "This confirmation is not valid, so nothing was done."
	}
}

func newConfirmationPrompt(o action.Outcome, params map[string]any) ConfirmationPrompt {
	p := ConfirmationPrompt{
		Type: "confirmation_prompt", Action: string(o.Action), Summary: confirmationSummary(o), Params: params,
		ConfirmToken: o.Token.Value, ExpiresAt: o.Token.ExpiresAt.Format(time.RFC3339), Assumptions: AssumptionTexts(o),
	}
	if o.Action.Spends() {
		p.Payment = &Payment{FromWalletTRY: o.Payment.FromWallet.TL(), FromCardTRY: o.Payment.FromCard.TL()}
	}
	return p
}

func paymentText(p pricing.PaymentPlan) string {
	switch {
	case p.FromCard == 0:
		return fmt.Sprintf("Paid from your wallet (%v).", p.FromWallet)
	case p.FromWallet == 0:
		return fmt.Sprintf("Charged to your saved card (%v).", p.FromCard)
	default:
		return fmt.Sprintf("%v from your wallet and %v charged to your saved card.", p.FromWallet, p.FromCard)
	}
}

func confirmationSummary(o action.Outcome) string {
	var s string
	switch o.Action {
	case domain.PlaceOrder:
		s = fmt.Sprintf("Place an order at %s for %v.", o.Restaurant, o.Quote.Total)
	case domain.CancelOrder:
		s = fmt.Sprintf("Cancel your %s order.", o.Restaurant)
	case domain.AddTip:
		s = fmt.Sprintf("Leave a %v tip on your %s order.", o.Payment.FromWallet+o.Payment.FromCard, o.Restaurant)
	}
	if o.Action.Spends() {
		s += " " + paymentText(o.Payment)
	}
	switch {
	case o.Rejection == confirm.StateChanged:
		s = fmt.Sprintf("The amounts changed since the previous confirmation, so nothing was done. %s Confirm again to proceed.", s)
	case o.Rejection != "":
		s = fmt.Sprintf("The previous confirmation could not be used (%s), so nothing was done. %s Confirm again to proceed.",
			o.Rejection, s)
	}
	return s
}

func newVerificationGate(o action.Outcome) VerificationGate {
	v := o.Violation
	return VerificationGate{
		Type: "verification_gate", Requirement: string(v.Requirement), Reason: v.Detail,
		CTA: callsToAction[v.Requirement],
	}
}

var callsToAction = map[gate.Requirement]string{
	gate.OutOfServiceArea: "Choose a restaurant that delivers to your district.",
	gate.ItemUnavailable:  "Choose another item from the menu.",
	gate.Age18Plus:        "Complete age verification under Profile > Age Verification.",
	gate.MinOrder:         "Add items to reach the minimum order value.",
	gate.SufficientFunds:  "Top up your wallet or add a saved card.",
	gate.NotCancellable:   "If something is wrong with the order, report an issue from the order details.",
	gate.TipWindowExpired: "Tips can no longer be left on this order.",
}

func newRefund(r *action.Refund) *Refund {
	if r == nil {
		return nil
	}
	return &Refund{ToWalletTRY: r.ToWallet.TL(), ToCardTRY: r.ToCard.TL(), OrderByPaymentsSystem: r.OrderUnrecorded}
}
