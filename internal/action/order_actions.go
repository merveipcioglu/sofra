package action

import (
	"errors"
	"time"

	"sofra/internal/confirm"
	"sofra/internal/domain"
	"sofra/internal/gate"
	"sofra/internal/store"
)

type CancelOrderRequest struct {
	UserID  domain.UserID
	OrderID domain.OrderID
	Turn    int
}

func (s *Service) PrepareCancelOrder(req CancelOrderRequest) (Outcome, error) {
	return s.propose(req.UserID, req.Turn, req)
}

func (s *Service) ConfirmCancelOrderInChat(req CancelOrderRequest) (Outcome, error) {
	return s.confirmInChat(req.UserID, domain.CancelOrder, req.Turn, req)
}

func (req CancelOrderRequest) usedID(confirm.Approval) string { return string(req.OrderID) }

func refundFor(o domain.Order, tips []domain.Tip) Refund {
	var r Refund
	if o.Paid != nil {
		r.ToWallet, r.ToCard = o.Paid.Wallet, o.Paid.Card
	} else {
		r.OrderUnrecorded = true
	}
	for _, t := range tips {
		if t.OrderID == o.ID {
			r.ToWallet += t.Paid.Wallet
			r.ToCard += t.Paid.Card
		}
	}
	return r
}

func (req CancelOrderRequest) evaluate(r reader, _ time.Time) (proposal, error) {
	order, err := findOrder(r, req.UserID, req.OrderID)
	if err != nil {
		return proposal{}, err
	}
	if v := gate.EvaluateCancelOrder(gate.CancelOrderRequest{Order: order}); v != nil {
		return proposal{stop: ptr(stopped(domain.CancelOrder, v))}, nil
	}
	params := confirm.CancelOrderParams(req.OrderID)
	return proposal{
		outcome:  Outcome{Action: domain.CancelOrder, Restaurant: order.RestaurantName},
		approved: confirm.Approved{Request: params, Resolved: params},
		request:  CancelOrderRequest{UserID: req.UserID, OrderID: req.OrderID},
		apply: func(tx *store.Tx, _ confirm.Approval, _ time.Time) (*Receipt, error) {
			cancelled := *order
			if err := cancelled.Cancel(); err != nil {
				return nil, err
			}
			user, err := tx.User(req.UserID)
			if err != nil {
				return nil, err
			}
			tips, err := tx.Tips(req.UserID)
			if err != nil {
				return nil, err
			}
			refund := refundFor(cancelled, tips)
			if err := user.CreditWallet(refund.ToWallet); err != nil {
				return nil, err
			}
			if err := errors.Join(tx.SaveOrder(cancelled), tx.SaveUser(user)); err != nil {
				return nil, err
			}
			return &Receipt{
				Event: EventCancelled, OrderID: cancelled.ID, RestaurantName: cancelled.RestaurantName,
				Amount: cancelled.Total, OrderStatus: cancelled.Status, Refund: &refund,
			}, nil
		},
	}, nil
}

type AddTipRequest struct {
	UserID  domain.UserID
	OrderID domain.OrderID
	Amount  domain.Money
	Turn    int
}

func (s *Service) PrepareAddTip(req AddTipRequest) (Outcome, error) {
	return s.propose(req.UserID, req.Turn, req)
}

func (s *Service) ConfirmAddTipInChat(req AddTipRequest) (Outcome, error) {
	return s.confirmInChat(req.UserID, domain.AddTip, req.Turn, req)
}

func (req AddTipRequest) usedID(a confirm.Approval) string { return a.DerivedID(tipPrefix) }

func (req AddTipRequest) evaluate(r reader, now time.Time) (proposal, error) {
	user, err := r.User(req.UserID)
	if err != nil {
		return proposal{}, err
	}
	order, err := findOrder(r, req.UserID, req.OrderID)
	if err != nil {
		return proposal{}, err
	}
	priorTips, err := r.Tips(req.UserID)
	if err != nil {
		return proposal{}, err
	}
	res := gate.EvaluateAddTip(gate.AddTipRequest{
		User: user, Order: order, PriorTips: priorTips, Amount: req.Amount, Now: now,
	})
	if res.Violation != nil {
		return proposal{stop: ptr(stopped(domain.AddTip, res.Violation))}, nil
	}
	plan := res.Payment
	return proposal{
		outcome: Outcome{Action: domain.AddTip, Restaurant: order.RestaurantName, Payment: plan},
		approved: confirm.Approved{
			Request:  confirm.AddTipRequest(req.OrderID, req.Amount),
			Resolved: confirm.AddTipResolved(req.OrderID, req.Amount, plan.Paid()),
		},
		request: AddTipRequest{UserID: req.UserID, OrderID: req.OrderID, Amount: req.Amount},
		apply: func(tx *store.Tx, a confirm.Approval, now time.Time) (*Receipt, error) {
			if err := user.DebitWallet(plan.FromWallet); err != nil {
				return nil, err
			}
			tip := domain.Tip{
				ID:        domain.TipID(a.DerivedID(tipPrefix)),
				UserID:    user.ID,
				OrderID:   order.ID,
				Amount:    req.Amount,
				Paid:      plan.Paid(),
				CreatedAt: now,
			}
			if err := errors.Join(tx.SaveUser(user), tx.CreateTip(tip)); err != nil {
				return nil, err
			}
			return &Receipt{
				Event: EventTipAdded, OrderID: order.ID, RestaurantName: order.RestaurantName,
				Amount: tip.Amount, OrderStatus: order.Status, Payment: plan,
			}, nil
		},
	}, nil
}
