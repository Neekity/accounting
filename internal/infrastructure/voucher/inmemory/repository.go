package inmemory

import (
	"context"
	"sync"

	"github.com/example/accounting/internal/app"
	"github.com/example/accounting/internal/domain/voucher"
)

// VoucherRepository is an in-memory implementation suitable for demos.
type VoucherRepository struct {
	mu          sync.RWMutex
	byMessageID map[string]voucher.Voucher
}

// NewVoucherRepository creates the repository.
func NewVoucherRepository() *VoucherRepository {
	return &VoucherRepository{byMessageID: make(map[string]voucher.Voucher)}
}

func (r *VoucherRepository) Save(_ context.Context, v voucher.Voucher) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.byMessageID[v.MessageID] = v
	return nil
}

func (r *VoucherRepository) DeleteByMessageID(_ context.Context, messageID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.byMessageID, messageID)
	return nil
}

func (r *VoucherRepository) FindByMessageID(_ context.Context, messageID string) (voucher.Voucher, bool, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	v, ok := r.byMessageID[messageID]
	return v, ok, nil
}

var _ app.VoucherRepository = (*VoucherRepository)(nil)
