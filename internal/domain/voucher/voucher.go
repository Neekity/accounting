package voucher

import (
	"time"
)

// Entry represents the debit/credit accounts for a single voucher line.
type Entry struct {
	DebitAccount  string
	CreditAccount string
	Amount        int64
	Currency      string
	AmountType    string
}

// Voucher stores accounting entries generated for a message.
type Voucher struct {
	ID          string
	MessageID   string
	GeneratedAt time.Time
	Entries     []Entry
	Metadata    map[string]string
}

func (v Voucher) TotalAmount() int64 {
	var total int64
	for _, entry := range v.Entries {
		total += entry.Amount
	}
	return total
}
