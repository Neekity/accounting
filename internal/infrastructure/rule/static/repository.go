package static

import (
	"context"

	"github.com/example/accounting/internal/domain/rule"
)

// Repository returns voucher rules from an in-memory catalogue derived from the documentation tables.
type Repository struct {
	rules map[rule.Key]rule.Rule
}

// NewRepository seeds the repository with default demo rules.
func NewRepository() *Repository {
	repo := &Repository{rules: make(map[rule.Key]rule.Rule)}
	for _, r := range defaultRules() {
		repo.rules[r.Key] = r
	}
	return repo
}

// FindByKey returns the rule that matches the provided key.
func (r *Repository) FindByKey(_ context.Context, key rule.Key) (rule.Rule, error) {
	if rule, ok := r.rules[key]; ok {
		return rule, nil
	}
	return rule.Rule{}, rule.NotFoundError{Key: key}
}

func defaultRules() []rule.Rule {
	return []rule.Rule{
		{
			Key: rule.Key{TransType: "loan_repayplan", AmountType: "principal"},
			Template: rule.Template{
				DebitAccount:  "1221.01.01应收账款-未到期-应收本金",
				CreditAccount: "1012.X.02其他货币资金-通道/账户-放款",
			},
		},
		{
			Key: rule.Key{TransType: "loan_repayplan", AmountType: "interest"},
			Template: rule.Template{
				DebitAccount:  "1221.01.02应收账款-未到期-应收利息",
				CreditAccount: "6001.03.01营业收入-未到期收入-利息收入",
			},
		},
		{
			Key: rule.Key{TransType: "loan_repayplan", AmountType: "fin_service"},
			Template: rule.Template{
				DebitAccount:  "1221.01.03应收账款-未到期-应收服务费",
				CreditAccount: "6001.03.02营业收入-未到期收入-服务费收入",
			},
		},
		{
			Key: rule.Key{TransType: "repay_before_compensate", AmountType: "principal"},
			Template: rule.Template{
				DebitAccount:  "1221.03应收账款-清分",
				CreditAccount: "1221.02.01应收账款-已到期-应收本金",
			},
		},
		{
			Key: rule.Key{TransType: "repay_before_compensate", AmountType: "interest"},
			Template: rule.Template{
				DebitAccount:  "1221.03应收账款-清分",
				CreditAccount: "1221.02.02应收账款-已到期-应收利息",
			},
		},
	}
}

var _ rule.Repository = (*Repository)(nil)
