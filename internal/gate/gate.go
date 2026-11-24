package gate

import (
	"slices"
)

type Requirement string

const (
	OutOfServiceArea Requirement = "out_of_service_area"
	ItemUnavailable  Requirement = "item_unavailable"
	Age18Plus        Requirement = "age_18_plus"
	MinOrder         Requirement = "min_order"
	SufficientFunds  Requirement = "sufficient_funds"
	NotCancellable   Requirement = "not_cancellable"
	TipWindowExpired Requirement = "tip_window_expired"

	OrderNotFound  Requirement = "order_not_found"
	InvalidAmount  Requirement = "invalid_amount"
	InvalidRequest Requirement = "invalid_request"
)

func Gates() []Requirement {
	return []Requirement{OutOfServiceArea, ItemUnavailable, Age18Plus, MinOrder, SufficientFunds, NotCancellable, TipWindowExpired}
}

func (r Requirement) IsGate() bool { return slices.Contains(Gates(), r) }

type Violation struct {
	Requirement Requirement
	Detail string
}

type check[S any] func(S) *Violation

func run[S any](checks []check[S], state S) *Violation {
	for _, c := range checks {
		if v := c(state); v != nil {
			return v
		}
	}
	return nil
}
