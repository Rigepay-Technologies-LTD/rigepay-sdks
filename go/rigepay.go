// Package rigepay is a dependency-free client for the RigePay API with idempotency keys,
// retries that reuse the key, and webhook signature verification.
package rigepay

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	mrand "math/rand"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

const DefaultBaseURL = "https://api.rigepay.co.ke/api/v1"

type Client struct {
	Token      string
	BaseURL    string
	MaxRetries int
	HTTP       *http.Client
	Escrows    *EscrowsService

	// ClientID and ClientSecret, set by NewWithClientCredentials, make the client fetch an OAuth
	// access token from /oauth/token and cache it until TokenRefreshSkew before it expires.
	// They are used only when Token is empty.
	ClientID         string
	ClientSecret     string
	TokenRefreshSkew time.Duration
	// Now is the clock used for the token cache. Nil means time.Now.
	Now func() time.Time

	OAuth        *OAuthService
	Collections  *CollectionsService
	Checkout     *CheckoutService
	Payouts      *PayoutsService
	BulkPayouts  *BulkPayoutsService
	Transfers    *TransfersService
	Balance      *BalanceService
	Transactions *TransactionsService
	PaymentLinks *PaymentLinksService
	Invoices     *InvoicesService

	tokenMu      sync.Mutex
	accessToken  string
	tokenRefresh time.Time
}

func New(token string) *Client {
	c := &Client{
		Token:      token,
		BaseURL:    DefaultBaseURL,
		MaxRetries: 3,
		HTTP: &http.Client{
			Timeout: 30 * time.Second,
		},
		TokenRefreshSkew: time.Minute,
	}
	c.Escrows = &EscrowsService{c: c}
	c.OAuth = &OAuthService{c: c}
	c.Collections = &CollectionsService{c: c}
	c.Checkout = &CheckoutService{c: c}
	c.Payouts = &PayoutsService{c: c}
	c.BulkPayouts = &BulkPayoutsService{c: c}
	c.Transfers = &TransfersService{c: c}
	c.Balance = &BalanceService{c: c}
	c.Transactions = &TransactionsService{c: c}
	c.PaymentLinks = &PaymentLinksService{c: c}
	c.Invoices = &InvoicesService{c: c}
	return c
}

// NewWithClientCredentials returns a client that authenticates with an OAuth2 client-credentials
// access token, fetched on first use and cached until shortly before it expires.
func NewWithClientCredentials(clientID, clientSecret string) *Client {
	c := New("")
	c.ClientID = clientID
	c.ClientSecret = clientSecret
	return c
}

func (c *Client) usesClientCredentials() bool { return c.Token == "" && c.ClientID != "" }

func (c *Client) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

// BearerToken returns the static Token, or a cached client-credentials access token.
func (c *Client) BearerToken(ctx context.Context) (string, error) {
	if !c.usesClientCredentials() {
		return c.Token, nil
	}
	c.tokenMu.Lock()
	defer c.tokenMu.Unlock()
	if c.accessToken != "" && c.now().Before(c.tokenRefresh) {
		return c.accessToken, nil
	}
	t, err := c.OAuth.CreateToken(ctx, c.ClientID, c.ClientSecret)
	if err != nil {
		return "", err
	}
	c.accessToken = t.AccessToken
	c.tokenRefresh = c.now().Add(time.Duration(t.ExpiresIn)*time.Second - c.TokenRefreshSkew)
	return c.accessToken, nil
}

// invalidateToken drops the cached access token, but only if it is still the one that was refused,
// so a concurrent request's late 401 does not discard a token another request just fetched.
func (c *Client) invalidateToken(refused string) {
	c.tokenMu.Lock()
	if c.accessToken == refused {
		c.accessToken = ""
	}
	c.tokenMu.Unlock()
}

type Error struct {
	Status  int
	Code    string
	Message string
	Body    json.RawMessage
}

func (e *Error) Error() string {
	if e.Code != "" {
		return fmt.Sprintf("rigepay: %d %s: %s", e.Status, e.Code, e.Message)
	}
	return fmt.Sprintf("rigepay: %d: %s", e.Status, e.Message)
}

type Response struct {
	Status  string          `json:"status"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
	Paging  json.RawMessage `json:"paging"`
	// Raw is the whole response body, for endpoints that answer without a data envelope.
	Raw json.RawMessage `json:"-"`
}

type Request struct {
	Body           any
	Query          url.Values
	IdempotencyKey string
	// NoAuth sends no Authorization or idempotency header (used for /oauth/token).
	NoAuth bool
}

func newKey() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}

func backoff(attempt int, retryAfter time.Duration) time.Duration {
	if retryAfter > 0 {
		if retryAfter > 30*time.Second {
			return 30 * time.Second
		}
		return retryAfter
	}
	base := time.Duration(math.Min(float64(500*time.Millisecond)*math.Pow(2, float64(attempt)), float64(8*time.Second)))
	return base/2 + time.Duration(mrand.Int63n(int64(base/2)+1))
}

func (c *Client) Do(ctx context.Context, method, path string, req Request, out any) (*Response, error) {
	u := strings.TrimRight(c.BaseURL, "/") + path
	if len(req.Query) > 0 {
		u += "?" + req.Query.Encode()
	}
	var payload []byte
	if req.Body != nil {
		b, err := json.Marshal(req.Body)
		if err != nil {
			return nil, err
		}
		payload = b
	}
	key := req.IdempotencyKey
	if method != http.MethodGet && key == "" && !req.NoAuth {
		key = newKey()
	}
	refreshed := false
	for attempt := 0; ; attempt++ {
		hr, err := http.NewRequestWithContext(ctx, method, u, bytes.NewReader(payload))
		if err != nil {
			return nil, err
		}
		var bearer string
		if !req.NoAuth {
			bearer, err = c.BearerToken(ctx)
			if err != nil {
				return nil, err
			}
			hr.Header.Set("Authorization", "Bearer "+bearer)
		}
		hr.Header.Set("Accept", "application/json")
		if payload != nil {
			hr.Header.Set("Content-Type", "application/json")
		}
		if key != "" && method != http.MethodGet {
			hr.Header.Set("X-Idempotency-Key", key)
		}
		resp, err := c.HTTP.Do(hr)
		if err != nil {
			if attempt >= c.MaxRetries || ctx.Err() != nil {
				return nil, err
			}
			if !sleep(ctx, backoff(attempt, 0)) {
				return nil, ctx.Err()
			}
			continue
		}
		raw, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode == http.StatusUnauthorized && !req.NoAuth && c.usesClientCredentials() && !refreshed {
			// The cached access token was revoked or expired early: fetch a new one once, same key.
			refreshed = true
			c.invalidateToken(bearer)
			continue
		}
		retryable := resp.StatusCode == 429 || resp.StatusCode == 502 || resp.StatusCode == 503 || resp.StatusCode == 504
		if retryable && attempt < c.MaxRetries {
			ra, _ := strconv.Atoi(resp.Header.Get("Retry-After"))
			if !sleep(ctx, backoff(attempt, time.Duration(ra)*time.Second)) {
				return nil, ctx.Err()
			}
			continue
		}
		var env struct {
			Response
			Code     string `json:"code"`
			OAuthErr string `json:"error"`
		}
		_ = json.Unmarshal(raw, &env)
		env.Raw = raw
		if resp.StatusCode >= 400 {
			code := env.Code
			if code == "" {
				code = env.OAuthErr
			}
			return nil, &Error{
				Status:  resp.StatusCode,
				Code:    code,
				Message: env.Message,
				Body:    raw,
			}
		}
		if out != nil && len(env.Data) > 0 {
			if err := json.Unmarshal(env.Data, out); err != nil {
				return nil, err
			}
		}
		return &env.Response, nil
	}
}

func sleep(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

type EscrowsService struct{ c *Client }

type Escrow = map[string]any

func (s *EscrowsService) Create(ctx context.Context, input map[string]any, idempotencyKey string) (Escrow, error) {
	var out Escrow
	_, err := s.c.Do(ctx, http.MethodPost, "/escrows", Request{Body: input, IdempotencyKey: idempotencyKey}, &out)
	return out, err
}

func (s *EscrowsService) Get(ctx context.Context, id string) (Escrow, error) {
	var out Escrow
	_, err := s.c.Do(ctx, http.MethodGet, "/escrows/"+id, Request{}, &out)
	return out, err
}

func (s *EscrowsService) Fund(ctx context.Context, id, idempotencyKey string) (Escrow, error) {
	var out Escrow
	_, err := s.c.Do(ctx, http.MethodPost, "/escrows/"+id+"/fund", Request{IdempotencyKey: idempotencyKey}, &out)
	return out, err
}

type SettleOptions struct {
	AmountCents    int64
	MilestoneID    string
	ConfirmationID string
	Remarks        string
	IdempotencyKey string
}

func (o SettleOptions) body() map[string]any {
	b := map[string]any{}
	if o.AmountCents > 0 {
		b["amount_cents"] = o.AmountCents
	}
	if o.MilestoneID != "" {
		b["milestone_id"] = o.MilestoneID
	}
	if o.ConfirmationID != "" {
		b["confirmation_id"] = o.ConfirmationID
	}
	if o.Remarks != "" {
		b["remarks"] = o.Remarks
	}
	return b
}

func (s *EscrowsService) Release(ctx context.Context, id string, o SettleOptions) (Escrow, error) {
	var out Escrow
	_, err := s.c.Do(ctx, http.MethodPost, "/escrows/"+id+"/release", Request{Body: o.body(), IdempotencyKey: o.IdempotencyKey}, &out)
	return out, err
}

func (s *EscrowsService) Refund(ctx context.Context, id string, o SettleOptions) (Escrow, error) {
	var out Escrow
	_, err := s.c.Do(ctx, http.MethodPost, "/escrows/"+id+"/refund", Request{Body: o.body(), IdempotencyKey: o.IdempotencyKey}, &out)
	return out, err
}

func (s *EscrowsService) Cancel(ctx context.Context, id, idempotencyKey string) (Escrow, error) {
	var out Escrow
	_, err := s.c.Do(ctx, http.MethodPost, "/escrows/"+id+"/cancel", Request{IdempotencyKey: idempotencyKey}, &out)
	return out, err
}

func (s *EscrowsService) OpenDispute(ctx context.Context, id, reason, idempotencyKey string) (map[string]any, error) {
	var out map[string]any
	_, err := s.c.Do(ctx, http.MethodPost, "/escrows/"+id+"/disputes", Request{Body: map[string]any{"reason": reason}, IdempotencyKey: idempotencyKey}, &out)
	return out, err
}

func (s *EscrowsService) Concede(ctx context.Context, id, disputeID, idempotencyKey string) (map[string]any, error) {
	var out map[string]any
	_, err := s.c.Do(ctx, http.MethodPost, "/escrows/"+id+"/disputes/"+disputeID+"/concede", Request{IdempotencyKey: idempotencyKey}, &out)
	return out, err
}

func Sign(secret, method, rawURL, timestamp, requestID string, body []byte) string {
	sum := sha256.Sum256(body)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(method + ":" + rawURL + ":" + timestamp + ":" + requestID + ":" + hex.EncodeToString(sum[:])))
	return hex.EncodeToString(mac.Sum(nil))
}

// VerifyWebhook checks a delivery. rawURL must be the endpoint URL exactly as registered with RigePay.
func VerifyWebhook(secret, rawURL string, header http.Header, body []byte, tolerance time.Duration, now time.Time) bool {
	ts := header.Get("X-RigePay-Timestamp")
	reqID := header.Get("X-RigePay-Request-ID")
	sig := header.Get("X-RigePay-Signature")
	if ts == "" || reqID == "" || sig == "" {
		return false
	}
	expected := Sign(secret, http.MethodPost, rawURL, ts, reqID, body)
	if !hmac.Equal([]byte(expected), []byte(sig)) {
		return false
	}
	unix, err := strconv.ParseInt(ts, 10, 64)
	if err != nil {
		return false
	}
	delta := now.Sub(time.Unix(unix, 0))
	if delta < 0 {
		delta = -delta
	}
	return delta <= tolerance
}
