package http

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/example/accounting/internal/app"
	domainmsg "github.com/example/accounting/internal/domain/message"
)

// Server exposes HTTP handlers for processing messages.
type Server struct {
	service *app.Service
}

// NewServer creates the API server.
func NewServer(service *app.Service) *Server {
	return &Server{service: service}
}

// Routes registers HTTP routes.
func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/messages", s.processMessage)
	return mux
}

func (s *Server) processMessage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	var req messageRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	msg := domainmsg.Message{
		ID:        req.ID,
		TransType: req.TransType,
		Metadata:  req.Metadata,
	}
	if req.OccurredAt != "" {
		if ts, err := time.Parse(time.RFC3339, req.OccurredAt); err == nil {
			msg.OccurredAt = ts
		}
	}

	for _, line := range req.Amounts {
		msg.Amounts = append(msg.Amounts, domainmsg.AmountLine{
			AmountType: line.AmountType,
			Amount:     line.Amount,
			Currency:   line.Currency,
		})
	}

	regenerate := r.URL.Query().Get("regenerate") == "true"
	result, err := s.service.ProcessMessage(r.Context(), app.ProcessCommand{Message: msg, Regenerate: regenerate})
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	status := http.StatusCreated
	if !result.Created {
		status = http.StatusOK
	}
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(newMessageResponse(result))
}

type messageRequest struct {
	ID         string            `json:"id"`
	TransType  string            `json:"trans_type"`
	OccurredAt string            `json:"occurred_at"`
	Amounts    []amountLine      `json:"amounts"`
	Metadata   map[string]string `json:"metadata"`
}

type amountLine struct {
	AmountType string `json:"amount_type"`
	Amount     int64  `json:"amount"`
	Currency   string `json:"currency"`
}

type messageResponse struct {
	VoucherID   string            `json:"voucher_id"`
	MessageID   string            `json:"message_id"`
	GeneratedAt time.Time         `json:"generated_at"`
	Entries     []entryResponse   `json:"entries"`
	Metadata    map[string]string `json:"metadata"`
}

type entryResponse struct {
	DebitAccount  string `json:"debit_account"`
	CreditAccount string `json:"credit_account"`
	Amount        int64  `json:"amount"`
	Currency      string `json:"currency"`
	AmountType    string `json:"amount_type"`
}

func newMessageResponse(result app.ProcessResult) messageResponse {
	resp := messageResponse{
		VoucherID:   result.Voucher.ID,
		MessageID:   result.Voucher.MessageID,
		GeneratedAt: result.Voucher.GeneratedAt,
		Metadata:    result.Voucher.Metadata,
	}
	for _, entry := range result.Voucher.Entries {
		resp.Entries = append(resp.Entries, entryResponse{
			DebitAccount:  entry.DebitAccount,
			CreditAccount: entry.CreditAccount,
			Amount:        entry.Amount,
			Currency:      entry.Currency,
			AmountType:    entry.AmountType,
		})
	}
	return resp
}
