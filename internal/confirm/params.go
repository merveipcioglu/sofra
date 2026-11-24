package confirm

import (
	"cmp"
	"crypto/sha256"
	"encoding/json"
	"slices"

	"sofra/internal/domain"
)

type Params struct {
	action    domain.Action
	canonical []byte
}

func (p Params) Action() domain.Action { return p.action }

func (p Params) hash() [sha256.Size]byte { return sha256.Sum256(p.canonical) }

type PlaceOrder struct {
	RestaurantID                 domain.RestaurantID
	Items                        []domain.LineItem
	Subtotal, DeliveryFee, Total domain.Money
	ClearsCart                   bool
	Paid                         domain.Paid
}

type line struct {
	ItemID domain.ItemID `json:"item_id"`
	Qty    int           `json:"qty"`
}

func lines(items []domain.LineItem) []line {
	merged := map[domain.ItemID]int{}
	for _, li := range items {
		merged[li.ItemID] += li.Qty
	}
	out := make([]line, 0, len(merged))
	for id, qty := range merged {
		out = append(out, line{ItemID: id, Qty: qty})
	}
	slices.SortFunc(out, func(a, b line) int { return cmp.Compare(a.ItemID, b.ItemID) })
	return out
}

func PlaceOrderRequest(restaurantID domain.RestaurantID, items []domain.LineItem, cartMode string) Params {
	return newParams(domain.PlaceOrder, struct {
		RestaurantID domain.RestaurantID `json:"restaurant_id"`
		Items        []line              `json:"items"`
		CartMode     string              `json:"cart_mode"`
	}{restaurantID, lines(items), cartMode})
}

func PlaceOrderResolved(o PlaceOrder) Params {
	return newParams(domain.PlaceOrder, struct {
		RestaurantID     domain.RestaurantID `json:"restaurant_id"`
		Items            []line              `json:"items"`
		SubtotalKurus    domain.Money        `json:"subtotal_kurus"`
		DeliveryFeeKurus domain.Money        `json:"delivery_fee_kurus"`
		TotalKurus       domain.Money        `json:"total_kurus"`
		ClearsCart       bool                `json:"clears_cart"`
		WalletKurus      domain.Money        `json:"wallet_kurus"`
		CardKurus        domain.Money        `json:"card_kurus"`
	}{o.RestaurantID, lines(o.Items), o.Subtotal, o.DeliveryFee, o.Total, o.ClearsCart, o.Paid.Wallet, o.Paid.Card})
}

func CancelOrderParams(orderID domain.OrderID) Params {
	return newParams(domain.CancelOrder, struct {
		OrderID domain.OrderID `json:"order_id"`
	}{orderID})
}

func AddTipRequest(orderID domain.OrderID, amount domain.Money) Params {
	return newParams(domain.AddTip, struct {
		OrderID     domain.OrderID `json:"order_id"`
		AmountKurus domain.Money   `json:"amount_kurus"`
	}{orderID, amount})
}

func AddTipResolved(orderID domain.OrderID, amount domain.Money, paid domain.Paid) Params {
	return newParams(domain.AddTip, struct {
		OrderID     domain.OrderID `json:"order_id"`
		AmountKurus domain.Money   `json:"amount_kurus"`
		WalletKurus domain.Money   `json:"wallet_kurus"`
		CardKurus   domain.Money   `json:"card_kurus"`
	}{orderID, amount, paid.Wallet, paid.Card})
}

func newParams(action domain.Action, v any) Params {
	canonical, err := json.Marshal(v)
	if err != nil {
		panic("confirm: params are not encodable: " + err.Error())
	}
	return Params{action: action, canonical: canonical}
}
