package ui

import (
	"encoding/json"
	"os"
	"slices"
	"strings"
	"testing"

	"sofra/internal/action"
	"sofra/internal/gate"
)

func TestGatesMatchSchemaAndCallsToAction(t *testing.T) {
	var want []string
	for _, g := range gate.Gates() {
		want = append(want, string(g))
	}
	slices.Sort(want)

	var cta []string
	for g, text := range callsToAction {
		if text == "" {
			t.Errorf("gate %s has an empty call to action", g)
		}
		cta = append(cta, string(g))
	}
	slices.Sort(cta)
	if !slices.Equal(cta, want) {
		t.Errorf("calls to action %v, gates %v", cta, want)
	}

	raw, err := os.ReadFile("../../schema/ui_spec.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	type enum struct {
		Enum []string `json:"enum"`
	}
	var schema struct {
		Definitions struct {
			VerificationGate struct {
				Properties struct {
					Requirement enum `json:"requirement"`
				} `json:"properties"`
			} `json:"verification_gate"`
		} `json:"definitions"`
	}
	if err := json.Unmarshal(raw, &schema); err != nil {
		t.Fatal(err)
	}
	if got := slices.Sorted(slices.Values(schema.Definitions.VerificationGate.Properties.Requirement.Enum)); !slices.Equal(got, want) {
		t.Errorf("schema verification_gate.requirement %v, gates %v", got, want)
	}
}

func TestRefundText(t *testing.T) {
	cases := []struct {
		name         string
		refund       action.Refund
		want, absent []string
	}{
		{"seed order, no record of its payment", action.Refund{OrderUnrecorded: true},
			[]string{"260.00 TL is refunded by the payments system"}, []string{"wallet now"}},
		{"seed order with a tip", action.Refund{OrderUnrecorded: true, ToWallet: tl(20), ToCard: tl(30)},
			[]string{"payments system", "The tips left on it are refunded: 20.00 TL to the wallet now and 30.00 TL to the saved card"}, nil},
		{"an order placed here", action.Refund{ToWallet: tl(100), ToCard: tl(160)},
			[]string{"Refunded: 100.00 TL to the wallet now and 160.00 TL to the saved card within 3-5 business days"}, []string{"payments system"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := c.refund
			got := RefundText(action.Receipt{Event: action.EventCancelled, Amount: tl(260), Refund: &f})
			for _, w := range c.want {
				if !strings.Contains(got, w) {
					t.Errorf("%q does not say %q", got, w)
				}
			}
			for _, a := range c.absent {
				if strings.Contains(got, a) {
					t.Errorf("%q says %q", got, a)
				}
			}
		})
	}
}
