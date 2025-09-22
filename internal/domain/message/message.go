package message

import "time"

// Message represents an event pushed from upstream business systems.
type Message struct {
	ID         string
	TransType  string
	Amounts    []AmountLine
	OccurredAt time.Time
	Metadata   map[string]string
}

// AmountLine describes an amount broken down by amount_type.
type AmountLine struct {
	AmountType string
	Amount     int64
	Currency   string
}

// Validate performs basic domain validations on the message.
func (m Message) Validate() error {
	if m.ID == "" {
		return ErrMissingID
	}
	if m.TransType == "" {
		return ErrMissingTransType
	}
	if len(m.Amounts) == 0 {
		return ErrNoAmounts
	}
	for _, line := range m.Amounts {
		if line.AmountType == "" {
			return ErrInvalidAmountType
		}
		if line.Amount == 0 {
			return ErrInvalidAmount
		}
	}
	return nil
}

var (
	ErrMissingID         = ValidationError{"message id is required"}
	ErrMissingTransType  = ValidationError{"trans_type is required"}
	ErrNoAmounts         = ValidationError{"amount lines are required"}
	ErrInvalidAmountType = ValidationError{"amount_type is required"}
	ErrInvalidAmount     = ValidationError{"amount must be non-zero"}
)

// ValidationError is a domain level validation error.
type ValidationError struct {
	Msg string
}

func (e ValidationError) Error() string { return e.Msg }
