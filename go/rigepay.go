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
	"time"
)

const DefaultBaseURL = "https://api.rigepay.co.ke/api/v1"

type Client struct {
	Token      string
	BaseURL    string
	MaxRetries int
	HTTP       *http.Client
	Escrows    *EscrowsService
}

func New(token string) *Client {
	c := &Client{Token: token, BaseURL: DefaultBaseURL, MaxRetries: 3, HTTP: &http.Client{Timeout: 30 * time.Second}}
	c.Escrows = &EscrowsService{c: c}
	return c
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
}

type Request struct {
	Body           any
	Query          url.Values
	IdempotencyKey string
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
	if method != http.MethodGet && key == "" {
		key = newKey()
	}
	for attempt := 0; ; attempt++ {
		hr, err := http.NewRequestWithContext(ctx, method, u, bytes.NewReader(payload))
		if err != nil {
			return nil, err
		}
		hr.Header.Set("Authorization", "Bearer "+c.Token)
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
			Code string `json:"code"`
		}
		_ = json.Unmarshal(raw, &env)
		if resp.StatusCode >= 400 {
			return nil, &Error{Status: resp.StatusCode, Code: env.Code, Message: env.Message, Body: raw}
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
