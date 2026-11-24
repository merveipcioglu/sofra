package gate

import (
	"strings"
	"testing"

	"sofra/internal/domain"
)

var restaurant = domain.Restaurant{
	ID: "r", Name: "R", MinOrder: tl(100), DeliveryFee: tl(30), DeliveryDistricts: []string{"Kadıköy"},
	Menu: []domain.MenuItem{
		{ID: "burger", Name: "Burger", Price: tl(150), Available: true},
		{ID: "soup", Name: "Soup", Price: tl(50), Available: true},
		{ID: "energy", Name: "Energy drink", Price: tl(40), Available: true, AgeRestricted: true},
		{ID: "kebab", Name: "Kebab", Price: tl(200)}, 
	},
}

var (
	adult    = domain.User{District: "Kadıköy", AgeVerified: true, WalletBalance: tl(10000), HasSavedCard: true}
	minor    = domain.User{District: "Kadıköy", WalletBalance: tl(10000), HasSavedCard: true}
	broke    = domain.User{District: "Kadıköy", AgeVerified: true}
	outsider = domain.User{District: "Üsküdar", AgeVerified: true, WalletBalance: tl(10000), HasSavedCard: true}
)

func items(pairs ...any) []domain.LineItem {
	var out []domain.LineItem
	for i := 0; i < len(pairs); i += 2 {
		out = append(out, domain.LineItem{ItemID: domain.ItemID(pairs[i].(string)), Qty: pairs[i+1].(int)})
	}
	return out
}

func TestPlaceOrderGateOrder(t *testing.T) {
	cases := []struct {
		name  string
		user  domain.User
		items []domain.LineItem
		want  Requirement
	}{
		{"out of area, no items named (prompt 12): area", outsider, nil, OutOfServiceArea},
		{"out of area, 60 of an item: area", outsider, items("burger", 60), OutOfServiceArea},
		{"no items", adult, nil, InvalidRequest},
		{"60 of an item and an unavailable one: quantity", adult, items("burger", 60, "kebab", 1), InvalidRequest},
		{"not on the menu, and age-restricted: item", minor, items("nope", 1, "energy", 1), ItemUnavailable},
		{"only an item not on the menu: item", adult, items("nope", 1), ItemUnavailable},
		{"unavailable, and age-restricted: item", minor, items("kebab", 1, "energy", 1), ItemUnavailable},
		{"age-restricted and below the minimum (prompt 6): age", minor, items("energy", 1), Age18Plus},
		{"below the minimum, cannot pay: minimum", broke, items("soup", 1), MinOrder},
		{"cannot pay", broke, items("burger", 1), SufficientFunds},
		{"allowed", adult, items("burger", 1), ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			res := EvaluatePlaceOrder(PlaceOrderRequest{User: c.user, Restaurant: restaurant, Items: c.items})
			if got := requirement(res.Violation); got != c.want {
				t.Errorf("got %q (%v), want %q", got, res.Violation, c.want)
			}
		})
	}
}

func TestPlaceOrderQuote(t *testing.T) {
	below := EvaluatePlaceOrder(PlaceOrderRequest{User: broke, Restaurant: restaurant, Items: items("soup", 1)})
	if below.Quote.Subtotal != tl(50) || !strings.Contains(below.Violation.Detail, "50.00 TL short") {
		t.Errorf("min_order: subtotal %v, reason %q; want 50.00 TL, and 50.00 TL short", below.Quote.Subtotal, below.Violation.Detail)
	}
	ok := EvaluatePlaceOrder(PlaceOrderRequest{User: adult, Restaurant: restaurant, Items: items("burger", 1)})
	if ok.Quote.Total != tl(180) || ok.Payment.FromWallet != tl(180) {
		t.Errorf("allowed: total %v from wallet %v, want 180.00 TL (150 + 30 delivery)", ok.Quote.Total, ok.Payment.FromWallet)
	}
}
