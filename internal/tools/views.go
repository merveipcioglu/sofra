package tools

import (
	"time"

	"sofra/internal/action"
	"sofra/internal/confirm"
	"sofra/internal/domain"
	"sofra/internal/gate"
	"sofra/internal/knowledge"
	"sofra/internal/pricing"
	"sofra/internal/ui"
)

type errorView struct {
	Error string `json:"error"`
}

type userView struct {
	WalletBalanceTRY float64 `json:"wallet_balance_try"`
	PaymentMethod    bool    `json:"payment_method"`
	AgeVerified      bool    `json:"age_verified"`
	Address          string  `json:"address"`
	District         string  `json:"district"`
}

func newUserView(u domain.User) userView {
	return userView{
		WalletBalanceTRY: u.WalletBalance.TL(), PaymentMethod: u.HasSavedCard,
		AgeVerified: u.AgeVerified, Address: u.Address, District: u.District,
	}
}

type emptyCartView struct {
	RestaurantID   *domain.RestaurantID `json:"restaurant_id"`
	RestaurantName *string              `json:"restaurant_name"`
	Items          []quoteLineView      `json:"items"`
}

type restaurantsView struct {
	District    string           `json:"district"`
	Restaurants []restaurantView `json:"restaurants"`
}

type restaurantView struct {
	ID                 domain.RestaurantID `json:"id"`
	Name               string              `json:"name"`
	Cuisine            string              `json:"cuisine"`
	Rating             float64             `json:"rating"`
	District           string              `json:"district"`
	DeliveryFeeTRY     float64             `json:"delivery_fee_try"`
	MinOrderTRY        float64             `json:"min_order_try"`
	ETAMin             int                 `json:"eta_min"`
	DeliversToDistrict bool                `json:"delivers_to_district"`
}

func newRestaurantView(r domain.Restaurant, delivers bool) restaurantView {
	return restaurantView{
		ID: r.ID, Name: r.Name, Cuisine: r.Cuisine, Rating: r.Rating, District: r.District,
		DeliveryFeeTRY: r.DeliveryFee.TL(), MinOrderTRY: r.MinOrder.TL(), ETAMin: r.ETAMinutes,
		DeliversToDistrict: delivers,
	}
}

type menuView struct {
	RestaurantID domain.RestaurantID `json:"restaurant_id"`
	Name         string              `json:"name"`
	Items        []menuItemView      `json:"items"`
}

type menuItemView struct {
	ItemID        domain.ItemID `json:"item_id"`
	Name          string        `json:"name"`
	PriceTRY      float64       `json:"price_try"`
	Category      string        `json:"category"`
	Available     bool          `json:"available"`
	AgeRestricted bool          `json:"age_restricted"`
}

func newMenuItemView(m domain.MenuItem) menuItemView {
	return menuItemView{
		ItemID: m.ID, Name: m.Name, PriceTRY: m.Price.TL(), Category: m.Category,
		Available: m.Available, AgeRestricted: m.AgeRestricted,
	}
}

type quoteView struct {
	RestaurantID             domain.RestaurantID `json:"restaurant_id"`
	RestaurantName           string              `json:"restaurant_name"`
	Items                    []quoteLineView     `json:"items"`
	SubtotalTRY              float64             `json:"subtotal_try"`
	DeliveryFeeTRY           float64             `json:"delivery_fee_try"`
	TotalTRY                 float64             `json:"total_try"`
	MinOrderTRY              float64             `json:"min_order_try"`
	MeetsMinimum             bool                `json:"meets_minimum"`
	FreeDeliveryThresholdTRY float64             `json:"free_delivery_threshold_try"`
	FreeDeliveryRemainingTRY float64             `json:"free_delivery_remaining_try"`
}

type quoteLineView struct {
	ItemID       domain.ItemID `json:"item_id"`
	Name         string        `json:"name"`
	Qty          int           `json:"qty"`
	UnitPriceTRY float64       `json:"unit_price_try"`
}

func newQuoteView(r domain.Restaurant, q pricing.Quote) quoteView {
	lines := make([]quoteLineView, 0, len(q.Lines))
	for _, l := range q.Lines {
		lines = append(lines, quoteLineView{ItemID: l.ItemID, Name: l.Name, Qty: l.Qty, UnitPriceTRY: l.UnitPrice.TL()})
	}
	return quoteView{
		RestaurantID: r.ID, RestaurantName: r.Name, Items: lines,
		SubtotalTRY: q.Subtotal.TL(), DeliveryFeeTRY: q.DeliveryFee.TL(), TotalTRY: q.Total.TL(),
		MinOrderTRY: q.MinOrder.TL(), MeetsMinimum: q.MeetsMinimum,
		FreeDeliveryThresholdTRY: q.FreeDeliveryThreshold.TL(),
		FreeDeliveryRemainingTRY: q.FreeDeliveryShortfall().TL(),
	}
}

type ordersView struct {
	Orders []orderView `json:"orders"`
}

type orderView struct {
	OrderID    string  `json:"order_id"`
	Date       string  `json:"date"`
	Restaurant string  `json:"restaurant"`
	TotalTRY   float64 `json:"total_try"`
	Status     string  `json:"status"`
	Note       string  `json:"note,omitempty"`
}

type knowledgeView struct {
	Results []docView `json:"results"`
	Note    string    `json:"note,omitempty"`
}

type docView struct {
	DocID            string    `json:"doc_id"`
	Title            string    `json:"title"`
	Category         string    `json:"category"`
	Date             string    `json:"date"`
	Status           string    `json:"status"`
	Content          string    `json:"content"`
	OutdatedVersions []docView `json:"outdated_versions,omitempty"`
}

func newDocView(d knowledge.Document, status string) docView {
	return docView{DocID: d.ID, Title: d.Title, Category: d.Category, Date: d.Date, Status: status, Content: framed(d.Body)}
}

type outcomeView struct {
	Status               action.Status    `json:"status"`
	Requirement          gate.Requirement `json:"requirement,omitempty"`
	Reason               string           `json:"reason,omitempty"`
	ConfirmationRejected confirm.Reason   `json:"confirmation_rejected,omitempty"`
	Note                 string           `json:"note,omitempty"`
	AlreadyUsedFor       string           `json:"already_used_for,omitempty"`
	ExpiresAt            string           `json:"expires_at,omitempty"`
	ConfirmWithParams    map[string]any   `json:"confirm_with_params,omitempty"`
	TotalTRY             *float64         `json:"total_try,omitempty"`
	Payment              *paymentView     `json:"payment,omitempty"`
	Assumptions          []string         `json:"assumptions,omitempty"`
	Receipt              *receiptView     `json:"receipt,omitempty"`
}

type paymentView struct {
	FromWalletTRY float64 `json:"from_wallet_try"`
	FromCardTRY   float64 `json:"from_card_try"`
}

type receiptView struct {
	Event    action.Event       `json:"event"`
	OrderID  domain.OrderID     `json:"order_id"`
	Status   domain.OrderStatus `json:"status"`
	TotalTRY *float64           `json:"total_try,omitempty"`
	TipTRY   *float64           `json:"tip_try,omitempty"`
	Refund   *refundView        `json:"refund,omitempty"`
}

type refundView struct {
	ToWalletTRY                   float64 `json:"to_wallet_try"`
	ToCardTRY                     float64 `json:"to_card_try"`
	OrderRefundedByPaymentsSystem bool    `json:"order_refunded_by_payments_system"`
	Summary                       string  `json:"summary"`
}

func newOutcomeView(o action.Outcome, params map[string]any) outcomeView {
	v := outcomeView{Status: o.Status, AlreadyUsedFor: o.Used, Assumptions: ui.AssumptionTexts(o)}
	if o.Violation != nil {
		v.Requirement, v.Reason = o.Violation.Requirement, o.Violation.Detail
	}
	if o.Rejection != "" {
		v.ConfirmationRejected = o.Rejection
		v.Note = "The confirmation could not be used and nothing was executed. A new confirmation was issued."
		if o.Status == action.Rejected {
			v.Note = "The confirmation could not be used and nothing was executed. No new confirmation was issued."
		}
	}
	if o.Token != nil {
		v.ExpiresAt = o.Token.ExpiresAt.Format(time.RFC3339)
		v.ConfirmWithParams = params
	}
	if o.Quote != nil {
		v.TotalTRY = tlPtr(o.Quote.Total)
	}
	if o.Status == action.NeedsConfirmation && o.Action.Spends() {
		v.Payment = &paymentView{FromWalletTRY: o.Payment.FromWallet.TL(), FromCardTRY: o.Payment.FromCard.TL()}
	}
	if r := o.Receipt; r != nil {
		v.Receipt = newReceiptView(*r)
	}
	return v
}

func newReceiptView(r action.Receipt) *receiptView {
	v := &receiptView{Event: r.Event, OrderID: r.OrderID, Status: r.OrderStatus}
	if r.Event == action.EventTipAdded {
		v.TipTRY = tlPtr(r.Amount)
	} else {
		v.TotalTRY = tlPtr(r.Amount)
	}
	if f := r.Refund; f != nil {
		v.Refund = &refundView{
			ToWalletTRY: f.ToWallet.TL(), ToCardTRY: f.ToCard.TL(),
			OrderRefundedByPaymentsSystem: f.OrderUnrecorded, Summary: ui.RefundText(r),
		}
	}
	return v
}

func tlPtr(m domain.Money) *float64 {
	v := m.TL()
	return &v
}
