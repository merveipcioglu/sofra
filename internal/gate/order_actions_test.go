package gate

import (
	"math"
	"strings"
	"testing"
	"time"

	"sofra/internal/domain"
)

var (
	istanbul   = domain.Istanbul
	now        = time.Date(2026, 8, 20, 12, 0, 0, 0, istanbul)
	payingUser = domain.User{ID: "u", WalletBalance: tl(1000), HasSavedCard: true}
	brokeUser  = domain.User{ID: "u"}
)

func order(status domain.OrderStatus, date time.Time) *domain.Order {
	return &domain.Order{ID: "o", Status: status, Date: date, Total: tl(1000)}
}

func day(d int) time.Time { return time.Date(2026, 8, d, 0, 0, 0, 0, istanbul) }

func requirement(v *Violation) Requirement {
	if v == nil {
		return ""
	}
	return v.Requirement
}

func TestCancelOrderGates(t *testing.T) {
	cases := []struct {
		name  string
		order *domain.Order
		want  Requirement
	}{
		{"another user's or no order: nothing else is checked", nil, OrderNotFound},
		{"delivered", order(domain.OrderDelivered, day(19)), NotCancellable},
		{"cancelled", order(domain.OrderCancelled, day(19)), NotCancellable},
		{"received", order(domain.OrderReceived, day(19)), ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := requirement(EvaluateCancelOrder(CancelOrderRequest{Order: c.order})); got != c.want {
				t.Errorf("got %q, want %q", got, c.want)
			}
		})
	}
}

func tip(user domain.User, o *domain.Order, amount domain.Money, at time.Time, prior ...domain.Tip) AddTipResult {
	return EvaluateAddTip(AddTipRequest{User: user, Order: o, PriorTips: prior, Amount: amount, Now: at})
}

func TestAddTipGateOrder(t *testing.T) {
	cases := []struct {
		name string
		res  AddTipResult
		want Requirement
	}{
		{"no order, bad amount, no money: ownership", tip(brokeUser, nil, 0, now), OrderNotFound},
		{"cancelled, bad amount, no money: window", tip(brokeUser, order(domain.OrderCancelled, day(19)), 0, now), TipWindowExpired},
		{"expired, bad amount, no money: window", tip(brokeUser, order(domain.OrderDelivered, day(1)), 0, now), TipWindowExpired},
		{"over the cap, no money: amount", tip(brokeUser, order(domain.OrderDelivered, day(19)), tl(600), now), InvalidAmount},
		{"no money: funds", tip(brokeUser, order(domain.OrderDelivered, day(19)), tl(50), now), SufficientFunds},
		{"on its way: allowed", tip(payingUser, order(domain.OrderReceived, day(19)), tl(50), now), ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := requirement(c.res.Violation); got != c.want {
				t.Errorf("got %q, want %q", got, c.want)
			}
		})
	}
}

func TestTipWindowBoundary(t *testing.T) {
	utc := func(d, h, m int) time.Time { return time.Date(2026, 8, d, h, m, 0, 0, time.UTC) }
	cases := []struct {
		name      string
		delivered time.Time
		at        time.Time
		allowed   bool
	}{
		{"7th day", day(13), now, true},
		{"8th day", day(12), now, false},
		{"7th day, just before Istanbul midnight", day(13), utc(20, 20, 30), true},
		{"8th day, just after Istanbul midnight", day(13), utc(20, 21, 30), false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			res := tip(payingUser, order(domain.OrderDelivered, c.delivered), tl(50), c.at)
			if allowed := res.Violation == nil; allowed != c.allowed {
				t.Errorf("allowed %t, want %t (%v)", allowed, c.allowed, res.Violation)
			}
		})
	}

	res := tip(payingUser, order(domain.OrderDelivered, utc(11, 22, 30)), tl(50), now)
	if res.Violation == nil || !strings.Contains(res.Violation.Detail, "delivered on 2026-08-12") {
		t.Errorf("got %v, want the delivery dated 2026-08-12", res.Violation)
	}
}

func TestTipCapIsCumulative(t *testing.T) {
	o := order(domain.OrderDelivered, day(19))
	onThis := domain.Tip{OrderID: "o", Amount: tl(450)}
	onOther := domain.Tip{OrderID: "other", Amount: tl(450)}
	cases := []struct {
		name    string
		amount  domain.Money
		prior   []domain.Tip
		allowed bool
		detail  string
	}{
		{"up to the cap", tl(50), []domain.Tip{onThis}, true, ""},
		{"past the cap", tl(60), []domain.Tip{onThis}, false, "at most 50.00 TL more"},
		{"another order's tips do not count", tl(500), []domain.Tip{onOther}, true, ""},
		{"zero with earlier tips: the amount, not the cap", 0, []domain.Tip{onThis}, false, "must be more than 0"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			v := tip(payingUser, o, c.amount, now, c.prior...).Violation
			if allowed := v == nil; allowed != c.allowed {
				t.Fatalf("allowed %t, want %t (%v)", allowed, c.allowed, v)
			}
			if v != nil && !strings.Contains(v.Detail, c.detail) {
				t.Errorf("detail %q, want it to say %q", v.Detail, c.detail)
			}
		})
	}
}

func tl(lira float64) domain.Money { return domain.Money(math.Round(lira * 100)) }
