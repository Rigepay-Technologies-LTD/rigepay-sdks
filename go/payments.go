package rigepay

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
)

// All amounts are int64 cents. Payouts and transfers name the field "amount" on the wire;
// everything else names it "amount_cents". The Go field is AmountCents either way.

// doFlat decodes the whole response body into out, for endpoints that answer without a data envelope.
func (c *Client) doFlat(ctx context.Context, method, path string, req Request, out any) error {
	resp, err := c.Do(ctx, method, path, req, nil)
	if err != nil {
		return err
	}
	if out != nil && len(resp.Raw) > 0 {
		return json.Unmarshal(resp.Raw, out)
	}
	return nil
}

func ptr[T any](v *T, err error) (*T, error) {
	if err != nil {
		return nil, err
	}
	return v, nil
}

func (c *Client) doRaw(ctx context.Context, method, path string, req Request) (json.RawMessage, error) {
	resp, err := c.Do(ctx, method, path, req, nil)
	if err != nil {
		return nil, err
	}
	return resp.Raw, nil
}

// --- OAuth ---

type OAuthService struct{ c *Client }

type TokenResponse struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	ExpiresIn   int64  `json:"expires_in"`
	Scope       string `json:"scope"`
}

// CreateToken exchanges client credentials for an access token (POST /oauth/token). A client made
// with NewWithClientCredentials calls this for you and caches the result.
func (s *OAuthService) CreateToken(ctx context.Context, clientID, clientSecret string) (*TokenResponse, error) {
	var out TokenResponse
	body := map[string]string{"grant_type": "client_credentials", "client_id": clientID, "client_secret": clientSecret}
	err := s.c.doFlat(ctx, http.MethodPost, "/oauth/token", Request{Body: body, NoAuth: true}, &out)
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// --- whoami ---

type WhoAmI struct {
	OrganizationID     string   `json:"organization_id,omitempty"`
	OrganizationStatus string   `json:"organization_status,omitempty"`
	BranchIDs          []string `json:"branch_ids,omitempty"`
	APIClientID        string   `json:"api_client_id,omitempty"`
	APIClientStatus    string   `json:"api_client_status,omitempty"`
	Scopes             []string `json:"scopes,omitempty"`
	Role               string   `json:"role,omitempty"`
	AuthType           string   `json:"auth_type,omitempty"`
	ExpiresIn          int64    `json:"expires_in,omitempty"`
}

// WhoAmI shows which account the credential resolves to (GET /payments/whoami).
func (c *Client) WhoAmI(ctx context.Context) (*WhoAmI, error) {
	var out WhoAmI
	_, err := c.Do(ctx, http.MethodGet, "/payments/whoami", Request{}, &out)
	return ptr(&out, err)
}

// --- Collections ---

type CollectionsService struct{ c *Client }

type STKPushInput struct {
	AmountCents   int64          `json:"amount_cents"`
	CustomerPhone string         `json:"customer_phone"`
	Remarks       string         `json:"remarks,omitempty"`
	BranchID      string         `json:"branch_id,omitempty"`
	Metadata      map[string]any `json:"metadata,omitempty"`
	Channel       string         `json:"channel,omitempty"` // mpesa, airtel or tkash
}

type STKPushResult struct {
	Reference         string `json:"reference"`
	CheckoutRequestID string `json:"checkout_request_id"`
	MerchantRequestID string `json:"merchant_request_id"`
	CustomerMessage   string `json:"customer_message"`
	Rail              string `json:"rail"`
	Channel           string `json:"channel"`
	RequiresOTP       bool   `json:"requires_otp"`
}

// STKPush prompts the customer to pay. A success means the prompt was sent, not that they paid:
// wait for the collection.completed webhook.
func (s *CollectionsService) STKPush(ctx context.Context, in STKPushInput, idempotencyKey string) (*STKPushResult, error) {
	var out STKPushResult
	_, err := s.c.Do(ctx, http.MethodPost, "/payments/collections/stk-push", Request{Body: in, IdempotencyKey: idempotencyKey}, &out)
	return ptr(&out, err)
}

// --- Checkout ---

type CheckoutService struct{ c *Client }

type CheckoutSessionInput struct {
	AmountCents  int64          `json:"amount_cents"`
	AllowedRails []string       `json:"allowed_rails"` // mpesa_stk, mpesa_c2b, bank_transfer, card
	BranchID     string         `json:"branch_id,omitempty"`
	Description  string         `json:"description,omitempty"`
	Metadata     map[string]any `json:"metadata,omitempty"`
}

type CheckoutSession struct {
	SessionID        string `json:"session_id"`
	Status           string `json:"status"`
	CompletedViaRail string `json:"completed_via_rail"`
	ExpiresAt        string `json:"expires_at"`
	AmountCents      int64  `json:"amount_cents"`
	Currency         string `json:"currency"`
	Reference        string `json:"reference"`
	CheckoutURL      string `json:"checkout_url"`
}

func (s *CheckoutService) CreateSession(ctx context.Context, in CheckoutSessionInput, idempotencyKey string) (*CheckoutSession, error) {
	var out CheckoutSession
	_, err := s.c.Do(ctx, http.MethodPost, "/payments/checkout/sessions", Request{Body: in, IdempotencyKey: idempotencyKey}, &out)
	return ptr(&out, err)
}

func (s *CheckoutService) GetSession(ctx context.Context, id string) (*CheckoutSession, error) {
	var out CheckoutSession
	_, err := s.c.Do(ctx, http.MethodGet, "/payments/checkout/sessions/"+url.PathEscape(id), Request{}, &out)
	return ptr(&out, err)
}

// --- Payouts ---

type PayoutsService struct{ c *Client }

type PayoutInput struct {
	AmountCents       int64          `json:"amount"`
	RecipientName     string         `json:"recipient_name"`
	Remarks           string         `json:"remarks"`
	DestinationType   string         `json:"destination_type,omitempty"` // PHONE_NUMBER, POCHI, BANK_ACCOUNT, PAYBILL, TILL_NUMBER
	PhoneNumber       string         `json:"phone_number,omitempty"`
	BankCode          string         `json:"bank_code,omitempty"`
	BankAccountNumber string         `json:"bank_account_number,omitempty"`
	Shortcode         string         `json:"shortcode,omitempty"`
	AccountReference  string         `json:"account_reference,omitempty"`
	BranchID          string         `json:"branch_id,omitempty"`
	Metadata          map[string]any `json:"metadata,omitempty"`
}

// PayoutResult is the 202 body. Status is "success" (queued) or "approval_required".
type PayoutResult struct {
	Status      string `json:"status"`
	Message     string `json:"message"`
	PayoutID    string `json:"payout_id"`
	ApprovalID  string `json:"approval_id"`
	AmountCents int64  `json:"amount"`
	FeeCents    int64  `json:"fee_cents"`
}

func (s *PayoutsService) Create(ctx context.Context, in PayoutInput, idempotencyKey string) (*PayoutResult, error) {
	var out PayoutResult
	if err := s.c.doFlat(ctx, http.MethodPost, "/payments/payouts", Request{Body: in, IdempotencyKey: idempotencyKey}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

type FeeEstimate struct {
	AmountCents int64 `json:"amount_cents"`
	FeeCents    int64 `json:"fee_cents"`
	TotalCents  int64 `json:"total_cents"`
	IsEstimate  bool  `json:"is_estimate"`
}

// FeeEstimate estimates the fee for a payout. destinationType may be empty (PHONE_NUMBER).
func (s *PayoutsService) FeeEstimate(ctx context.Context, amountCents int64, destinationType string) (*FeeEstimate, error) {
	q := url.Values{"amount_cents": {strconv.FormatInt(amountCents, 10)}}
	if destinationType != "" {
		q.Set("destination_type", destinationType)
	}
	var out FeeEstimate
	_, err := s.c.Do(ctx, http.MethodGet, "/payments/payouts/fee-estimate", Request{Query: q}, &out)
	return ptr(&out, err)
}

type ApprovalCeiling struct {
	Applies          bool  `json:"applies"`
	Configured       bool  `json:"configured"`
	AmountCents      int64 `json:"amount_cents"`
	Active           bool  `json:"active"`
	ActiveOwnerCount int64 `json:"active_owner_count"`
	Enforceable      bool  `json:"enforceable"`
}

// ApprovalCeiling reports whether a high-value approval ceiling applies. amountCents 0 omits the amount.
func (s *PayoutsService) ApprovalCeiling(ctx context.Context, amountCents int64) (*ApprovalCeiling, error) {
	q := url.Values{}
	if amountCents > 0 {
		q.Set("amount_cents", strconv.FormatInt(amountCents, 10))
	}
	var out ApprovalCeiling
	_, err := s.c.Do(ctx, http.MethodGet, "/payments/payouts/approval-ceiling", Request{Query: q}, &out)
	return ptr(&out, err)
}

// --- Bulk payouts ---

type BulkPayoutsService struct{ c *Client }

type BulkPayoutItem struct {
	AmountCents       int64  `json:"amount_cents"`
	RecipientName     string `json:"recipient_name"`
	DestinationType   string `json:"destination_type,omitempty"`
	PhoneNumber       string `json:"phone_number,omitempty"`
	BankCode          string `json:"bank_code,omitempty"`
	BankAccountNumber string `json:"bank_account_number,omitempty"`
	Shortcode         string `json:"shortcode,omitempty"`
	AccountReference  string `json:"account_reference,omitempty"`
	Remarks           string `json:"remarks,omitempty"`
}

type BulkPayoutInput struct {
	FundingSource string           `json:"funding_source,omitempty"` // MAIN or VAULT
	VaultID       string           `json:"vault_id,omitempty"`
	Remarks       string           `json:"remarks,omitempty"`
	Items         []BulkPayoutItem `json:"items"`
}

type BulkPayoutBatch struct {
	ID                   string `json:"id"`
	ItemCount            int64  `json:"item_count"`
	TotalAmountCents     int64  `json:"total_amount_cents"`
	TotalFeeReserveCents int64  `json:"total_fee_reserve_cents"`
	Status               string `json:"status"`
	DispatchedCount      int64  `json:"dispatched_count"`
	RejectedCount        int64  `json:"rejected_count"`
	Remarks              string `json:"remarks"`
	CreatedAt            string `json:"created_at"`
}

type BulkPayoutDetail struct {
	Batch BulkPayoutBatch `json:"batch"`
	Items []struct {
		RowNumber       int64  `json:"row_number"`
		AmountCents     int64  `json:"amount_cents"`
		Status          string `json:"status"`
		PayoutID        string `json:"payout_id"`
		PayoutStatus    string `json:"payout_status"`
		RejectionReason string `json:"rejection_reason"`
	} `json:"items"`
	EscrowBalanceCents int64 `json:"escrow_balance_cents"`
}

func (s *BulkPayoutsService) Create(ctx context.Context, in BulkPayoutInput, idempotencyKey string) (*BulkPayoutBatch, error) {
	var out BulkPayoutBatch
	_, err := s.c.Do(ctx, http.MethodPost, "/payments/bulk-payouts", Request{Body: in, IdempotencyKey: idempotencyKey}, &out)
	return ptr(&out, err)
}

func (s *BulkPayoutsService) List(ctx context.Context) ([]BulkPayoutBatch, error) {
	var out []BulkPayoutBatch
	_, err := s.c.Do(ctx, http.MethodGet, "/payments/bulk-payouts", Request{}, &out)
	return out, err
}

func (s *BulkPayoutsService) Get(ctx context.Context, batchID string) (*BulkPayoutDetail, error) {
	var out BulkPayoutDetail
	_, err := s.c.Do(ctx, http.MethodGet, "/payments/bulk-payouts/"+url.PathEscape(batchID), Request{}, &out)
	return ptr(&out, err)
}

// --- Transfers ---

type TransfersService struct{ c *Client }

type TransferInput struct {
	From        string `json:"from"` // "org" or a branch UUID
	To          string `json:"to"`
	AmountCents int64  `json:"amount"`
	Remarks     string `json:"remarks,omitempty"`
}

type Transfer struct {
	TxnID       string `json:"txn_id"`
	AmountCents int64  `json:"amount_cents"`
	From        string `json:"from"`
	To          string `json:"to"`
}

// Create moves money between the organization and its branches (POST /payments/transfers).
func (s *TransfersService) Create(ctx context.Context, in TransferInput, idempotencyKey string) (*Transfer, error) {
	var out Transfer
	_, err := s.c.Do(ctx, http.MethodPost, "/payments/transfers", Request{Body: in, IdempotencyKey: idempotencyKey}, &out)
	return ptr(&out, err)
}

type TransferRecipient struct {
	Name           string `json:"name"`
	Type           string `json:"type"`
	CollectionCode string `json:"collection_code"`
}

func (s *TransfersService) Lookup(ctx context.Context, code string) (*TransferRecipient, error) {
	var out TransferRecipient
	_, err := s.c.Do(ctx, http.MethodGet, "/payments/transfers/lookup", Request{Query: url.Values{"code": {code}}}, &out)
	return ptr(&out, err)
}

type ExternalTransferInput struct {
	RecipientCollectionCode string `json:"recipient_collection_code"`
	AmountCents             int64  `json:"amount"`
	From                    string `json:"from,omitempty"` // a branch UUID; empty sends from the organization wallet
	Remarks                 string `json:"remarks"`
}

// ExternalTransferStarted is the 202 body: Status is "otp_required".
type ExternalTransferStarted struct {
	Status     string `json:"status"`
	Message    string `json:"message"`
	TransferID string `json:"transfer_id"`
	ExpiresIn  int64  `json:"expires_in"`
}

type ExternalTransfer struct {
	TransferID  string `json:"transfer_id"`
	AmountCents int64  `json:"amount_cents"`
	From        string `json:"from"`
	To          string `json:"to"`
}

// StartExternal starts a transfer to another organization; confirm it with the OTP sent by SMS.
func (s *TransfersService) StartExternal(ctx context.Context, in ExternalTransferInput, idempotencyKey string) (*ExternalTransferStarted, error) {
	var out ExternalTransferStarted
	if err := s.c.doFlat(ctx, http.MethodPost, "/payments/transfers/external", Request{Body: in, IdempotencyKey: idempotencyKey}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (s *TransfersService) ConfirmExternal(ctx context.Context, transferID, otp, idempotencyKey string) (*ExternalTransfer, error) {
	var out ExternalTransfer
	body := map[string]string{"transfer_id": transferID, "otp": otp}
	_, err := s.c.Do(ctx, http.MethodPost, "/payments/transfers/external/confirm", Request{Body: body, IdempotencyKey: idempotencyKey}, &out)
	return ptr(&out, err)
}

func (s *TransfersService) CancelExternal(ctx context.Context, transferID string) (string, error) {
	var out struct {
		TransferID string `json:"transfer_id"`
	}
	_, err := s.c.Do(ctx, http.MethodPost, "/payments/transfers/external/cancel", Request{Body: map[string]string{"transfer_id": transferID}}, &out)
	return out.TransferID, err
}

// --- Balance and transactions ---

type BalanceService struct{ c *Client }

type Wallet struct {
	Type           string `json:"type"`
	WalletName     string `json:"wallet_name"`
	Currency       string `json:"currency"`
	BalanceCents   int64  `json:"balance_bigint"`
	AvailableCents int64  `json:"available_bigint"`
	Status         string `json:"status"`
}

type Balances struct {
	BranchID string `json:"branch_id"`
	Balances struct {
		MainCents       int64 `json:"main_cents"`
		EscrowCents     int64 `json:"escrow_cents"`
		ChargebackCents int64 `json:"chargeback_cents"`
	} `json:"balances"`
	Wallets []Wallet `json:"wallets"`
}

// Get reads wallet balances. branchID may be empty.
func (s *BalanceService) Get(ctx context.Context, branchID string) (*Balances, error) {
	q := url.Values{}
	if branchID != "" {
		q.Set("branch_id", branchID)
	}
	var out Balances
	_, err := s.c.Do(ctx, http.MethodGet, "/payments/balance", Request{Query: q}, &out)
	return ptr(&out, err)
}

type TransactionsService struct{ c *Client }

type Transaction struct {
	ID            string  `json:"id"`
	Type          string  `json:"type"`
	Direction     string  `json:"direction"`
	Status        string  `json:"status"`
	AmountCents   int64   `json:"amountCents"`
	FeeCents      int64   `json:"feeCents"`
	Currency      string  `json:"currency"`
	Rail          string  `json:"rail"`
	Reference     string  `json:"reference"`
	ExternalTxnID *string `json:"externalTxnId"`
	Description   string  `json:"description"`
	CustomerPhone string  `json:"customer_phone"`
	CreatedAt     string  `json:"created_at"`
	UpdatedAt     string  `json:"updated_at"`
}

type TransactionPage struct {
	TotalCount   int64         `json:"total_count"`
	Page         int64         `json:"page"`
	PageSize     int64         `json:"page_size"`
	TotalPages   int64         `json:"total_pages"`
	Transactions []Transaction `json:"transactions"`
}

// List takes the API's query names: page, page_size, search, status, direction, type, rail,
// start_date, end_date, min_amount, max_amount (cents), branch_id.
func (s *TransactionsService) List(ctx context.Context, query url.Values) (*TransactionPage, error) {
	var out TransactionPage
	_, err := s.c.Do(ctx, http.MethodGet, "/payments/transactions", Request{Query: query}, &out)
	return ptr(&out, err)
}

func (s *TransactionsService) Get(ctx context.Context, id string) (*Transaction, error) {
	var out Transaction
	_, err := s.c.Do(ctx, http.MethodGet, "/payments/transactions/"+url.PathEscape(id), Request{}, &out)
	return ptr(&out, err)
}

// --- Payment links and invoices ---
//
// The spec leaves these response bodies open, so they come back as the raw JSON body for you to
// decode (decode cents into an integer type, not float64).

type PaymentLinksService struct{ c *Client }

type PaymentLinkInput struct {
	AmountCents     int64  `json:"amount_cents,omitempty"`
	Currency        string `json:"currency"` // KES or USD
	PaymentMethods  string `json:"payment_methods,omitempty"`
	Description     string `json:"description,omitempty"`
	Name            string `json:"name,omitempty"`
	IsReusable      bool   `json:"is_reusable,omitempty"`
	AllowOpenAmount bool   `json:"allow_open_amount,omitempty"`
	ExpiresInDays   int64  `json:"expires_in_days,omitempty"`
	MaxUses         int64  `json:"max_uses,omitempty"`
	RedirectURL     string `json:"redirect_url,omitempty"`
	CancelURL       string `json:"cancel_url,omitempty"`
	LinkType        string `json:"link_type,omitempty"` // STANDARD, INVOICE, CUSTOMER, DONATION
}

// Create returns the whole body: the link in "data", its shareable address in "url".
func (s *PaymentLinksService) Create(ctx context.Context, in PaymentLinkInput, idempotencyKey string) (json.RawMessage, error) {
	return s.c.doRaw(ctx, http.MethodPost, "/payments/payment-links", Request{Body: in, IdempotencyKey: idempotencyKey})
}

func (s *PaymentLinksService) List(ctx context.Context, query url.Values) (json.RawMessage, error) {
	return s.c.doRaw(ctx, http.MethodGet, "/payments/payment-links", Request{Query: query})
}

func (s *PaymentLinksService) Get(ctx context.Context, id string) (json.RawMessage, error) {
	return s.c.doRaw(ctx, http.MethodGet, "/payments/payment-links/"+url.PathEscape(id), Request{})
}

func (s *PaymentLinksService) Close(ctx context.Context, id, idempotencyKey string) (json.RawMessage, error) {
	return s.c.doRaw(ctx, http.MethodPost, "/payments/payment-links/"+url.PathEscape(id)+"/close", Request{IdempotencyKey: idempotencyKey})
}

type InvoicesService struct{ c *Client }

type InvoiceInput struct {
	CustomerName     string           `json:"customer_name"`
	CustomerPhone    string           `json:"customer_phone"`
	CustomerEmail    string           `json:"customer_email,omitempty"`
	Currency         string           `json:"currency"`
	DueDate          string           `json:"due_date"` // RFC 3339
	Notes            string           `json:"notes,omitempty"`
	PricesIncludeVAT bool             `json:"prices_include_vat,omitempty"`
	Items            []map[string]any `json:"items"`
}

// Create returns the whole body: the invoice in "data", its shareable address in "url".
func (s *InvoicesService) Create(ctx context.Context, in InvoiceInput, idempotencyKey string) (json.RawMessage, error) {
	return s.c.doRaw(ctx, http.MethodPost, "/payments/invoices", Request{Body: in, IdempotencyKey: idempotencyKey})
}

func (s *InvoicesService) List(ctx context.Context, query url.Values) (json.RawMessage, error) {
	return s.c.doRaw(ctx, http.MethodGet, "/payments/invoices", Request{Query: query})
}

// Get returns the invoice object (the response's "data").
func (s *InvoicesService) Get(ctx context.Context, id string) (json.RawMessage, error) {
	resp, err := s.c.Do(ctx, http.MethodGet, "/payments/invoices/"+url.PathEscape(id), Request{}, nil)
	if err != nil {
		return nil, err
	}
	return resp.Data, nil
}

func (s *InvoicesService) Cancel(ctx context.Context, id, idempotencyKey string) (json.RawMessage, error) {
	resp, err := s.c.Do(ctx, http.MethodPost, "/payments/invoices/"+url.PathEscape(id)+"/cancel", Request{IdempotencyKey: idempotencyKey}, nil)
	if err != nil {
		return nil, err
	}
	return resp.Data, nil
}
