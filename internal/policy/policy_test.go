package policy

import (
	"fmt"
	"strings"
	"testing"

	"sofra/internal/knowledge"
)

func loadKB(t *testing.T) *knowledge.Base {
	t.Helper()
	kb, err := knowledge.Load("../../data/knowledge/kb.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	return kb
}

func TestConstantsMatchKnowledge(t *testing.T) {
	if err := VerifyAgainstKnowledge(loadKB(t).CurrentPolicy); err != nil {
		t.Fatal(err)
	}
}

func TestVerifyReportsDrift(t *testing.T) {
	kb := loadKB(t)
	threshold := claim{"free delivery threshold", "pol_delivery_fee_v2", liraText(150_00)}
	window := claim{"tip window", "pol_tip", fmt.Sprintf("%d days", 14)}
	cases := []struct {
		name    string
		changed []claim
		want    []string
	}{
		{"threshold", []claim{threshold}, []string{"free delivery threshold"}},
		{"tip window", []claim{window}, []string{"tip window"}},
		{"both", []claim{threshold, window}, []string{"free delivery threshold", "tip window"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := verify(withChanged(c.changed), kb.CurrentPolicy)
			if err == nil {
				t.Fatal("drift not reported")
			}
			for _, w := range c.want {
				if !strings.Contains(err.Error(), w) {
					t.Errorf("error %q does not mention %q", err, w)
				}
			}
		})
	}
}

func withChanged(changed []claim) []claim {
	all := claims()
	for i, c := range all {
		for _, ch := range changed {
			if ch.param == c.param {
				all[i] = ch
			}
		}
	}
	return all
}

func TestArchivedPolicyIsNotASource(t *testing.T) {
	if _, ok := loadKB(t).CurrentPolicy("pol_delivery_fee_v1_old"); ok {
		t.Fatal("the archived delivery-fee policy is accepted as current")
	}
}
