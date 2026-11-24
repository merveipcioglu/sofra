package gate

import (
	"fmt"
	"time"

	"sofra/internal/domain"
	"sofra/internal/policy"
	"sofra/internal/pricing"
)

type CancelOrderRequest struct {
	Order *domain.Order
}

func EvaluateCancelOrder(req CancelOrderRequest) *Violation {
	return run(cancelOrderChecks, req)
}

var cancelOrderChecks = []check[CancelOrderRequest]{
	func(r CancelOrderRequest) *Violation { return orderOwned(r.Order) },
	cancellable,
}

func cancellable(r CancelOrderRequest) *Violation {
	if r.Order.Cancellable() {
		return nil
	}
	return &Violation{
		Requirement: NotCancellable,
		Detail: fmt.Sprintf("Order %s is %s; an order can be cancelled only before the restaurant starts preparing it.",
			r.Order.ID, r.Order.Status),
	}
}

type AddTipRequest struct {
	User      domain.User
	Order     *domain.Order
	PriorTips []domain.Tip
	Amount    domain.Money
	Now       time.Time
}

type AddTipResult struct {
	Violation *Violation
	Payment   pricing.PaymentPlan
}

func EvaluateAddTip(req AddTipRequest) AddTipResult {
	st := tipState{req: req}
	st.payment, st.funded = pricing.PlanPayment(req.User, req.Amount)
	if v := run(addTipChecks, st); v != nil {
		return AddTipResult{Violation: v}
	}
	return AddTipResult{Payment: st.payment}
}

type tipState struct {
	req     AddTipRequest
	payment pricing.PaymentPlan
	funded  bool
}

var addTipChecks = []check[tipState]{
	func(st tipState) *Violation { return orderOwned(st.req.Order) },
	withinTipWindow,
	tipAmountAllowed,
	tipFunded,
}

func withinTipWindow(st tipState) *Violation {
	o, window := st.req.Order, policy.TipWindowDays
	switch {
	case o.Status == domain.OrderCancelled:
		return &Violation{Requirement: TipWindowExpired, Detail: fmt.Sprintf("Order %s was cancelled.", o.ID)}
	case o.Status == domain.OrderDelivered && domain.DaysBetween(o.Date, st.req.Now) > window:
		return &Violation{
			Requirement: TipWindowExpired,
			Detail: fmt.Sprintf("Order %s was delivered on %s; tips can be left within %d days after delivery.",
				o.ID, domain.FormatDate(o.Date), window),
		}
	default:
		return nil
	}
}

func tipAmountAllowed(st tipState) *Violation {
	if st.req.Amount <= 0 {
		return &Violation{Requirement: InvalidAmount, Detail: fmt.Sprintf("A tip must be more than 0; %v was asked.", st.req.Amount)}
	}
	o := st.req.Order
	limit := min(o.Total, policy.TipCap)
	var tipped domain.Money
	for _, t := range st.req.PriorTips {
		if t.OrderID == o.ID {
			tipped += t.Amount
		}
	}
	remaining := limit - tipped
	if st.req.Amount <= remaining {
		return nil
	}
	detail := fmt.Sprintf("A tip on order %s can be at most %v.", o.ID, limit)
	if tipped > 0 {
		detail = fmt.Sprintf("Order %s has %v in tips already; tips on it are capped at %v in total, so at most %v more can be left.",
			o.ID, tipped, limit, max(remaining, 0))
	}
	return &Violation{Requirement: InvalidAmount, Detail: detail}
}

func tipFunded(st tipState) *Violation {
	if !st.funded {
		return insufficientFunds(st.req.User, st.req.Amount, st.payment.Shortfall)
	}
	return nil
}

func orderOwned(o *domain.Order) *Violation {
	if o != nil {
		return nil
	}
	return &Violation{Requirement: OrderNotFound, Detail: "No such order on this account."}
}
