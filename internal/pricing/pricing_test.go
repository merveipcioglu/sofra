package pricing

import (
	"math"
	"slices"
	"testing"

	"sofra/internal/domain"
)

func TestPlanPayment(t *testing.T) {
	total := tl(180)
	cases := []struct {
		name       string
		wallet     domain.Money
		card       bool
		funded     bool
		fromWallet domain.Money
		fromCard   domain.Money
		shortfall  domain.Money
	}{
		{"wallet covers it, card", tl(500), true, true, tl(180), 0, 0},
		{"wallet covers it, no card", tl(500), false, true, tl(180), 0, 0},
		{"wallet covers it exactly, no card", tl(180), false, true, tl(180), 0, 0},
		{"wallet short, card takes the rest", tl(100), true, true, tl(100), tl(80), 0},
		{"empty wallet, card takes it all", 0, true, true, 0, tl(180), 0},
		{"wallet short, no card", tl(30), false, false, 0, 0, tl(150)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			plan, funded := PlanPayment(domain.User{WalletBalance: c.wallet, HasSavedCard: c.card}, total)
			want := PaymentPlan{FromWallet: c.fromWallet, FromCard: c.fromCard, Shortfall: c.shortfall}
			if funded != c.funded || plan != want {
				t.Errorf("got %+v funded=%t, want %+v funded=%t", plan, funded, want, c.funded)
			}
		})
	}
}

func TestQuoteOrder(t *testing.T) {
	r := domain.Restaurant{MinOrder: tl(100), DeliveryFee: tl(30)}
	item := func(price float64) domain.MenuItem { return domain.MenuItem{ID: "x", Price: tl(price)} }
	burger := domain.MenuItem{ID: "burger", Name: "Burger", Price: tl(100)}
	soup := domain.MenuItem{ID: "soup", Name: "Soup", Price: tl(50)}
	cases := []struct {
		name          string
		lines         []Line
		fee, total    domain.Money
		meetsMinimum  bool
		toMinimum     domain.Money
		toFreeDeliver domain.Money
	}{
		{"just below the threshold: the restaurant's fee", []Line{{item(249.99), 1}}, tl(30), tl(279.99), true, 0, tl(0.01)},
		{"at the threshold: free (>= 250)", []Line{{item(250), 1}}, 0, tl(250), true, 0, 0},
		{"the threshold is on the subtotal, not the total", []Line{{item(240), 1}}, tl(30), tl(270), true, 0, tl(10)},
		{"lines summed: 2 × 100 + 50 reaches the threshold", []Line{{burger, 2}, {soup, 1}}, 0, tl(250), true, 0, 0},
		{"at the minimum: met (>= min)", []Line{{item(100), 1}}, tl(30), tl(130), true, 0, tl(150)},
		{"the fee does not count toward the minimum", []Line{{item(90), 1}}, tl(30), tl(120), false, tl(10), tl(160)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			q, err := QuoteOrder(r, c.lines)
			if err != nil {
				t.Fatal(err)
			}
			if q.DeliveryFee != c.fee || q.Total != c.total || q.MeetsMinimum != c.meetsMinimum ||
				q.MinimumShortfall() != c.toMinimum || q.FreeDeliveryShortfall() != c.toFreeDeliver {
				t.Errorf("fee %v total %v meets %t to-minimum %v to-free %v; want %v %v %t %v %v",
					q.DeliveryFee, q.Total, q.MeetsMinimum, q.MinimumShortfall(), q.FreeDeliveryShortfall(),
					c.fee, c.total, c.meetsMinimum, c.toMinimum, c.toFreeDeliver)
			}
		})
	}

	q, err := QuoteOrder(r, []Line{{burger, 2}, {soup, 1}})
	if err != nil {
		t.Fatal(err)
	}
	want := []QuotedLine{
		{ItemID: "burger", Name: "Burger", Qty: 2, UnitPrice: tl(100)},
		{ItemID: "soup", Name: "Soup", Qty: 1, UnitPrice: tl(50)},
	}
	if !slices.Equal(q.Lines, want) {
		t.Errorf("lines %+v, want %+v", q.Lines, want)
	}

	if q, err := QuoteOrder(r, nil); err == nil {
		t.Errorf("an empty basket was priced: total %v", q.Total)
	}
}

func tl(lira float64) domain.Money { return domain.Money(math.Round(lira * 100)) }
