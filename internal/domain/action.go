package domain

import "slices"

type Action string

const (
	PlaceOrder  Action = "place_order"
	CancelOrder Action = "cancel_order"
	AddTip      Action = "add_tip"
)

func Actions() []Action { return []Action{PlaceOrder, CancelOrder, AddTip} }

func (a Action) Valid() bool { return slices.Contains(Actions(), a) }

func (a Action) Spends() bool { return a == PlaceOrder || a == AddTip }
