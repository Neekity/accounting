package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"time"

	"github.com/example/accounting/internal/domain/message"
	"github.com/example/accounting/internal/domain/rule"
	"github.com/example/accounting/internal/domain/voucher"
)

// VoucherRepository persists vouchers and message metadata.
type VoucherRepository interface {
	Save(ctx context.Context, v voucher.Voucher) error
	DeleteByMessageID(ctx context.Context, messageID string) error
	FindByMessageID(ctx context.Context, messageID string) (voucher.Voucher, bool, error)
}

// MessageStore keeps track of message processing history.
type MessageStore interface {
	Upsert(ctx context.Context, record MessageRecord) error
	FindByID(ctx context.Context, id string) (MessageRecord, bool, error)
}

// MessageRecord stores process metadata for idempotency and regeneration.
type MessageRecord struct {
	ID            string
	TransType     string
	ProcessedAt   time.Time
	LastVoucherID string
}

// Service orchestrates the message workflow.
type Service struct {
	ruleRepo    rule.Repository
	voucherRepo VoucherRepository
	messages    MessageStore
}

// NewService builds the application service.
func NewService(ruleRepo rule.Repository, voucherRepo VoucherRepository, store MessageStore) *Service {
	return &Service{ruleRepo: ruleRepo, voucherRepo: voucherRepo, messages: store}
}

// ProcessCommand contains parameters to process incoming messages.
type ProcessCommand struct {
	Message    message.Message
	Regenerate bool
}

// ProcessResult returns metadata from voucher creation.
type ProcessResult struct {
	Voucher voucher.Voucher
	Created bool
}

// ProcessMessage handles a message with idempotency and regeneration support.
func (s *Service) ProcessMessage(ctx context.Context, cmd ProcessCommand) (ProcessResult, error) {
	if err := cmd.Message.Validate(); err != nil {
		return ProcessResult{}, err
	}

	_, exists, err := s.messages.FindByID(ctx, cmd.Message.ID)
	if err != nil {
		return ProcessResult{}, err
	}

	if exists && !cmd.Regenerate {
		existingVoucher, found, err := s.voucherRepo.FindByMessageID(ctx, cmd.Message.ID)
		if err != nil {
			return ProcessResult{}, err
		}
		if found {
			return ProcessResult{Voucher: existingVoucher, Created: false}, nil
		}
	}

	if cmd.Regenerate {
		// remove previous artifacts so we can recompute from scratch
		if err := s.voucherRepo.DeleteByMessageID(ctx, cmd.Message.ID); err != nil {
			return ProcessResult{}, err
		}
	}

	entries := make([]voucher.Entry, 0, len(cmd.Message.Amounts))
	for _, line := range cmd.Message.Amounts {
		r, err := s.ruleRepo.FindByKey(ctx, rule.Key{TransType: cmd.Message.TransType, AmountType: line.AmountType})
		if err != nil {
			if errors.As(err, &rule.NotFoundError{}) {
				return ProcessResult{}, err
			}
			return ProcessResult{}, err
		}

		entries = append(entries, voucher.Entry{
			DebitAccount:  r.Template.DebitAccount,
			CreditAccount: r.Template.CreditAccount,
			Amount:        line.Amount,
			Currency:      line.Currency,
			AmountType:    line.AmountType,
		})
	}

	id, err := generateID()
	if err != nil {
		return ProcessResult{}, err
	}

	v := voucher.Voucher{
		ID:          id,
		MessageID:   cmd.Message.ID,
		GeneratedAt: time.Now().UTC(),
		Entries:     entries,
		Metadata:    cmd.Message.Metadata,
	}

	if err := s.voucherRepo.Save(ctx, v); err != nil {
		return ProcessResult{}, err
	}

	record := MessageRecord{
		ID:            cmd.Message.ID,
		TransType:     cmd.Message.TransType,
		ProcessedAt:   time.Now().UTC(),
		LastVoucherID: v.ID,
	}
	if err := s.messages.Upsert(ctx, record); err != nil {
		return ProcessResult{}, err
	}

	return ProcessResult{Voucher: v, Created: true}, nil
}

func generateID() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}
