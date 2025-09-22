# Accounting Demo

This repository now includes a Go-based demo that showcases the DDD architecture described in the documentation. The sample service accepts upstream business messages via an HTTP API, looks up voucher rules, and produces accounting vouchers with regeneration and idempotency safeguards.

## Run the API server

```bash
cd /workspace/accounting
go run ./cmd/api
```

The server listens on `:8080` and exposes a `POST /v1/messages` endpoint.

## Sample request

```bash
curl -X POST http://localhost:8080/v1/messages \
  -H 'Content-Type: application/json' \
  -d '{
    "id": "demo-001",
    "trans_type": "loan_repayplan",
    "amounts": [
      {"amount_type": "principal", "amount": 100000, "currency": "IDR"},
      {"amount_type": "interest", "amount": 5000, "currency": "IDR"}
    ],
    "metadata": {"asset_id": "ASSET-1"}
  }'
```

Repeat the same call with `?regenerate=true` in the URL to force voucher regeneration for the same message identifier.
