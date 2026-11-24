package action

import (
	"encoding/base64"
	"encoding/hex"
	"errors"
	"math"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"sofra/internal/clock"
	"sofra/internal/confirm"
	"sofra/internal/domain"
	"sofra/internal/gate"
	"sofra/internal/store"
)

func newService(t *testing.T, st *store.Store) *Service {
	t.Helper()
	now, err := clock.ParseReference("")
	if err != nil {
		t.Fatal(err)
	}
	clk := clock.NewFixed(now)
	secret, err := confirm.RandomSecret()
	if err != nil {
		t.Fatal(err)
	}
	tokens, err := confirm.NewService(secret, clk)
	if err != nil {
		t.Fatal(err)
	}
	return NewService(st, tokens, clk)
}

func confirmed(t *testing.T, turn *int, prepare func(int) (Outcome, error), execute func(int) (Outcome, error)) Outcome {
	t.Helper()
	*turn++
	out, err := prepare(*turn)
	if err != nil || out.Token == nil {
		t.Fatalf("prepare: %v, %+v", err, out)
	}
	*turn++
	out, err = execute(*turn)
	if err != nil || out.Status != Executed {
		t.Fatalf("execute: %v, %+v", err, out)
	}
	return out
}

func wallet(t *testing.T, st *store.Store) domain.Money {
	t.Helper()
	u, err := st.User("u_ok")
	if err != nil {
		t.Fatal(err)
	}
	return u.WalletBalance
}

func setWallet(t *testing.T, st *store.Store, m domain.Money) {
	t.Helper()
	err := st.Transact(func(tx *store.Tx) error {
		u, err := tx.User("u_ok")
		if err != nil {
			return err
		}
		u.WalletBalance = m
		return tx.SaveUser(u)
	})
	if err != nil {
		t.Fatal(err)
	}
}

func placeAndTip(t *testing.T, s *Service, turn *int, tip domain.Money) domain.OrderID {
	t.Helper()
	order := PlaceOrderRequest{UserID: "u_ok", RestaurantID: burgerStop, Items: []domain.LineItem{{ItemID: cheeseburger, Qty: 2}}}
	placed := confirmed(t, turn,
		func(n int) (Outcome, error) { order.Turn = n; return s.PreparePlaceOrder(order) },
		func(n int) (Outcome, error) { order.Turn = n; return s.ConfirmPlaceOrderInChat(order) })
	id := placed.Receipt.OrderID
	addTip(t, s, turn, id, tip)
	return id
}

func addTip(t *testing.T, s *Service, turn *int, id domain.OrderID, amount domain.Money) {
	t.Helper()
	req := AddTipRequest{UserID: "u_ok", OrderID: id, Amount: amount}
	confirmed(t, turn,
		func(n int) (Outcome, error) { req.Turn = n; return s.PrepareAddTip(req) },
		func(n int) (Outcome, error) { req.Turn = n; return s.ConfirmAddTipInChat(req) })
}

func cancel(t *testing.T, s *Service, turn *int, id domain.OrderID) Refund {
	t.Helper()
	req := CancelOrderRequest{UserID: "u_ok", OrderID: id}
	out := confirmed(t, turn,
		func(n int) (Outcome, error) { req.Turn = n; return s.PrepareCancelOrder(req) },
		func(n int) (Outcome, error) { req.Turn = n; return s.ConfirmCancelOrderInChat(req) })
	if out.Receipt.Refund == nil {
		t.Fatal("cancellation receipt has no refund")
	}
	return *out.Receipt.Refund
}

func TestCancelRefundsOrderAndTips(t *testing.T) {
	cases := []struct {
		name           string
		wallet         domain.Money
		toWallet, card domain.Money
	}{
		{"paid from the wallet", tl(800), tl(590), 0},
		{"split between wallet and card", tl(100), tl(100), tl(490)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			st := loadStore(t)
			setWallet(t, st, c.wallet)
			s, turn := newService(t, st), 0

			id := placeAndTip(t, s, &turn, tl(50))
			r := cancel(t, s, &turn, id)

			if r != (Refund{ToWallet: c.toWallet, ToCard: c.card}) {
				t.Errorf("refund %+v, want wallet %v card %v", r, c.toWallet, c.card)
			}
			if got := wallet(t, st); got != c.wallet {
				t.Errorf("wallet after cancelling %v, want it back at %v", got, c.wallet)
			}
		})
	}
}

func TestCancelSeedOrderRefundsTipsOnly(t *testing.T) {
	st := loadStore(t)
	s, turn := newService(t, st), 0
	before := wallet(t, st)

	addTip(t, s, &turn, "u_ok_o9", tl(20))
	r := cancel(t, s, &turn, "u_ok_o9")

	if r != (Refund{ToWallet: tl(20), OrderUnrecorded: true}) {
		t.Errorf("refund %+v, want the 20 TL tip and the order left to the payments system", r)
	}
	if got := wallet(t, st); got != before {
		t.Errorf("wallet %v, want %v", got, before)
	}
}

func TestApprovalBindsPaymentSplit(t *testing.T) {
	order := PlaceOrderRequest{UserID: "u_ok", RestaurantID: burgerStop, Items: []domain.LineItem{{ItemID: cheeseburger, Qty: 2}}}
	tip := AddTipRequest{UserID: "u_ok", OrderID: "u_ok_o9", Amount: tl(50)}
	cases := []struct {
		name    string
		prepare func(*Service, int) (Outcome, error)
		execute func(*Service, int) (Outcome, error)
	}{
		{"place_order",
			func(s *Service, n int) (Outcome, error) { order.Turn = n; return s.PreparePlaceOrder(order) },
			func(s *Service, n int) (Outcome, error) {
				order.Turn = n
				return s.ConfirmPlaceOrderInChat(order)
			}},
		{"add_tip",
			func(s *Service, n int) (Outcome, error) { tip.Turn = n; return s.PrepareAddTip(tip) },
			func(s *Service, n int) (Outcome, error) { tip.Turn = n; return s.ConfirmAddTipInChat(tip) }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			st := loadStore(t)
			s := newService(t, st)
			prepared, err := c.prepare(s, 1)
			if err != nil || prepared.Token == nil || prepared.Payment.FromCard != 0 {
				t.Fatalf("prepare: %v, %+v; want a token and the whole amount from the wallet", err, prepared)
			}

			setWallet(t, st, tl(20))
			out, err := c.execute(s, 2)
			if err != nil {
				t.Fatal(err)
			}
			if out.Status != NeedsConfirmation || out.Rejection != confirm.StateChanged || out.Token == nil {
				t.Errorf("got status %s rejection %q token %v; want a fresh confirmation after state_changed",
					out.Status, out.Rejection, out.Token != nil)
			}
			if out.Payment.FromCard == 0 {
				t.Errorf("fresh confirmation shows payment %+v, want the new card share", out.Payment)
			}
			if got := wallet(t, st); got != tl(20) {
				t.Errorf("wallet %v, want 20.00 TL: nothing may be collected", got)
			}
		})
	}
}

func TestChatConfirmationMustAskForTheApproved(t *testing.T) {
	st := loadStore(t)
	s := newService(t, st)
	two := PlaceOrderRequest{UserID: "u_ok", RestaurantID: burgerStop, Items: []domain.LineItem{{ItemID: cheeseburger, Qty: 2}}, Turn: 1}
	prepared, err := s.PreparePlaceOrder(two)
	if err != nil || prepared.Token == nil {
		t.Fatalf("prepare: %v, %+v", err, prepared)
	}
	before := wallet(t, st)

	five := two
	five.Items, five.Turn = []domain.LineItem{{ItemID: cheeseburger, Qty: 5}}, 2
	out, err := s.ConfirmPlaceOrderInChat(five)
	if err != nil {
		t.Fatal(err)
	}
	if out.Status != NeedsConfirmation || out.Rejection != confirm.ParamsMismatch || out.Token == nil {
		t.Fatalf("got status %s rejection %q; want a fresh confirmation after params_mismatch", out.Status, out.Rejection)
	}
	if out.Quote.Total == prepared.Quote.Total {
		t.Errorf("the fresh confirmation is not for 5: total %v", out.Quote.Total)
	}
	if _, err := s.tokens.Verify("u_ok", prepared.Token.Value, 3); err == nil {
		t.Error("the approval for 2 still works after the user asked for 5")
	}
	if got := wallet(t, st); got != before {
		t.Errorf("wallet %v, want %v: nothing may be collected", got, before)
	}
}

func TestCartChangedAfterApproval(t *testing.T) {
	st := loadStore(t)
	s := newService(t, st)
	req := PlaceOrderRequest{UserID: "u_ok", RestaurantID: burgerStop, Items: []domain.LineItem{{ItemID: cheeseburger, Qty: 2}}, Turn: 1}
	prepared, err := s.PreparePlaceOrder(req)
	if err != nil || prepared.Cart == nil || prepared.Cart.Kind != CartIncluded {
		t.Fatalf("prepare: %v, %+v; want the cart included", err, prepared)
	}
	changed := domain.Cart{RestaurantID: burgerStop, Items: []domain.LineItem{{ItemID: cheeseburger, Qty: 1}}}
	if err := st.Transact(func(tx *store.Tx) error { return tx.SaveCart("u_ok", changed) }); err != nil {
		t.Fatal(err)
	}

	req.Turn = 2
	out, err := s.ConfirmPlaceOrderInChat(req)
	if err != nil {
		t.Fatal(err)
	}
	if out.Status != NeedsConfirmation || out.Rejection != confirm.StateChanged {
		t.Fatalf("got status %s rejection %q; want a fresh confirmation after state_changed", out.Status, out.Rejection)
	}
	if cart, _ := st.Cart("u_ok"); len(cart.Items) != 1 {
		t.Errorf("cart %+v, want it left as it is", cart)
	}
}

func TestGateClosedBeforeConfirmation(t *testing.T) {
	st := loadStore(t)
	s := newService(t, st)
	req := CancelOrderRequest{UserID: "u_ok", OrderID: "u_ok_o9", Turn: 1}
	if prepared, err := s.PrepareCancelOrder(req); err != nil || prepared.Token == nil {
		t.Fatalf("prepare: %v, %+v", err, prepared)
	}
	err := st.Transact(func(tx *store.Tx) error {
		o, err := tx.Order("u_ok", "u_ok_o9")
		if err != nil {
			return err
		}
		o.Status = domain.OrderDelivered
		return tx.SaveOrder(o)
	})
	if err != nil {
		t.Fatal(err)
	}

	req.Turn = 2
	out, err := s.ConfirmCancelOrderInChat(req)
	if err != nil {
		t.Fatal(err)
	}
	if out.Status != Blocked || out.Violation.Requirement != gate.NotCancellable || out.Token != nil {
		t.Errorf("got status %s token %v; want blocked by not_cancellable, no confirmation", out.Status, out.Token != nil)
	}
}

func TestConfirmOnScreen(t *testing.T) {
	order := PlaceOrderRequest{UserID: "u_ok", RestaurantID: burgerStop, Items: []domain.LineItem{{ItemID: cheeseburger, Qty: 2}}, Turn: 1}
	type result struct {
		out      Outcome
		prepared Outcome
		st       *store.Store
		s        *Service
	}
	cases := []struct {
		name      string
		confirm   func(t *testing.T, s *Service, st *store.Store, tok string) (Outcome, error)
		status    Status
		rejection confirm.Reason
		fresh     bool
		orders    int
		check     func(t *testing.T, r result)
	}{
		{"valid: the approved copy executes", func(t *testing.T, s *Service, _ *store.Store, tok string) (Outcome, error) {
			return s.ConfirmOnScreen("u_ok", tok, 2)
		}, Executed, "", false, 1, func(t *testing.T, r result) {
			if r.out.Receipt.Amount != r.prepared.Quote.Total || r.out.Receipt.Amount != tl(540) {
				t.Errorf("charged %v, want the approved %v", r.out.Receipt.Amount, r.prepared.Quote.Total)
			}
			if cart, _ := r.st.Cart("u_ok"); !cart.IsEmpty() {
				t.Errorf("cart %+v, want it emptied as approved", cart)
			}
		}},
		{"fabricated token (prompt 15)", func(t *testing.T, s *Service, _ *store.Store, _ string) (Outcome, error) {
			return s.ConfirmOnScreen("u_ok", "abc123", 2)
		}, Rejected, confirm.Malformed, false, 0, nil},
		{"another user's token", func(t *testing.T, s *Service, _ *store.Store, tok string) (Outcome, error) {
			return s.ConfirmOnScreen("u_new", tok, 2)
		}, Rejected, confirm.WrongUser, false, 0, nil},
		{"pressed twice (prompt 14)", func(t *testing.T, s *Service, _ *store.Store, tok string) (Outcome, error) {
			first, err := s.ConfirmOnScreen("u_ok", tok, 2)
			if err != nil || first.Status != Executed {
				t.Fatalf("first press: %v, %+v", err, first)
			}
			second, err := s.ConfirmOnScreen("u_ok", tok, 3)
			if second.Used != string(first.Receipt.OrderID) {
				t.Errorf("used %q, want the order the first press placed, %s", second.Used, first.Receipt.OrderID)
			}
			return second, err
		}, Rejected, confirm.AlreadyUsed, false, 1, nil},
		{"replaced by a newer confirmation", func(t *testing.T, s *Service, _ *store.Store, tok string) (Outcome, error) {
			newer := order
			newer.Items, newer.Turn = []domain.LineItem{{ItemID: cheeseburger, Qty: 3}}, 2
			if _, err := s.PreparePlaceOrder(newer); err != nil {
				t.Fatal(err)
			}
			return s.ConfirmOnScreen("u_ok", tok, 3)
		}, Rejected, confirm.Superseded, false, 0, func(t *testing.T, r result) {
			if _, err := r.s.tokens.Latest("u_ok", domain.PlaceOrder, 4); err != nil {
				t.Errorf("the newer confirmation was revoked: %v", err)
			}
		}},
		{"expired (prompt 17)", func(t *testing.T, s *Service, _ *store.Store, tok string) (Outcome, error) {
			s.clock.(*clock.Fixed).Advance(6 * time.Minute)
			return s.ConfirmOnScreen("u_ok", tok, 2)
		}, NeedsConfirmation, confirm.Expired, true, 0, nil},
		{"figures moved: the wallet", func(t *testing.T, s *Service, st *store.Store, tok string) (Outcome, error) {
			setWallet(t, st, tl(20))
			return s.ConfirmOnScreen("u_ok", tok, 2)
		}, NeedsConfirmation, confirm.StateChanged, true, 0, nil},
		{"a gate closed: no wallet balance or card left", func(t *testing.T, s *Service, st *store.Store, tok string) (Outcome, error) {
			setWallet(t, st, 0)
			err := st.Transact(func(tx *store.Tx) error {
				u, err := tx.User("u_ok")
				if err != nil {
					return err
				}
				u.HasSavedCard = false
				return tx.SaveUser(u)
			})
			if err != nil {
				t.Fatal(err)
			}
			return s.ConfirmOnScreen("u_ok", tok, 2)
		}, Blocked, "", false, 0, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			st := loadStore(t)
			s := newService(t, st)
			prepared, err := s.PreparePlaceOrder(order)
			if err != nil || prepared.Token == nil {
				t.Fatalf("prepare: %v, %+v", err, prepared)
			}
			before, _ := st.Orders("u_ok")

			out, err := c.confirm(t, s, st, prepared.Token.Value)
			if err != nil {
				t.Fatal(err)
			}
			if out.Status != c.status || out.Rejection != c.rejection || (out.Token != nil) != c.fresh {
				t.Errorf("got status %s rejection %q fresh %t; want %s %q %t",
					out.Status, out.Rejection, out.Token != nil, c.status, c.rejection, c.fresh)
			}
			if after, _ := st.Orders("u_ok"); len(after)-len(before) != c.orders {
				t.Errorf("%d new orders, want %d", len(after)-len(before), c.orders)
			}
			if c.check != nil {
				c.check(t, result{out: out, prepared: prepared, st: st, s: s})
			}
		})
	}
}

func TestFailedExecutionLeavesApprovalUnspent(t *testing.T) {
	st := loadStore(t)
	s := newService(t, st)
	req := PlaceOrderRequest{UserID: "u_ok", RestaurantID: burgerStop, Items: []domain.LineItem{{ItemID: cheeseburger, Qty: 2}}, Turn: 1}
	prepared, err := s.PreparePlaceOrder(req)
	if err != nil || prepared.Token == nil {
		t.Fatalf("prepare: %v, %+v", err, prepared)
	}

	noncePart, _, _ := strings.Cut(prepared.Token.Value, ".")
	nonce, err := base64.RawURLEncoding.DecodeString(noncePart)
	if err != nil {
		t.Fatal(err)
	}
	taken := domain.Order{ID: domain.OrderID("ord_" + hex.EncodeToString(nonce)), UserID: "u_ok", Status: domain.OrderDelivered}
	if err := st.Transact(func(tx *store.Tx) error { return tx.CreateOrder(taken) }); err != nil {
		t.Fatal(err)
	}
	before := wallet(t, st)

	req.Turn = 2
	if _, err := s.ConfirmPlaceOrderInChat(req); !errors.Is(err, store.ErrDuplicate) {
		t.Fatalf("execute: %v, want the duplicate-order failure", err)
	}
	if got := wallet(t, st); got != before {
		t.Errorf("wallet %v, want %v: the failed execution must roll back", got, before)
	}
	if _, err := s.tokens.Verify("u_ok", prepared.Token.Value, 3); err != nil {
		t.Errorf("the approval was spent by a failed execution: %v", err)
	}
}

func TestPlacedOrderRecordsWhatWasOrdered(t *testing.T) {
	st := loadStore(t)
	s, turn := newService(t, st), 0
	order := PlaceOrderRequest{UserID: "u_ok", RestaurantID: burgerStop, Items: []domain.LineItem{{ItemID: cheeseburger, Qty: 2}}}
	placed := confirmed(t, &turn,
		func(n int) (Outcome, error) { order.Turn = n; return s.PreparePlaceOrder(order) },
		func(n int) (Outcome, error) { order.Turn = n; return s.ConfirmPlaceOrderInChat(order) })

	got, err := st.Order("u_ok", placed.Receipt.OrderID)
	if err != nil {
		t.Fatal(err)
	}
	want := []domain.LineItem{{ItemID: fries, Qty: 1}, {ItemID: "itm_015", Qty: 1}, {ItemID: cheeseburger, Qty: 2}}
	if got.RestaurantID != burgerStop || !slices.Equal(got.Items, want) {
		t.Fatalf("recorded %s %v, want %s %v", got.RestaurantID, got.Items, burgerStop, want)
	}

	got.Items[0].Qty, got.Paid.Wallet = 99, 0
	again, _ := st.Order("u_ok", placed.Receipt.OrderID)
	if again.Items[0].Qty != 1 || again.Paid.Wallet == 0 {
		t.Error("changing a read order changed the store")
	}
}

func TestConcurrentConfirmationsExecuteOnce(t *testing.T) {
	const presses = 20
	for round := range 20 {
		st := loadStore(t)
		s := newService(t, st)
		order := PlaceOrderRequest{UserID: "u_ok", RestaurantID: burgerStop, Items: []domain.LineItem{{ItemID: cheeseburger, Qty: 2}}, Turn: 1}
		prepared, err := s.PreparePlaceOrder(order)
		if err != nil || prepared.Token == nil {
			t.Fatalf("prepare: %v, %+v", err, prepared)
		}
		before, _ := st.Orders("u_ok")
		walletBefore := wallet(t, st)

		var (
			wg       sync.WaitGroup
			mu       sync.Mutex
			executed int
		)
		count := func(out Outcome, err error) {
			defer wg.Done()
			if err != nil {
				t.Errorf("confirm: %v", err)
				return
			}
			if out.Status == Executed {
				mu.Lock()
				executed++
				mu.Unlock()
			}
		}
		chat := order
		chat.Turn = 2
		for range presses {
			wg.Add(2)
			go func() { count(s.ConfirmOnScreen("u_ok", prepared.Token.Value, 2)) }()
			go func() { count(s.ConfirmPlaceOrderInChat(chat)) }()
		}
		wg.Wait()

		after, _ := st.Orders("u_ok")
		if executed != 1 || len(after)-len(before) != 1 {
			t.Fatalf("round %d: %d confirmations executed, %d orders created; want 1 and 1", round, executed, len(after)-len(before))
		}
		if spent := walletBefore - wallet(t, st); spent != prepared.Quote.Total {
			t.Fatalf("round %d: wallet charged %v, want %v once", round, spent, prepared.Quote.Total)
		}
	}
}

func tl(lira float64) domain.Money { return domain.Money(math.Round(lira * 100)) }
