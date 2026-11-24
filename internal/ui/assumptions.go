package ui

import (
	"fmt"
	"strings"

	"sofra/internal/action"
)

func AssumptionTexts(o action.Outcome) []string {
	a := o.Cart
	if a == nil {
		return nil
	}
	switch a.Kind {
	case action.CartIncluded:
		items := make([]string, len(a.Lines))
		var summed []string
		for i, l := range a.Lines {
			items[i] = fmt.Sprintf("%s ×%d", l.Name, l.InCart)
			if l.Requested > 0 {
				summed = append(summed, fmt.Sprintf("%s: %d in the cart + %d requested = %d", l.Name, l.InCart, l.Requested, l.InCart+l.Requested))
			}
		}
		if a.CartOnly {
			return []string{fmt.Sprintf("This order is the current contents of your %s cart: %s.", a.CartRestaurant, strings.Join(items, ", "))}
		}
		texts := []string{fmt.Sprintf("Your %s cart already holds %s; they are included in this order, and the cart is emptied when it is placed.",
			a.CartRestaurant, strings.Join(items, ", "))}
		if len(summed) > 0 {
			texts = append(texts, "Quantities of the same item are added: "+strings.Join(summed, "; ")+".")
		}
		return texts
	case action.OtherCartKept:
		return []string{fmt.Sprintf("Your %s cart is kept as it is; this order contains only the items requested from %s.", a.CartRestaurant, o.Restaurant)}
	case action.CartKept:
		return []string{fmt.Sprintf("Your %s cart is kept as it is; this order contains only the items you named.", a.CartRestaurant)}
	default:
		panic(fmt.Sprintf("ui: cart assumption %q has no wording", a.Kind))
	}
}

func RefundText(r action.Receipt) string {
	f := r.Refund
	if f == nil {
		return ""
	}
	var parts []string
	if f.OrderUnrecorded {
		parts = append(parts, fmt.Sprintf("The order's %v is refunded by the payments system to the way it was paid; it is not in the figures here.", r.Amount))
	}
	if f.ToWallet > 0 || f.ToCard > 0 {
		what := "Refunded"
		if f.OrderUnrecorded {
			what = "The tips left on it are refunded"
		}
		var to []string
		if f.ToWallet > 0 {
			to = append(to, fmt.Sprintf("%v to the wallet now", f.ToWallet))
		}
		if f.ToCard > 0 {
			to = append(to, fmt.Sprintf("%v to the saved card within 3-5 business days", f.ToCard))
		}
		parts = append(parts, what+": "+strings.Join(to, " and ")+".")
	}
	return strings.Join(parts, " ")
}
