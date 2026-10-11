package rigepay

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"
	"time"
)

type vectorFile struct {
	Signature struct {
		Secret    string `json:"secret"`
		URL       string `json:"url"`
		Timestamp string `json:"timestamp"`
		RequestID string `json:"request_id"`
		Body      string `json:"body"`
		Signature string `json:"signature"`
	} `json:"signature"`
}

func loadVectors(t *testing.T) vectorFile {
	t.Helper()
	raw, err := os.ReadFile("../vectors.json")
	if err != nil {
		t.Fatal(err)
	}
	var v vectorFile
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatal(err)
	}
	return v
}

func TestSignMatchesTheServerSignerVector(t *testing.T) {
	v := loadVectors(t).Signature
	if got := Sign(v.Secret, "POST", v.URL, v.Timestamp, v.RequestID, []byte(v.Body)); got != v.Signature {
		t.Fatalf("got %s want %s", got, v.Signature)
	}
}

func TestVerifyWebhook(t *testing.T) {
	v := loadVectors(t).Signature
	h := http.Header{}
	h.Set("X-RigePay-Timestamp", v.Timestamp)
	h.Set("X-RigePay-Request-ID", v.RequestID)
	h.Set("X-RigePay-Signature", v.Signature)
	now := time.Unix(1769424764+10, 0)
	if !VerifyWebhook(v.Secret, v.URL, h, []byte(v.Body), 5*time.Minute, now) {
		t.Fatal("the pinned delivery must verify")
	}
	if VerifyWebhook(v.Secret, v.URL, h, []byte(v.Body+" "), 5*time.Minute, now) {
		t.Fatal("a changed body must not verify")
	}
	if VerifyWebhook(v.Secret, "https://yourapp.example.com/webhooks/rigepay", h, []byte(v.Body), 5*time.Minute, now) {
		t.Fatal("the URL is part of the signed message")
	}
	if VerifyWebhook(v.Secret, v.URL, h, []byte(v.Body), 5*time.Minute, time.Unix(1769424764+301, 0)) {
		t.Fatal("an old timestamp must not verify")
	}
}

func TestRetryReusesTheIdempotencyKey(t *testing.T) {
	var calls int32
	var keys []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		keys = append(keys, r.Header.Get("X-Idempotency-Key"))
		if atomic.AddInt32(&calls, 1) == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "success", "data": map[string]any{"id": "e1"}})
	}))
	defer srv.Close()
	c := New("t")
	c.BaseURL = srv.URL
	out, err := c.Escrows.Create(context.Background(), map[string]any{"reference": "r"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if out["id"] != "e1" || len(keys) != 2 || keys[0] == "" || keys[0] != keys[1] {
		t.Fatalf("out=%v keys=%v", out, keys)
	}
}

func TestErrorsCarryTheCodeAndAreNotRetried(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"status":"error","code":"dispute_active","message":"frozen"}`))
	}))
	defer srv.Close()
	c := New("t")
	c.BaseURL = srv.URL
	_, err := c.Escrows.Release(context.Background(), "e1", SettleOptions{MilestoneID: "m1"})
	apiErr, ok := err.(*Error)
	if !ok || apiErr.Code != "dispute_active" || apiErr.Status != 409 {
		t.Fatalf("unexpected error %v", err)
	}
	if calls != 1 {
		t.Fatalf("a 409 must not be retried, got %d calls", calls)
	}
}
