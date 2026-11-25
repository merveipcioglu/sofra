package gate

import (
	"fmt"

	"sofra/internal/domain"
	"sofra/internal/pricing"
)

type PlaceOrderRequest struct {
	User       domain.User
	Restaurant domain.Restaurant
	Items      []domain.LineItem
}

type PlaceOrderResult struct {
	Violation *Violation
	Quote     pricing.Quote
	Payment   pricing.PaymentPlan
}

func EvaluatePlaceOrder(req PlaceOrderRequest) PlaceOrderResult {
	st := newOrderState(req)
	if v := run(placeOrderChecks, st); v != nil {
		return PlaceOrderResult{Violation: v, Quote: st.quote}
	}
	return PlaceOrderResult{Quote: st.quote, Payment: st.payment}
}

var placeOrderChecks = []check[*orderState]{
	deliversToUser,
	hasItems,
	quantitiesValid,
	itemsOrderable,
	ageVerifiedForRestricted,
	meetsMinimum,
	orderFunded,
}

type orderState struct {
	req      PlaceOrderRequest
	resolved []pricing.Line
	missing  []domain.ItemID
	quoteErr error
	quote    pricing.Quote
	payment  pricing.PaymentPlan
	funded   bool
}

func newOrderState(req PlaceOrderRequest) *orderState {
	st := &orderState{req: req}
	for _, li := range req.Items {
		item, ok := req.Restaurant.MenuItem(li.ItemID)
		if !ok {
			st.missing = append(st.missing, li.ItemID)
			continue
		}
		st.resolved = append(st.resolved, pricing.Line{Item: item, Qty: li.Qty})
	}
	if len(st.resolved) == 0 {
		return st
	}
	st.quote, st.quoteErr = pricing.QuoteOrder(req.Restaurant, st.resolved)
	if st.quoteErr == nil {
		st.payment, st.funded = pricing.PlanPayment(req.User, st.quote.Total)
	}
	return st
}

func deliversToUser(st *orderState) *Violation {
	r, u := st.req.Restaurant, st.req.User
	if r.DeliversTo(u.District) {
		return nil
	}
	return &Violation{
		Requirement: OutOfServiceArea,
		Detail:      fmt.Sprintf("%s does not deliver to %s.", r.Name, u.District),
	}
}

func hasItems(st *orderState) *Violation {
	if len(st.req.Items) > 0 {
		return nil
	}
	return &Violation{Requirement: InvalidRequest, Detail: "The order has no items."}
}

func quantitiesValid(st *orderState) *Violation {
	if st.quoteErr == nil {
		return nil
	}
	return &Violation{Requirement: InvalidRequest, Detail: st.quoteErr.Error()}
}

func itemsOrderable(st *orderState) *Violation {
	r := st.req.Restaurant
	if len(st.missing) > 0 {
		return &Violation{
			Requirement: ItemUnavailable,
			Detail:      fmt.Sprintf("Item %s is not on the %s menu.", st.missing[0], r.Name),
		}
	}
	for _, l := range st.resolved {
		if !l.Item.Available {
			return &Violation{
				Requirement: ItemUnavailable,
				Detail:      fmt.Sprintf("%s is currently unavailable at %s.", l.Item.Name, r.Name),
			}
		}
	}
	return nil
}

func ageVerifiedForRestricted(st *orderState) *Violation {
	if st.req.User.AgeVerified {
		return nil
	}
	for _, l := range st.resolved {
		if l.Item.AgeRestricted {
			return &Violation{
				Requirement: Age18Plus,
				Detail:      fmt.Sprintf("%s is sold only to users aged 18+ who have completed age verification.", l.Item.Name),
			}
		}
	}
	return nil
}

func meetsMinimum(st *orderState) *Violation {
	if st.quote.MeetsMinimum {
		return nil
	}
	return &Violation{
		Requirement: MinOrder,
		Detail: fmt.Sprintf("The item subtotal %v is below the %v minimum at %s: %v short.",
			st.quote.Subtotal, st.quote.MinOrder, st.req.Restaurant.Name, st.quote.MinimumShortfall()),
	}
}

func orderFunded(st *orderState) *Violation {
	if st.funded {
		return nil
	}
	return insufficientFunds(st.req.User, st.quote.Total, st.payment.Shortfall)
}

func insufficientFunds(u domain.User, amount, shortfall domain.Money) *Violation {
	return &Violation{
		Requirement: SufficientFunds,
		Detail: fmt.Sprintf("The wallet balance %v does not cover %v and there is no saved card: %v short.",
			u.WalletBalance, amount, shortfall),
	}
}
