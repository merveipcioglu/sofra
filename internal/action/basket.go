package action

import (
	"fmt"
	"slices"

	"sofra/internal/domain"
)

type CartMode string

const (
	CartAuto        CartMode = ""
	CartRequestOnly CartMode = "request_only"
)

type basket struct {
	items        []domain.LineItem
	includesCart bool
}

func validateRequested(requested []domain.LineItem) error {
	for _, li := range requested {
		if li.Qty < 1 {
			return fmt.Errorf("quantity %d for %s: each requested item needs a quantity of at least 1; items cannot be removed from the cart by ordering", li.Qty, li.ItemID)
		}
	}
	return nil
}

func buildBasket(cart domain.Cart, restaurantID domain.RestaurantID, requested []domain.LineItem, mode CartMode) basket {
	sameRestaurant := !cart.IsEmpty() && cart.RestaurantID == restaurantID
	switch {
	case len(requested) == 0:
		if sameRestaurant {
			return basket{items: slices.Clone(cart.Items), includesCart: true}
		}
		return basket{}
	case sameRestaurant && mode != CartRequestOnly:
		return basket{items: merge(cart.Items, requested), includesCart: true}
	default:
		return basket{items: merge(nil, requested)}
	}
}

func merge(cart, requested []domain.LineItem) []domain.LineItem {
	out := slices.Clone(cart)
	for _, li := range requested {
		if i := slices.IndexFunc(out, func(x domain.LineItem) bool { return x.ItemID == li.ItemID }); i >= 0 {
			out[i].Qty += li.Qty
		} else {
			out = append(out, li)
		}
	}
	return out
}

type CartAssumption struct {
	Kind           CartAssumptionKind
	CartRestaurant string
	Lines          []AssumedLine
	CartOnly       bool
}

type CartAssumptionKind string

const (
	CartIncluded  CartAssumptionKind = "cart_included"
	OtherCartKept CartAssumptionKind = "other_cart_kept"
	CartKept      CartAssumptionKind = "cart_kept"
)

type AssumedLine struct {
	Name      string
	InCart    int
	Requested int
}

func cartAssumption(r domain.Restaurant, cart domain.Cart, otherName string, requested []domain.LineItem, b basket) *CartAssumption {
	switch {
	case b.includesCart:
		a := &CartAssumption{Kind: CartIncluded, CartRestaurant: r.Name, CartOnly: len(requested) == 0}
		for _, li := range cart.Items {
			item, _ := r.MenuItem(li.ItemID)
			line := AssumedLine{Name: item.Name, InCart: li.Qty}
			for _, req := range requested {
				if req.ItemID == li.ItemID {
					line.Requested += req.Qty
				}
			}
			a.Lines = append(a.Lines, line)
		}
		return a
	case cart.IsEmpty():
		return nil
	case cart.RestaurantID != r.ID:
		return &CartAssumption{Kind: OtherCartKept, CartRestaurant: otherName}
	default:
		return &CartAssumption{Kind: CartKept, CartRestaurant: r.Name}
	}
}
