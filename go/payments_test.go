package rigepay

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"sync"
	"testing"
	"time"
)

type recorded struct {
	Method string
	Path   string
	Query  url.Values
	Header http.Header
	Body   map[string]any
}

// recorder answers every request with the next scripted response (default 200 with an empty data object).
type recorder struct {
	mu        sync.Mutex
	calls     []recorded
	responses []func(w http.ResponseWriter, r *http.Request)
}

func (rc *recorder) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	raw, _ := io.ReadAll(r.Body)
	var body map[string]any
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &body)
	}
	rc.mu.Lock()
	rc.calls = append(rc.calls, recorded{r.Method, r.URL.Path, r.URL.Query(), r.Header.Clone(), body})
	var next func(w http.ResponseWriter, r *http.Request)
	if len(rc.responses) > 0 {
		next, rc.responses = rc.responses[0], rc.responses[1:]
	}
	rc.mu.Unlock()
	if next != nil {
		next(w, r)
		return
	}
	_, _ = w.Write([]byte(`{"status":"success","data":{}}`))
}

func reply(status int, body string) func(w http.ResponseWriter, r *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}
}

func testClient(t *testing.T, rc *recorder) *Client {
	t.Helper()
	srv := httptest.NewServer(rc)
	t.Cleanup(srv.Close)
	c := New("k")
	c.BaseURL = srv.URL
	return c
}

func TestPaymentsMethodsSendTheDocumentedRequest(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name   string
		call   func(c *Client) error
		method string
		path   string
		query  url.Values
		body   map[string]any
	}{
		{"WhoAmI", func(c *Client) error { _, err := c.WhoAmI(ctx); return err }, "GET", "/payments/whoami", nil, nil},
		{"Collections.STKPush", func(c *Client) error {
			_, err := c.Collections.STKPush(ctx, STKPushInput{AmountCents: 1000, CustomerPhone: "+254700000000", Remarks: "Order 1", Channel: "mpesa"}, "")
			return err
		}, "POST", "/payments/collections/stk-push", nil, map[string]any{"amount_cents": 1000.0, "customer_phone": "+254700000000", "remarks": "Order 1", "channel": "mpesa"}},
		{"Checkout.CreateSession", func(c *Client) error {
			_, err := c.Checkout.CreateSession(ctx, CheckoutSessionInput{AmountCents: 2500, AllowedRails: []string{"mpesa_stk", "card"}}, "")
			return err
		}, "POST", "/payments/checkout/sessions", nil, map[string]any{"amount_cents": 2500.0, "allowed_rails": []any{"mpesa_stk", "card"}}},
		{"Checkout.GetSession", func(c *Client) error { _, err := c.Checkout.GetSession(ctx, "s1"); return err }, "GET", "/payments/checkout/sessions/s1", nil, nil},
		{"Payouts.Create", func(c *Client) error {
			_, err := c.Payouts.Create(ctx, PayoutInput{AmountCents: 15000, RecipientName: "Jane", Remarks: "May", DestinationType: "PAYBILL", Shortcode: "123456", AccountReference: "ACC1"}, "")
			return err
		}, "POST", "/payments/payouts", nil, map[string]any{"amount": 15000.0, "recipient_name": "Jane", "remarks": "May", "destination_type": "PAYBILL", "shortcode": "123456", "account_reference": "ACC1"}},
		{"Payouts.FeeEstimate", func(c *Client) error { _, err := c.Payouts.FeeEstimate(ctx, 15000, "BANK_ACCOUNT"); return err }, "GET", "/payments/payouts/fee-estimate", url.Values{"amount_cents": {"15000"}, "destination_type": {"BANK_ACCOUNT"}}, nil},
		{"Payouts.ApprovalCeiling", func(c *Client) error { _, err := c.Payouts.ApprovalCeiling(ctx, 9000000); return err }, "GET", "/payments/payouts/approval-ceiling", url.Values{"amount_cents": {"9000000"}}, nil},
		{"BulkPayouts.Create", func(c *Client) error {
			_, err := c.BulkPayouts.Create(ctx, BulkPayoutInput{FundingSource: "MAIN", Items: []BulkPayoutItem{{AmountCents: 100, RecipientName: "A", PhoneNumber: "0700000001"}, {AmountCents: 200, RecipientName: "B", DestinationType: "BANK_ACCOUNT", BankCode: "01", BankAccountNumber: "123"}}}, "")
			return err
		}, "POST", "/payments/bulk-payouts", nil, map[string]any{"funding_source": "MAIN", "items": []any{
			map[string]any{"amount_cents": 100.0, "recipient_name": "A", "phone_number": "0700000001"},
			map[string]any{"amount_cents": 200.0, "recipient_name": "B", "destination_type": "BANK_ACCOUNT", "bank_code": "01", "bank_account_number": "123"},
		}}},
		{"BulkPayouts.List", func(c *Client) error { _, err := c.BulkPayouts.List(ctx); return err }, "GET", "/payments/bulk-payouts", nil, nil},
		{"BulkPayouts.Get", func(c *Client) error { _, err := c.BulkPayouts.Get(ctx, "b1"); return err }, "GET", "/payments/bulk-payouts/b1", nil, nil},
		{"Transfers.Create", func(c *Client) error {
			_, err := c.Transfers.Create(ctx, TransferInput{From: "org", To: "br1", AmountCents: 1000, Remarks: "float"}, "")
			return err
		}, "POST", "/payments/transfers", nil, map[string]any{"from": "org", "to": "br1", "amount": 1000.0, "remarks": "float"}},
		{"Transfers.Lookup", func(c *Client) error { _, err := c.Transfers.Lookup(ctx, "ACME01"); return err }, "GET", "/payments/transfers/lookup", url.Values{"code": {"ACME01"}}, nil},
		{"Transfers.StartExternal", func(c *Client) error {
			_, err := c.Transfers.StartExternal(ctx, ExternalTransferInput{RecipientCollectionCode: "ACME01", AmountCents: 5000, Remarks: "stock"}, "")
			return err
		}, "POST", "/payments/transfers/external", nil, map[string]any{"recipient_collection_code": "ACME01", "amount": 5000.0, "remarks": "stock"}},
		{"Transfers.ConfirmExternal", func(c *Client) error { _, err := c.Transfers.ConfirmExternal(ctx, "t1", "123456", ""); return err }, "POST", "/payments/transfers/external/confirm", nil, map[string]any{"transfer_id": "t1", "otp": "123456"}},
		{"Transfers.CancelExternal", func(c *Client) error { _, err := c.Transfers.CancelExternal(ctx, "t1"); return err }, "POST", "/payments/transfers/external/cancel", nil, map[string]any{"transfer_id": "t1"}},
		{"Balance.Get", func(c *Client) error { _, err := c.Balance.Get(ctx, "br1"); return err }, "GET", "/payments/balance", url.Values{"branch_id": {"br1"}}, nil},
		{"Transactions.List", func(c *Client) error {
			_, err := c.Transactions.List(ctx, url.Values{"page": {"2"}, "direction": {"CREDIT"}})
			return err
		}, "GET", "/payments/transactions", url.Values{"page": {"2"}, "direction": {"CREDIT"}}, nil},
		{"Transactions.Get", func(c *Client) error { _, err := c.Transactions.Get(ctx, "x1"); return err }, "GET", "/payments/transactions/x1", nil, nil},
		{"PaymentLinks.Create", func(c *Client) error {
			_, err := c.PaymentLinks.Create(ctx, PaymentLinkInput{AmountCents: 1000, Currency: "KES", Name: "Deposit", IsReusable: true, ExpiresInDays: 7}, "")
			return err
		}, "POST", "/payments/payment-links", nil, map[string]any{"amount_cents": 1000.0, "currency": "KES", "name": "Deposit", "is_reusable": true, "expires_in_days": 7.0}},
		{"PaymentLinks.List", func(c *Client) error { _, err := c.PaymentLinks.List(ctx, url.Values{"page": {"1"}}); return err }, "GET", "/payments/payment-links", url.Values{"page": {"1"}}, nil},
		{"PaymentLinks.Get", func(c *Client) error { _, err := c.PaymentLinks.Get(ctx, "l1"); return err }, "GET", "/payments/payment-links/l1", nil, nil},
		{"PaymentLinks.Close", func(c *Client) error { _, err := c.PaymentLinks.Close(ctx, "l1", ""); return err }, "POST", "/payments/payment-links/l1/close", nil, nil},
		{"Invoices.Create", func(c *Client) error {
			_, err := c.Invoices.Create(ctx, InvoiceInput{CustomerName: "Acme", CustomerPhone: "0700000000", Currency: "KES", DueDate: "2026-12-01T00:00:00+03:00", Items: []map[string]any{{"description": "Widget", "quantity": 2}}}, "")
			return err
		}, "POST", "/payments/invoices", nil, map[string]any{"customer_name": "Acme", "customer_phone": "0700000000", "currency": "KES", "due_date": "2026-12-01T00:00:00+03:00", "items": []any{map[string]any{"description": "Widget", "quantity": 2.0}}}},
		{"Invoices.List", func(c *Client) error { _, err := c.Invoices.List(ctx, url.Values{"status": {"PAID"}}); return err }, "GET", "/payments/invoices", url.Values{"status": {"PAID"}}, nil},
		{"Invoices.Get", func(c *Client) error { _, err := c.Invoices.Get(ctx, "i1"); return err }, "GET", "/payments/invoices/i1", nil, nil},
		{"Invoices.Cancel", func(c *Client) error { _, err := c.Invoices.Cancel(ctx, "i1", ""); return err }, "POST", "/payments/invoices/i1/cancel", nil, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rc := &recorder{}
			c := testClient(t, rc)
			if tc.name == "BulkPayouts.List" {
				rc.responses = append(rc.responses, reply(200, `{"status":"success","data":[{"id":"b1","total_amount_cents":300}]}`))
			}
			if tc.name == "BulkPayouts.Create" {
				rc.responses = append(rc.responses, reply(201, `{"status":"success","data":{"id":"b1"}}`))
			}
			if err := tc.call(c); err != nil {
				t.Fatal(err)
			}
			if len(rc.calls) != 1 {
				t.Fatalf("got %d calls", len(rc.calls))
			}
			got := rc.calls[0]
			if got.Method != tc.method || got.Path != tc.path {
				t.Fatalf("got %s %s", got.Method, got.Path)
			}
			want := tc.query
			if want == nil {
				want = url.Values{}
			}
			if !reflect.DeepEqual(got.Query, want) {
				t.Fatalf("query %v want %v", got.Query, want)
			}
			if !reflect.DeepEqual(got.Body, tc.body) {
				t.Fatalf("body %#v want %#v", got.Body, tc.body)
			}
			if got.Header.Get("Authorization") != "Bearer k" {
				t.Fatalf("auth %q", got.Header.Get("Authorization"))
			}
			key := got.Header.Get("X-Idempotency-Key")
			if (tc.method == "POST") != (key != "") {
				t.Fatalf("%s idempotency key %q", tc.method, key)
			}
		})
	}
}

func TestFlatResponsesAndIntegerCents(t *testing.T) {
	ctx := context.Background()
	rc := &recorder{responses: []func(http.ResponseWriter, *http.Request){
		reply(202, `{"status":"approval_required","payout_id":"p1","approval_id":"a1","amount":15000,"fee_cents":50}`),
		reply(202, `{"status":"otp_required","transfer_id":"t1","expires_in":600}`),
		reply(200, `{"status":"success","data":{"total_count":1,"page":1,"page_size":20,"total_pages":1,"transactions":[{"id":"x1","amountCents":9007199254740993,"feeCents":5}]}}`),
	}}
	c := testClient(t, rc)
	p, err := c.Payouts.Create(ctx, PayoutInput{AmountCents: 15000, RecipientName: "J", Remarks: "r", PhoneNumber: "0700000000"}, "")
	if err != nil || p.Status != "approval_required" || p.ApprovalID != "a1" || p.AmountCents != 15000 || p.FeeCents != 50 {
		t.Fatalf("payout %+v %v", p, err)
	}
	s, err := c.Transfers.StartExternal(ctx, ExternalTransferInput{RecipientCollectionCode: "C", AmountCents: 1000, Remarks: "r"}, "")
	if err != nil || s.Status != "otp_required" || s.TransferID != "t1" || s.ExpiresIn != 600 {
		t.Fatalf("start %+v %v", s, err)
	}
	page, err := c.Transactions.List(ctx, nil)
	if err != nil || len(page.Transactions) != 1 || page.Transactions[0].AmountCents != 9007199254740993 {
		t.Fatalf("cents must decode exactly into int64: %+v %v", page, err)
	}
}

func TestPayoutRetryReusesTheIdempotencyKey(t *testing.T) {
	rc := &recorder{responses: []func(http.ResponseWriter, *http.Request){
		reply(503, `{}`),
		reply(504, `{}`),
		reply(202, `{"status":"success","payout_id":"p1"}`),
	}}
	c := testClient(t, rc)
	p, err := c.Payouts.Create(context.Background(), PayoutInput{AmountCents: 100, RecipientName: "J", Remarks: "r", PhoneNumber: "0700000000"}, "")
	if err != nil || p.PayoutID != "p1" {
		t.Fatalf("%+v %v", p, err)
	}
	if len(rc.calls) != 3 {
		t.Fatalf("got %d calls", len(rc.calls))
	}
	k := rc.calls[0].Header.Get("X-Idempotency-Key")
	for _, call := range rc.calls {
		if k == "" || call.Header.Get("X-Idempotency-Key") != k {
			t.Fatal("every retry must carry the same idempotency key")
		}
	}
}

// oauthServer issues at-1, at-2, ... from /oauth/token and answers other paths with apiStatuses in order (then 200).
func oauthServer(t *testing.T, apiStatuses ...int) (*recorder, string) {
	t.Helper()
	rc := &recorder{}
	issued := 0
	var mu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/oauth/token" {
			mu.Lock()
			issued++
			n := issued
			mu.Unlock()
			rc.ServeHTTP(httptest.NewRecorder(), r)
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "at-" + string(rune('0'+n)), "token_type": "Bearer", "expires_in": 3600, "scope": "payouts:read"})
			return
		}
		status := 200
		mu.Lock()
		if len(apiStatuses) > 0 {
			status, apiStatuses = apiStatuses[0], apiStatuses[1:]
		}
		mu.Unlock()
		rc.ServeHTTP(httptest.NewRecorder(), r)
		w.WriteHeader(status)
		_, _ = w.Write([]byte(`{"status":"success","data":{}}`))
	}))
	t.Cleanup(srv.Close)
	return rc, srv.URL
}

func countPath(calls []recorded, path string) int {
	n := 0
	for _, c := range calls {
		if c.Path == path {
			n++
		}
	}
	return n
}

func TestClientCredentialsTokenIsCachedUntilShortlyBeforeExpiry(t *testing.T) {
	ctx := context.Background()
	rc, base := oauthServer(t)
	now := time.Unix(1_000_000, 0)
	c := NewWithClientCredentials("cid", "csecret")
	c.BaseURL = base
	c.Now = func() time.Time { return now }

	var wg sync.WaitGroup
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _, _ = c.WhoAmI(ctx) }()
	}
	wg.Wait()
	if _, err := c.Balance.Get(ctx, ""); err != nil {
		t.Fatal(err)
	}
	if n := countPath(rc.calls, "/oauth/token"); n != 1 {
		t.Fatalf("want 1 token request, got %d", n)
	}
	tok := rc.calls[0]
	if tok.Method != "POST" || tok.Header.Get("Authorization") != "" || tok.Header.Get("X-Idempotency-Key") != "" {
		t.Fatalf("token request must be unauthenticated: %+v", tok)
	}
	if !reflect.DeepEqual(tok.Body, map[string]any{"grant_type": "client_credentials", "client_id": "cid", "client_secret": "csecret"}) {
		t.Fatalf("token body %v", tok.Body)
	}
	for _, call := range rc.calls[1:] {
		if call.Header.Get("Authorization") != "Bearer at-1" {
			t.Fatalf("api call auth %q", call.Header.Get("Authorization"))
		}
	}

	now = now.Add((3600 - 61) * time.Second)
	_, _ = c.WhoAmI(ctx)
	if n := countPath(rc.calls, "/oauth/token"); n != 1 {
		t.Fatalf("still inside the cache window, got %d token requests", n)
	}
	now = now.Add(2 * time.Second)
	_, _ = c.WhoAmI(ctx)
	if n := countPath(rc.calls, "/oauth/token"); n != 2 {
		t.Fatalf("must refresh within a minute of expiry, got %d token requests", n)
	}
	if got := rc.calls[len(rc.calls)-1].Header.Get("Authorization"); got != "Bearer at-2" {
		t.Fatalf("after refresh auth %q", got)
	}
}

func TestClientCredentials401RefreshesOnceWithTheSameKey(t *testing.T) {
	rc, base := oauthServer(t, 401, 200)
	c := NewWithClientCredentials("cid", "s")
	c.BaseURL = base
	if _, err := c.Collections.STKPush(context.Background(), STKPushInput{AmountCents: 1000, CustomerPhone: "0700000000"}, ""); err != nil {
		t.Fatal(err)
	}
	var api []recorded
	for _, call := range rc.calls {
		if call.Path != "/oauth/token" {
			api = append(api, call)
		}
	}
	if len(api) != 2 || api[0].Header.Get("Authorization") != "Bearer at-1" || api[1].Header.Get("Authorization") != "Bearer at-2" {
		t.Fatalf("calls %+v", api)
	}
	if api[0].Header.Get("X-Idempotency-Key") != api[1].Header.Get("X-Idempotency-Key") {
		t.Fatal("the refreshed retry must reuse the idempotency key")
	}

	_, base2 := oauthServer(t, 401, 401)
	c2 := NewWithClientCredentials("cid", "s")
	c2.BaseURL = base2
	_, err := c2.WhoAmI(context.Background())
	if apiErr, ok := err.(*Error); !ok || apiErr.Status != 401 {
		t.Fatalf("a second 401 must be returned, got %v", err)
	}
}

func TestOAuthErrorCodeComesFromTheErrorField(t *testing.T) {
	rc := &recorder{responses: []func(http.ResponseWriter, *http.Request){
		reply(401, `{"status":"error","error":"invalid_client","message":"bad secret"}`),
	}}
	c := testClient(t, rc)
	_, err := c.OAuth.CreateToken(context.Background(), "a", "b")
	if apiErr, ok := err.(*Error); !ok || apiErr.Status != 401 || apiErr.Code != "invalid_client" {
		t.Fatalf("got %v", err)
	}
}
