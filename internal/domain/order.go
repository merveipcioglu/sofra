package domain

import (
	"errors"
	"fmt"
	"time"
)

var ErrInvalidTransition = errors.New("invalid state transition")

type OrderID string

type OrderStatus string

const (
	OrderReceived  OrderStatus = "received"
	OrderDelivered OrderStatus = "delivered"
	OrderCancelled OrderStatus = "cancelled"
)

func ParseOrderStatus(s string) (OrderStatus, error) {
	switch status := OrderStatus(s); status {
	case OrderReceived, OrderDelivered, OrderCancelled:
		return status, nil
	default:
		return "", fmt.Errorf("unknown order status %q", s)
	}
}

type Order struct {
	ID             OrderID
	UserID         UserID
	Date           time.Time
	RestaurantName string
	RestaurantID   RestaurantID
	Items          []LineItem
	Total          Money
	Status         OrderStatus
	Note string
	Paid *Paid
}

type Paid struct {
	Wallet Money
	Card   Money
}

func (o Order) Cancellable() bool {
	return o.Status == OrderReceived
}

func (o *Order) Cancel() error {
	if !o.Cancellable() {
		return fmt.Errorf("%w: order %s is %s", ErrInvalidTransition, o.ID, o.Status)
	}
	o.Status = OrderCancelled
	return nil
}
