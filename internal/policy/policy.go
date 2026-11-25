package policy

import (
	"errors"
	"fmt"
	"regexp"
	"time"

	"sofra/internal/domain"
)

const (
	FreeDeliveryThreshold domain.Money = 250_00
	TipCap                domain.Money = 500_00
	TipWindowDays                      = 7
	TokenTTL                           = 5 * time.Minute
	MaxItemQty                         = 50
)

type claim struct {
	param  string
	docID  string
	phrase string
}

func claims() []claim {
	return []claim{
		{"free delivery threshold", "pol_delivery_fee_v2", liraText(FreeDeliveryThreshold)},
		{"tip window", "pol_tip", fmt.Sprintf("%d days", TipWindowDays)},
	}
}

func VerifyAgainstKnowledge(currentPolicy func(docID string) (string, bool)) error {
	return verify(claims(), currentPolicy)
}

func verify(claims []claim, currentPolicy func(docID string) (string, bool)) error {
	var errs []error
	for _, c := range claims {
		body, ok := currentPolicy(c.docID)
		if !ok {
			errs = append(errs, fmt.Errorf("%s: source %q is not a current policy document", c.param, c.docID))
			continue
		}
		if !regexp.MustCompile(`\b` + regexp.QuoteMeta(c.phrase) + `\b`).MatchString(body) {
			errs = append(errs, fmt.Errorf("%s: %s does not state %q; update the policy or the document",
				c.param, c.docID, c.phrase))
		}
	}
	return errors.Join(errs...)
}

func liraText(m domain.Money) string {
	if m%100 == 0 {
		return fmt.Sprintf("%d TL", m/100)
	}
	return fmt.Sprintf("%d.%02d TL", m/100, m%100)
}
