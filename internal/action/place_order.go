package action

import (
	"errors"
	"fmt"
	"slices"
	"time"

	"sofra/internal/confirm"
	"sofra/internal/domain"
	"sofra/internal/gate"
	"sofra/internal/store"
)

const (
	orderPrefix = "ord_"
	tipPrefix   = "tip_"
)

type PlaceOrderRequest struct {
	UserID       domain.UserID
	RestaurantID domain.RestaurantID
	Items        []domain.LineItem
	CartMode     CartMode
	Turn         int
}

func (s *Service) PreparePlaceOrder(req PlaceOrderRequest) (Outcome, error) {
	return s.propose(req.UserID, req.Turn, func(r reader) (proposal, error) { return evaluatePlaceOrder(r, req) })
}

func (s *Service) ConfirmPlaceOrderInChat(req PlaceOrderRequest) (Outcome, error) {
	return s.confirmInChat(req.UserID, domain.PlaceOrder, req.Turn,
		func(r reader) (proposal, error) { return evaluatePlaceOrder(r, req) })
}

func evaluatePlaceOrder(r reader, req PlaceOrderRequest) (proposal, error) {
	if err := validateRequested(req.Items); err != nil {
		return invalidRequest(err.Error()), nil
	}
	user, err := r.User(req.UserID)
	if err != nil {
		return proposal{}, err
	}
	cart, err := r.Cart(req.UserID)
	if err != nil {
		return proposal{}, err
	}
	restaurant, err := r.Restaurant(req.RestaurantID)
	if errors.Is(err, store.ErrNotFound) {
		return invalidRequest(fmt.Sprintf("No restaurant %s.", req.RestaurantID)), nil
	}
	if err != nil {
		return proposal{}, err
	}

	b := buildBasket(cart, req.RestaurantID, req.Items, req.CartMode)
	res := gate.EvaluatePlaceOrder(gate.PlaceOrderRequest{User: user, Restaurant: restaurant, Items: b.items})
	if res.Violation != nil {
		return proposal{stop: ptr(blockedOrder(res))}, nil
	}

	var otherName string
	if !cart.IsEmpty() && cart.RestaurantID != restaurant.ID {
		other, err := r.Restaurant(cart.RestaurantID)
		if err != nil {
			return proposal{}, err
		}
		otherName = other.Name
	}
	quote, plan := res.Quote, res.Payment
	return proposal{
		outcome: Outcome{Action: domain.PlaceOrder, Restaurant: restaurant.Name, Quote: &quote, Payment: plan,
			Cart: cartAssumption(restaurant, cart, otherName, req.Items, b)},
		approved: confirm.Approved{
			Request: confirm.PlaceOrderRequest(req.RestaurantID, req.Items, string(req.CartMode)),
			Resolved: confirm.PlaceOrderResolved(confirm.PlaceOrder{
				RestaurantID: restaurant.ID, Items: b.items,
				Subtotal: quote.Subtotal, DeliveryFee: quote.DeliveryFee, Total: quote.Total,
				ClearsCart: b.includesCart, Paid: plan.Paid(),
			}),
			Snapshot: PlaceOrderRequest{UserID: req.UserID, RestaurantID: req.RestaurantID,
				Items: slices.Clone(req.Items), CartMode: req.CartMode},
		},
		apply: func(tx *store.Tx, a confirm.Approval, now time.Time) (*Receipt, error) {
			order := domain.Order{
				ID:             domain.OrderID(a.DerivedID(orderPrefix)),
				UserID:         user.ID,
				Date:           now,
				RestaurantName: restaurant.Name,
				RestaurantID:   restaurant.ID,
				Items:          slices.Clone(b.items),
				Total:          quote.Total,
				Status:         domain.OrderReceived,
				Paid:           ptr(plan.Paid()),
			}
			if err := user.DebitWallet(plan.FromWallet); err != nil {
				return nil, err
			}
			if err := errors.Join(tx.SaveUser(user), tx.CreateOrder(order)); err != nil {
				return nil, err
			}
			if b.includesCart {
				if err := tx.SaveCart(user.ID, domain.Cart{}); err != nil {
					return nil, err
				}
			}
			return &Receipt{
				Event: EventPlaced, OrderID: order.ID, RestaurantName: order.RestaurantName,
				Amount: order.Total, OrderStatus: order.Status, Payment: plan,
			}, nil
		},
	}, nil
}

func invalidRequest(detail string) proposal {
	v := &gate.Violation{Requirement: gate.InvalidRequest, Detail: detail}
	return proposal{stop: ptr(stopped(domain.PlaceOrder, v))}
}

func blockedOrder(res gate.PlaceOrderResult) Outcome {
	out := stopped(domain.PlaceOrder, res.Violation)

	if r := res.Violation.Requirement; r == gate.MinOrder || r == gate.SufficientFunds {
		out.Quote = &res.Quote
	}
	return out
}

func ptr[T any](v T) *T { return &v }

func deref(o *Outcome) Outcome {
	if o == nil {
		return Outcome{}
	}
	return *o
}
