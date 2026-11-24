//	Propose:  gates → issue an approval; nothing runs yet
//	Commit:   in one store transaction: re-check against current state
//	          → gates → same cost as approved → execute → use up
//	          approval → commit
//
// Approval comes from chat  or the confirmation screen.
package action

import (
	"errors"
	"fmt"
	"time"

	"sofra/internal/clock"
	"sofra/internal/confirm"
	"sofra/internal/domain"
	"sofra/internal/gate"
	"sofra/internal/policy"
	"sofra/internal/pricing"
	"sofra/internal/store"
)

type Status string

const (
	Blocked           Status = "blocked"
	Invalid           Status = "invalid"
	NeedsConfirmation Status = "needs_confirmation"
	Executed          Status = "executed"
	Rejected          Status = "rejected"
)

type Event string

const (
	EventPlaced    Event = "placed"
	EventCancelled Event = "cancelled"
	EventTipAdded  Event = "tip_added"
)

type Outcome struct {
	Action     domain.Action
	Status     Status
	Restaurant string

	Violation *gate.Violation
	Rejection confirm.Reason
	Used      string
	Token     *confirm.Token
	Request   any

	Quote   *pricing.Quote
	Payment pricing.PaymentPlan
	Cart    *CartAssumption
	Receipt *Receipt
}

type Receipt struct {
	Event          Event
	OrderID        domain.OrderID
	RestaurantName string
	Amount         domain.Money
	OrderStatus    domain.OrderStatus
	Payment        pricing.PaymentPlan
	Refund         *Refund
}

type Refund struct {
	ToWallet        domain.Money
	ToCard          domain.Money
	OrderUnrecorded bool
}

type Service struct {
	store  *store.Store
	tokens *confirm.Service
	clock  clock.Clock
}

func NewService(s *store.Store, tokens *confirm.Service, clk clock.Clock) *Service {
	return &Service{store: s, tokens: tokens, clock: clk}
}

type reader interface {
	User(domain.UserID) (domain.User, error)
	Cart(domain.UserID) (domain.Cart, error)
	Order(domain.UserID, domain.OrderID) (domain.Order, error)
	Restaurant(domain.RestaurantID) (domain.Restaurant, error)
	Tips(domain.UserID) ([]domain.Tip, error)
}

var errNoCommit = errors.New("action not executed")

type proposal struct {
	stop     *Outcome
	outcome  Outcome
	approved confirm.Approved
	apply    func(tx *store.Tx, a confirm.Approval, now time.Time) (*Receipt, error)
}

type evaluator func(r reader) (proposal, error)

func (s *Service) propose(userID domain.UserID, turn int, eval evaluator) (Outcome, error) {
	p, err := eval(s.store)
	if err != nil || p.stop != nil {
		return deref(p.stop), err
	}
	return s.issue(p, userID, turn, "")
}

func (s *Service) issue(p proposal, userID domain.UserID, turn int, rejection confirm.Reason) (Outcome, error) {
	tok, err := s.tokens.Issue(userID, p.approved, policy.TokenTTL, turn)
	if err != nil {
		return Outcome{}, err
	}
	out := p.outcome
	out.Status, out.Token, out.Rejection, out.Request = NeedsConfirmation, &tok, rejection, p.approved.Snapshot
	return out, nil
}

func (s *Service) confirmInChat(userID domain.UserID, action domain.Action, turn int, asked evaluator) (Outcome, error) {
	return s.transact(func(tx *store.Tx) (Outcome, confirm.Approval, error) {
		var none confirm.Approval
		p, err := asked(tx)
		if err != nil || p.stop != nil {
			return deref(p.stop), none, err
		}
		approval, err := s.tokens.Latest(userID, action, turn)
		reason, rejected := confirm.ReasonOf(err)
		if err != nil && !rejected {
			return Outcome{}, none, err
		}
		if !rejected && !approval.AsksFor(p.approved.Request) {
			reason, rejected = confirm.ParamsMismatch, true
		}
		if rejected {
			out, err := s.issue(p, userID, turn, reason)
			return out, none, err
		}
		return s.commit(tx, approval, userID, turn)
	})
}

func (s *Service) ConfirmOnScreen(userID domain.UserID, token string, turn int) (Outcome, error) {
	return s.transact(func(tx *store.Tx) (Outcome, confirm.Approval, error) {
		var none confirm.Approval
		approval, err := s.tokens.Verify(userID, token, turn)
		if err == nil {
			return s.commit(tx, approval, userID, turn)
		}
		reason, ok := confirm.ReasonOf(err)
		if !ok {
			return Outcome{}, none, err
		}
		if reason != confirm.Expired {
			return rejected(approval, reason), none, nil
		}
		eval, err := s.evaluator(approval.Snapshot())
		if err != nil {
			return Outcome{}, none, err
		}
		p, err := eval(tx)
		if err != nil || p.stop != nil {
			return deref(p.stop), none, err
		}
		out, err := s.issue(p, userID, turn, reason)
		return out, none, err
	})
}

func (s *Service) commit(tx *store.Tx, approval confirm.Approval, userID domain.UserID, turn int) (Outcome, confirm.Approval, error) {
	var none confirm.Approval
	eval, err := s.evaluator(approval.Snapshot())
	if err != nil {
		return Outcome{}, none, err
	}
	p, err := eval(tx)
	if err != nil || p.stop != nil {
		return deref(p.stop), none, err
	}
	if !approval.AsksFor(p.approved.Request) || !approval.Costs(p.approved.Resolved) {
		out, err := s.issue(p, userID, turn, confirm.StateChanged)
		return out, none, err
	}
	receipt, err := p.apply(tx, approval, s.clock.Now())
	if err != nil {
		return Outcome{}, none, err
	}
	out := p.outcome
	out.Status, out.Receipt = Executed, receipt
	return out, approval, nil
}

func (s *Service) evaluator(snapshot any) (evaluator, error) {
	switch req := snapshot.(type) {
	case PlaceOrderRequest:
		return func(r reader) (proposal, error) { return evaluatePlaceOrder(r, req) }, nil
	case CancelOrderRequest:
		return func(r reader) (proposal, error) { return evaluateCancelOrder(r, req) }, nil
	case AddTipRequest:
		return func(r reader) (proposal, error) { return evaluateAddTip(r, req, s.clock.Now()) }, nil
	default:
		return nil, fmt.Errorf("approval holds %T, not a request", snapshot)
	}
}

func rejected(a confirm.Approval, reason confirm.Reason) Outcome {
	out := Outcome{Action: a.Action(), Status: Rejected, Rejection: reason}
	if reason == confirm.AlreadyUsed {
		switch req := a.Snapshot().(type) {
		case PlaceOrderRequest:
			out.Used = a.DerivedID(orderPrefix)
		case AddTipRequest:
			out.Used = a.DerivedID(tipPrefix)
		case CancelOrderRequest:
			out.Used = string(req.OrderID)
		}
	}
	return out
}

func (s *Service) transact(fn func(*store.Tx) (Outcome, confirm.Approval, error)) (Outcome, error) {
	var out Outcome
	err := s.store.Transact(func(tx *store.Tx) error {
		var (
			approval confirm.Approval
			err      error
		)
		if out, approval, err = fn(tx); err != nil {
			return err
		}
		if out.Status != Executed {
			return errNoCommit
		}
		return s.tokens.Consume(approval)
	})
	if errors.Is(err, errNoCommit) {
		err = nil
	}
	return out, err
}

func stopped(a domain.Action, v *gate.Violation) Outcome {
	status := Blocked
	if !v.Requirement.IsGate() {
		status = Invalid
	}
	return Outcome{Action: a, Status: status, Violation: v}
}

func findOrder(r reader, userID domain.UserID, orderID domain.OrderID) (*domain.Order, error) {
	o, err := r.Order(userID, orderID)
	if errors.Is(err, store.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("load order: %w", err)
	}
	return &o, nil
}
