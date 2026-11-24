package confirm

import (
	"testing"

	"sofra/internal/domain"
)

func order() PlaceOrder {
	return PlaceOrder{
		RestaurantID: "rst_04",
		Items:        []domain.LineItem{{ItemID: "itm_014", Qty: 1}, {ItemID: "itm_013", Qty: 2}},
		Subtotal:     24000, DeliveryFee: 3000, Total: 27000,
		ClearsCart: true,
		Paid:       domain.Paid{Wallet: 10000, Card: 17000},
	}
}

func TestPlaceOrderResolved(t *testing.T) {
	base := PlaceOrderResolved(order()).hash()
	same := []struct {
		name string
		edit func(*PlaceOrder)
	}{
		{"items in another order", func(o *PlaceOrder) { o.Items[0], o.Items[1] = o.Items[1], o.Items[0] }},
		{"an item split across two lines", func(o *PlaceOrder) {
			o.Items = []domain.LineItem{{ItemID: "itm_013", Qty: 1}, {ItemID: "itm_014", Qty: 1}, {ItemID: "itm_013", Qty: 1}}
		}},
	}
	for _, c := range same {
		t.Run("same: "+c.name, func(t *testing.T) {
			o := order()
			c.edit(&o)
			if PlaceOrderResolved(o).hash() != base {
				t.Error("the same order hashes differently")
			}
		})
	}

	changed := []struct {
		name string
		edit func(*PlaceOrder)
	}{
		{"restaurant", func(o *PlaceOrder) { o.RestaurantID = "rst_11" }},
		{"an item", func(o *PlaceOrder) { o.Items[0].ItemID = "itm_015" }},
		{"a quantity", func(o *PlaceOrder) { o.Items[1].Qty = 3 }},
		{"subtotal and fee", func(o *PlaceOrder) { o.Subtotal, o.DeliveryFee = 27000, 0 }},
		{"total", func(o *PlaceOrder) { o.Total = 28000 }},
		{"effect on the cart", func(o *PlaceOrder) { o.ClearsCart = false }},
		{"wallet share", func(o *PlaceOrder) { o.Paid.Wallet = 9000 }},
		{"card share", func(o *PlaceOrder) { o.Paid.Card = 18000 }},
	}
	for _, c := range changed {
		t.Run("changed: "+c.name, func(t *testing.T) {
			o := order()
			c.edit(&o)
			if PlaceOrderResolved(o).hash() == base {
				t.Error("a change to a bound field kept the hash")
			}
		})
	}
}

func TestAddTipResolved(t *testing.T) {
	paid := domain.Paid{Wallet: 2000, Card: 3000}
	base := AddTipResolved("u_ok_o9", 5000, paid).hash()
	for name, p := range map[string]Params{
		"order":        AddTipResolved("u_ok_o1", 5000, paid),
		"amount":       AddTipResolved("u_ok_o9", 6000, paid),
		"wallet share": AddTipResolved("u_ok_o9", 5000, domain.Paid{Wallet: 1000, Card: 3000}),
		"card share":   AddTipResolved("u_ok_o9", 5000, domain.Paid{Wallet: 2000, Card: 4000}),
	} {
		if p.hash() == base {
			t.Errorf("changed %s kept the hash", name)
		}
	}
}

func TestPlaceOrderEncodingIsPinned(t *testing.T) {
	const want = `{"restaurant_id":"rst_04","items":[{"item_id":"itm_013","qty":2},{"item_id":"itm_014","qty":1}],` +
		`"subtotal_kurus":24000,"delivery_fee_kurus":3000,"total_kurus":27000,"clears_cart":true,` +
		`"wallet_kurus":10000,"card_kurus":17000}`
	for range 20 {
		if got := string(PlaceOrderResolved(order()).canonical); got != want {
			t.Fatalf("encoding\n got %s\nwant %s", got, want)
		}
	}
}

func TestPlaceOrderRequest(t *testing.T) {
	items := func(li ...domain.LineItem) []domain.LineItem { return li }
	burger, fries := domain.LineItem{ItemID: "itm_013", Qty: 2}, domain.LineItem{ItemID: "itm_014", Qty: 1}
	base := PlaceOrderRequest("rst_04", items(burger, fries), "").hash()
	for name, p := range map[string]Params{
		"items in another order":         PlaceOrderRequest("rst_04", items(fries, burger), ""),
		"an item split across two lines": PlaceOrderRequest("rst_04", items(domain.LineItem{ItemID: "itm_013", Qty: 1}, fries, domain.LineItem{ItemID: "itm_013", Qty: 1}), ""),
	} {
		if p.hash() != base {
			t.Errorf("same request, %s, hashes differently", name)
		}
	}
	for name, p := range map[string]Params{
		"restaurant": PlaceOrderRequest("rst_11", items(burger, fries), ""),
		"quantity":   PlaceOrderRequest("rst_04", items(domain.LineItem{ItemID: "itm_013", Qty: 5}, fries), ""),
		"an item":    PlaceOrderRequest("rst_04", items(burger), ""),
		"cart mode":  PlaceOrderRequest("rst_04", items(burger, fries), "request_only"),
	} {
		if p.hash() == base {
			t.Errorf("changed %s kept the hash", name)
		}
	}
}

func TestAddTipRequest(t *testing.T) {
	base := AddTipRequest("u_ok_o9", 5000).hash()
	if AddTipRequest("u_ok_o1", 5000).hash() == base || AddTipRequest("u_ok_o9", 6000).hash() == base {
		t.Error("a changed tip request kept the hash")
	}
}
