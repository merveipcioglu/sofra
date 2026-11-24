package confirm

import (
	"errors"
	"testing"
	"time"

	"sofra/internal/clock"
	"sofra/internal/domain"
)

const (
	user = domain.UserID("u_ok")
	ttl  = 5 * time.Minute
)

var (
	approved = Approved{Request: CancelOrderParams("u_ok_o9"), Resolved: CancelOrderParams("u_ok_o9"), Snapshot: "o9"}
	other    = Approved{Request: CancelOrderParams("u_ok_o1"), Resolved: CancelOrderParams("u_ok_o1"), Snapshot: "o1"}
)

func newTestService(t *testing.T) (*Service, *clock.Fixed) {
	t.Helper()
	now, err := clock.ParseReference("")
	if err != nil {
		t.Fatal(err)
	}
	clk := clock.NewFixed(now)
	secret, err := RandomSecret()
	if err != nil {
		t.Fatal(err)
	}
	s, err := NewService(secret, clk)
	if err != nil {
		t.Fatal(err)
	}
	return s, clk
}

func issue(t *testing.T, s *Service, a Approved) string {
	t.Helper()
	tok, err := s.Issue(user, a, ttl, 1)
	if err != nil {
		t.Fatal(err)
	}
	return tok.Value
}

func wantReason(t *testing.T, err error, want Reason) {
	t.Helper()
	got, ok := ReasonOf(err)
	if !ok || got != want {
		t.Fatalf("got %v, want rejection %q", err, want)
	}
}

func consume(t *testing.T, s *Service, tok string) {
	t.Helper()
	a, err := s.Verify(user, tok, 2)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Consume(a); err != nil {
		t.Fatal(err)
	}
}

func TestVerifyRejections(t *testing.T) {
	cases := []struct {
		name       string
		want       Reason
		handedBack bool
		attempt    func(t *testing.T, s *Service, clk *clock.Fixed, tok string) (Approval, error)
		after      func(t *testing.T, s *Service, tok string)
	}{
		{"not a token (prompt 15)", Malformed, false,
			func(t *testing.T, s *Service, _ *clock.Fixed, _ string) (Approval, error) {
				return s.Verify(user, "abc123", 2)
			}, nil},
		{"issued by another service", Unknown, false,
			func(t *testing.T, _ *Service, _ *clock.Fixed, _ string) (Approval, error) {
				foreign, _ := newTestService(t)
				s, _ := newTestService(t)
				return s.Verify(user, issue(t, foreign, approved), 2)
			}, nil},
		{"signature altered", BadSignature, false,
			func(t *testing.T, s *Service, _ *clock.Fixed, tok string) (Approval, error) {
				nonce, sig, _ := decode(tok)
				sig[0] ^= 0xff
				return s.Verify(user, encode(nonce[:])+"."+encode(sig), 2)
			}, nil},
		{"stored record altered", BadSignature, false,
			func(t *testing.T, s *Service, _ *clock.Fixed, tok string) (Approval, error) {
				nonce, _, _ := decode(tok)
				s.records[nonce].approval.resolved[0] ^= 0xff
				return s.Verify(user, tok, 2)
			}, nil},
		{"another user's token", WrongUser, false,
			func(t *testing.T, s *Service, _ *clock.Fixed, tok string) (Approval, error) {
				return s.Verify("u_other", tok, 2)
			}, nil},
		{"reused (prompt 14)", AlreadyUsed, true,
			func(t *testing.T, s *Service, _ *clock.Fixed, tok string) (Approval, error) {
				consume(t, s, tok)
				return s.Verify(user, tok, 3)
			}, nil},
		{"replaced by a newer confirmation", Superseded, true,
			func(t *testing.T, s *Service, _ *clock.Fixed, tok string) (Approval, error) {
				issue(t, s, approved)
				return s.Verify(user, tok, 2)
			}, nil},
		{"expired (prompt 17)", Expired, true,
			func(t *testing.T, s *Service, clk *clock.Fixed, tok string) (Approval, error) {
				clk.Advance(ttl)
				return s.Verify(user, tok, 2)
			}, nil},
		{"same turn as issued", SameTurn, true,
			func(t *testing.T, s *Service, _ *clock.Fixed, tok string) (Approval, error) {
				return s.Verify(user, tok, 1)
			},
			func(t *testing.T, s *Service, tok string) {
				if _, err := s.Verify(user, tok, 2); err != nil {
					t.Errorf("later turn: %v", err)
				}
			}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s, clk := newTestService(t)
			tok := issue(t, s, approved)
			a, err := c.attempt(t, s, clk, tok)
			wantReason(t, err, c.want)
			if got := a.Snapshot() == approved.Snapshot; got != c.handedBack {
				t.Errorf("approval handed back: %t, want %t", got, c.handedBack)
			}
			if c.after != nil {
				c.after(t, s, tok)
			}
		})
	}
}

func TestApprovalHoldsTheCopy(t *testing.T) {
	s, _ := newTestService(t)
	a, err := s.Verify(user, issue(t, s, approved), 2)
	if err != nil {
		t.Fatal(err)
	}
	if a.Action() != domain.CancelOrder || a.Snapshot() != "o9" {
		t.Errorf("approval %s %v, want cancel_order o9", a.Action(), a.Snapshot())
	}
	if !a.AsksFor(CancelOrderParams("u_ok_o9")) || a.AsksFor(CancelOrderParams("u_ok_o1")) {
		t.Error("AsksFor does not tell the approved request from another")
	}
	if !a.Costs(CancelOrderParams("u_ok_o9")) || a.Costs(CancelOrderParams("u_ok_o1")) {
		t.Error("Costs does not tell the approved resolution from another")
	}
	tip := Approved{Request: AddTipRequest("u_ok_o9", 5000), Resolved: AddTipResolved("u_ok_o9", 5000, domain.Paid{Wallet: 5000})}
	b, err := s.Verify(user, issue(t, s, tip), 2)
	if err != nil {
		t.Fatal(err)
	}
	if !b.AsksFor(tip.Request) || b.AsksFor(tip.Resolved) || !b.Costs(tip.Resolved) || b.Costs(tip.Request) {
		t.Error("request and resolution are confused")
	}
}

func TestVerifyThenConsume(t *testing.T) {
	s, _ := newTestService(t)
	tok := issue(t, s, approved)

	a, err := s.Verify(user, tok, 2)
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.Verify(user, tok, 2)
	if err != nil {
		t.Fatalf("verify spent the approval: %v", err)
	}
	if a.DerivedID("ord_") != b.DerivedID("ord_") {
		t.Error("one approval, two idempotency keys")
	}
	if err := s.Consume(a); err != nil {
		t.Fatal(err)
	}
	wantReason(t, s.Consume(b), AlreadyUsed)
}

func TestConsumeRechecks(t *testing.T) {
	s, _ := newTestService(t)
	a, err := s.Verify(user, issue(t, s, approved), 2)
	if err != nil {
		t.Fatal(err)
	}
	issue(t, s, approved)
	wantReason(t, s.Consume(a), Superseded)

	wantReason(t, s.Consume(Approval{}), Unknown)
}

func TestLatest(t *testing.T) {
	cases := []struct {
		name  string
		want  Reason
		setup func(t *testing.T, s *Service, clk *clock.Fixed)
	}{
		{"the latest approval verifies", "", func(t *testing.T, s *Service, _ *clock.Fixed) { issue(t, s, approved) }},
		{"none issued (prompt 15)", Unknown, func(*testing.T, *Service, *clock.Fixed) {}},
		{"issued to another user", Unknown, func(t *testing.T, s *Service, _ *clock.Fixed) {
			if _, err := s.Issue("u_other", approved, ttl, 1); err != nil {
				t.Fatal(err)
			}
		}},
		{"issued for another action", Unknown, func(t *testing.T, s *Service, _ *clock.Fixed) {
			tip := Approved{Request: AddTipRequest("u_ok_o9", 5000), Resolved: AddTipResolved("u_ok_o9", 5000, domain.Paid{})}
			issue(t, s, tip)
		}},
		{"already used (prompt 14)", AlreadyUsed, func(t *testing.T, s *Service, _ *clock.Fixed) {
			consume(t, s, issue(t, s, approved))
		}},
		{"expired (prompt 17)", Expired, func(t *testing.T, s *Service, clk *clock.Fixed) {
			issue(t, s, approved)
			clk.Advance(ttl)
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s, clk := newTestService(t)
			c.setup(t, s, clk)
			_, err := s.Latest(user, domain.CancelOrder, 2)
			if c.want == "" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			wantReason(t, err, c.want)
		})
	}
}

func TestLatestFollowsTheNewest(t *testing.T) {
	s, _ := newTestService(t)
	first := issue(t, s, approved)
	issue(t, s, other)
	a, err := s.Latest(user, domain.CancelOrder, 2)
	if err != nil || a.Snapshot() != "o1" {
		t.Fatalf("latest: %v %v, want the newer approval", err, a.Snapshot())
	}
	_, err = s.Verify(user, first, 2)
	wantReason(t, err, Superseded)
}

func TestIssueRefusesMixedActions(t *testing.T) {
	s, _ := newTestService(t)
	mixed := Approved{Request: CancelOrderParams("u_ok_o9"), Resolved: AddTipResolved("u_ok_o9", 5000, domain.Paid{})}
	if _, err := s.Issue(user, mixed, ttl, 1); err == nil {
		t.Error("an approval whose request and resolution are different actions was issued")
	}
}

func TestShortSecretRefused(t *testing.T) {
	if _, err := NewService(make([]byte, minSecretSize-1), clock.NewFixed(time.Now())); err == nil {
		t.Error("a secret shorter than 32 bytes was accepted")
	}
	if _, ok := ReasonOf(errors.New("store failure")); ok {
		t.Error("ReasonOf treats an unrelated error as a token rejection")
	}
}
