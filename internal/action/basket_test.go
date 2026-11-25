package action

import (
	"slices"
	"testing"
	"time"

	"sofra/internal/domain"
	"sofra/internal/gate"
	"sofra/internal/store"
)

const (
	burgerStop   domain.RestaurantID = "rst_04"
	cheeseburger domain.ItemID       = "itm_013"
	fries        domain.ItemID       = "itm_014"
)

func loadStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Load("../../data")
	if err != nil {
		t.Fatal(err)
	}
	return st
}

func TestRequestedQuantityBelowOneIsRefused(t *testing.T) {
	st := loadStore(t)
	cart, err := st.Cart("u_ok")
	if err != nil {
		t.Fatal(err)
	}
	cart.Items[0].Qty = 2 // fries ×2, so -1 would still merge to a valid ×1
	if err := st.Transact(func(tx *store.Tx) error { return tx.SaveCart("u_ok", cart) }); err != nil {
		t.Fatal(err)
	}

	for _, qty := range []int{-1, 0} {
		req := PlaceOrderRequest{UserID: "u_ok", RestaurantID: burgerStop,
			Items: []domain.LineItem{{ItemID: cheeseburger, Qty: 2}, {ItemID: fries, Qty: qty}}}
		ev, err := req.evaluate(st, time.Time{})
		if err != nil {
			t.Fatal(err)
		}
		if ev.stop == nil || ev.stop.Violation == nil || ev.stop.Violation.Requirement != gate.InvalidRequest {
			t.Errorf("qty %d: not refused as an invalid request (%+v)", qty, ev.stop)
		}
	}
}

func TestBuildBasket(t *testing.T) {
	cart := domain.Cart{RestaurantID: burgerStop, Items: []domain.LineItem{{ItemID: fries, Qty: 1}}}
	two := []domain.LineItem{{ItemID: cheeseburger, Qty: 2}}
	cases := []struct {
		name         string
		restaurant   domain.RestaurantID
		requested    []domain.LineItem
		mode         CartMode
		want         []domain.LineItem
		includesCart bool
	}{
		{"same restaurant: request added to the cart", burgerStop, two, CartAuto,
			[]domain.LineItem{{ItemID: fries, Qty: 1}, {ItemID: cheeseburger, Qty: 2}}, true},
		{"same item: quantities summed", burgerStop, []domain.LineItem{{ItemID: fries, Qty: 2}}, CartAuto,
			[]domain.LineItem{{ItemID: fries, Qty: 3}}, true},
		{"request_only: cart left out", burgerStop, two, CartRequestOnly, two, false},
		{"other restaurant: cart left out", "rst_11", two, CartAuto, two, false},
		{"nothing requested: the cart", burgerStop, nil, CartAuto, cart.Items, true},
		{"nothing requested, other restaurant: empty", "rst_11", nil, CartAuto, nil, false},
		{"item named twice: one line", "rst_11", []domain.LineItem{{ItemID: cheeseburger, Qty: 1}, {ItemID: cheeseburger, Qty: 1}}, CartAuto, two, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			b := buildBasket(cart, c.restaurant, c.requested, c.mode)
			if !slices.Equal(b.items, c.want) || b.includesCart != c.includesCart {
				t.Errorf("got %v includesCart=%t, want %v %t", b.items, b.includesCart, c.want, c.includesCart)
			}
		})
	}
	if cart.Items[0].Qty != 1 {
		t.Error("buildBasket changed the stored cart")
	}
}

func TestCartAssumption(t *testing.T) {
	st := loadStore(t)
	r, err := st.Restaurant(burgerStop)
	if err != nil {
		t.Fatal(err)
	}
	cart := domain.Cart{RestaurantID: burgerStop, Items: []domain.LineItem{{ItemID: fries, Qty: 1}}}
	req := []domain.LineItem{{ItemID: fries, Qty: 2}, {ItemID: cheeseburger, Qty: 1}}

	a := cartAssumption(r, cart, "", req, buildBasket(cart, burgerStop, req, CartAuto))
	if a == nil || a.Kind != CartIncluded || a.CartOnly || len(a.Lines) != 1 ||
		a.Lines[0] != (AssumedLine{Name: "Patates Kızartması", InCart: 1, Requested: 2}) {
		t.Errorf("merged order: %+v", a)
	}
	if a := cartAssumption(r, cart, "", req, buildBasket(cart, burgerStop, req, CartRequestOnly)); a == nil || a.Kind != CartKept {
		t.Errorf("request_only: %+v, want %s", a, CartKept)
	}
	other := domain.Cart{RestaurantID: "rst_11", Items: []domain.LineItem{{ItemID: "itm_x", Qty: 1}}}
	if a := cartAssumption(r, other, "Napoli Fırın", req, buildBasket(other, burgerStop, req, CartAuto)); a == nil ||
		a.Kind != OtherCartKept || a.CartRestaurant != "Napoli Fırın" {
		t.Errorf("other restaurant's cart: %+v", a)
	}
	if a := cartAssumption(r, domain.Cart{}, "", req, buildBasket(domain.Cart{}, burgerStop, req, CartAuto)); a != nil {
		t.Errorf("empty cart: %+v, want none", a)
	}
}

func TestQuantityCap(t *testing.T) {
	st := loadStore(t)
	cart, err := st.Cart("u_ok")
	if err != nil {
		t.Fatal(err)
	}
	cart.Items[0].Qty = 30 // fries ×30 in the cart
	if err := st.Transact(func(tx *store.Tx) error { return tx.SaveCart("u_ok", cart) }); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name    string
		items   []domain.LineItem
		mode    CartMode
		refused bool
	}{
		{"51 requested", []domain.LineItem{{ItemID: cheeseburger, Qty: 51}}, CartRequestOnly, true},
		{"the quantity that wrapped to 400 TL", []domain.LineItem{{ItemID: cheeseburger, Qty: 2246718829490265904}}, CartRequestOnly, true},
		{"30 in the cart + 30 requested", []domain.LineItem{{ItemID: fries, Qty: 30}}, CartAuto, true},
		{"50 requested", []domain.LineItem{{ItemID: cheeseburger, Qty: 50}}, CartRequestOnly, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req := PlaceOrderRequest{UserID: "u_ok", RestaurantID: burgerStop, Items: c.items, CartMode: c.mode}
			ev, err := req.evaluate(st, time.Time{})
			if err != nil {
				t.Fatal(err)
			}
			refused := ev.stop != nil && ev.stop.Violation != nil && ev.stop.Violation.Requirement == gate.InvalidRequest
			if refused != c.refused {
				t.Errorf("refused as an invalid request: %t, want %t (%+v)", refused, c.refused, ev.stop)
			}
		})
	}
}
