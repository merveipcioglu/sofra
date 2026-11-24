package pricing

import "sofra/internal/domain"

type PaymentPlan struct {
	FromWallet domain.Money
	FromCard   domain.Money
	Shortfall  domain.Money
}

func (p PaymentPlan) Paid() domain.Paid { return domain.Paid{Wallet: p.FromWallet, Card: p.FromCard} }

func PlanPayment(u domain.User, amount domain.Money) (PaymentPlan, bool) {
	fromWallet := min(u.WalletBalance, amount)
	remainder := amount - fromWallet
	if remainder > 0 && !u.HasSavedCard {
		return PaymentPlan{Shortfall: remainder}, false
	}
	return PaymentPlan{FromWallet: fromWallet, FromCard: remainder}, true
}
