package pricing

import (
	"errors"
	"fmt"

	"sofra/internal/domain"
	"sofra/internal/policy"
)

var ErrInvalidQuantity = errors.New("invalid quantity")

type Line struct {
	Item domain.MenuItem
	Qty  int
}

type QuotedLine struct {
	ItemID    domain.ItemID
	Name      string
	Qty       int
	UnitPrice domain.Money
}

type Quote struct {
	Lines                 []QuotedLine
	Subtotal              domain.Money
	DeliveryFee           domain.Money
	Total                 domain.Money
	MinOrder              domain.Money
	MeetsMinimum          bool
	FreeDeliveryThreshold domain.Money
}

func QuoteOrder(r domain.Restaurant, lines []Line) (Quote, error) {
	if len(lines) == 0 {
		return Quote{}, errors.New("nothing to price: the basket has no items")
	}
	q := Quote{
		Lines:                 make([]QuotedLine, 0, len(lines)),
		MinOrder:              r.MinOrder,
		FreeDeliveryThreshold: policy.FreeDeliveryThreshold,
	}
	for _, l := range lines {
		if l.Qty < 1 || l.Qty > policy.MaxItemQty {
			return Quote{}, fmt.Errorf("%s ×%d: %w, want 1 to %d", l.Item.ID, l.Qty, ErrInvalidQuantity, policy.MaxItemQty)
		}
		lineTotal, err := l.Item.Price.Times(l.Qty)
		if err != nil {
			return Quote{}, err
		}
		q.Lines = append(q.Lines, QuotedLine{
			ItemID:    l.Item.ID,
			Name:      l.Item.Name,
			Qty:       l.Qty,
			UnitPrice: l.Item.Price,
		})
		q.Subtotal += lineTotal
	}

	q.MeetsMinimum = q.Subtotal >= r.MinOrder
	q.DeliveryFee = deliveryFee(q.Subtotal, r.DeliveryFee, policy.FreeDeliveryThreshold)
	q.Total = q.Subtotal + q.DeliveryFee
	return q, nil
}

func (q Quote) MinimumShortfall() domain.Money {
	return max(q.MinOrder-q.Subtotal, 0)
}

func (q Quote) FreeDeliveryShortfall() domain.Money {
	return max(q.FreeDeliveryThreshold-q.Subtotal, 0)
}

func deliveryFee(subtotal, restaurantFee, freeThreshold domain.Money) domain.Money {
	if subtotal >= freeThreshold {
		return 0
	}
	return restaurantFee
}
