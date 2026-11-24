package domain

import "time"

type TipID string

type Tip struct {
	ID        TipID
	UserID    UserID
	OrderID   OrderID
	Amount    Money
	Paid      Paid
	CreatedAt time.Time
}
