package store

import (
	"errors"
	"math"
	"testing"

	"sofra/internal/domain"
)

func loaded(t *testing.T) *Store {
	t.Helper()
	s, err := Load("../../data")
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestCreateRefusesDuplicateIDs(t *testing.T) {
	s := loaded(t)
	order := domain.Order{ID: "ord_x", UserID: "u_ok", Status: domain.OrderReceived}
	tip := domain.Tip{ID: "tip_x", UserID: "u_ok", OrderID: "u_ok_o9", Amount: tl(20)}
	for name, create := range map[string]func(*Tx) error{
		"order": func(tx *Tx) error { return tx.CreateOrder(order) },
		"tip":   func(tx *Tx) error { return tx.CreateTip(tip) },
	} {
		if err := s.Transact(create); err != nil {
			t.Fatalf("%s: first create: %v", name, err)
		}
		if err := s.Transact(create); !errors.Is(err, ErrDuplicate) {
			t.Errorf("%s: second create: %v, want ErrDuplicate", name, err)
		}
	}
	other := tip
	other.UserID = "u_new"
	if err := s.Transact(func(tx *Tx) error { return tx.CreateTip(other) }); !errors.Is(err, ErrDuplicate) {
		t.Errorf("tip id reused by another user: %v, want ErrDuplicate", err)
	}
}

func TestReadsAreCopies(t *testing.T) {
	s := loaded(t)

	cart, err := s.Cart("u_ok")
	if err != nil || cart.IsEmpty() {
		t.Fatalf("u_ok's cart: %v, %+v", err, cart)
	}
	cart.Items[0].Qty = 99
	if again, _ := s.Cart("u_ok"); again.Items[0].Qty == 99 {
		t.Error("changing a read cart changed the store")
	}

	r, err := s.Restaurant("rst_04")
	if err != nil {
		t.Fatal(err)
	}
	r.Menu[0].Price = 0
	if again, _ := s.Restaurant("rst_04"); again.Menu[0].Price == 0 {
		t.Error("changing a read menu changed the store")
	}
}

func TestFailedTransactionWritesNothing(t *testing.T) {
	s := loaded(t)
	before, _ := s.User("u_ok")
	fail := errors.New("fail")
	err := s.Transact(func(tx *Tx) error {
		u, _ := tx.User("u_ok")
		u.WalletBalance = 0
		if err := tx.SaveUser(u); err != nil {
			return err
		}
		if err := tx.CreateTip(domain.Tip{ID: "tip_y", UserID: "u_ok", OrderID: "u_ok_o9", Amount: tl(20)}); err != nil {
			return err
		}
		return fail
	})
	if !errors.Is(err, fail) {
		t.Fatalf("Transact: %v", err)
	}
	if u, _ := s.User("u_ok"); u.WalletBalance != before.WalletBalance {
		t.Errorf("wallet %v, want %v: the failed transaction's write was kept", u.WalletBalance, before.WalletBalance)
	}
	if tips, _ := s.Tips("u_ok"); len(tips) != 0 {
		t.Errorf("tips %v, want none: the failed transaction's tip was kept", tips)
	}
}

func tl(lira float64) domain.Money { return domain.Money(math.Round(lira * 100)) }
