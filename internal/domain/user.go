package domain

import (
	"errors"
	"fmt"
)

var errInsufficientBalance = errors.New("insufficient wallet balance")

type UserID string

type User struct {
	ID          UserID
	DisplayName string
	WalletBalance Money
	HasSavedCard  bool
	AgeVerified   bool
	Address       string
	District      string
}

func (u *User) DebitWallet(amount Money) error {
	if amount < 0 {
		return fmt.Errorf("debit of a negative amount %v", amount)
	}
	if amount > u.WalletBalance {
		return fmt.Errorf("%w: %v requested, %v available", errInsufficientBalance, amount, u.WalletBalance)
	}
	u.WalletBalance -= amount
	return nil
}

func (u *User) CreditWallet(amount Money) error {
	if amount < 0 {
		return fmt.Errorf("credit of a negative amount %v", amount)
	}
	u.WalletBalance += amount
	return nil
}
