package rule

import "context"

// Key identifies a rule by business attributes.
type Key struct {
	TransType  string
	AmountType string
}

// Template describes a debit/credit mapping used to create voucher entries.
type Template struct {
	DebitAccount  string
	CreditAccount string
}

// Rule contains metadata required to apply a voucher template.
type Rule struct {
	Key      Key
	Template Template
}

// Repository resolves rules by key.
type Repository interface {
	FindByKey(ctx context.Context, key Key) (Rule, error)
}

// NotFoundError indicates the rule does not exist.
type NotFoundError struct {
	Key Key
}

func (e NotFoundError) Error() string {
	return "voucher rule not found"
}
