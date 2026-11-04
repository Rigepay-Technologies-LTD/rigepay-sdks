import io
import json
import os
import sys
import unittest
import urllib.error
import urllib.parse

sys.path.insert(0, os.path.join(os.path.dirname(__file__), ".."))

from rigepay import Client, RigePayError  # noqa: E402

BASE = "https://api.rigepay.co.ke/api/v1"


class FakeResponse:
    def __init__(self, status, body):
        self.status = status
        self._body = json.dumps(body).encode()

    def read(self):
        return self._body

    def __enter__(self):
        return self

    def __exit__(self, *args):
        return False


def scripted(responses, calls, default=(200, {"status": "success", "data": {}})):
    def opener(req, timeout=None):
        calls.append(req)
        status, body = responses.pop(0) if responses else default
        if status >= 400:
            raise urllib.error.HTTPError(req.full_url, status, "error", {}, io.BytesIO(json.dumps(body).encode()))
        return FakeResponse(status, body)

    return opener


def token_server(calls, api_statuses=()):
    statuses = list(api_statuses)
    issued = [0]

    def opener(req, timeout=None):
        calls.append(req)
        if req.full_url.endswith("/oauth/token"):
            issued[0] += 1
            return FakeResponse(200, {"access_token": f"at-{issued[0]}", "token_type": "Bearer", "expires_in": 3600, "scope": "x"})
        status = statuses.pop(0) if statuses else 200
        if status >= 400:
            raise urllib.error.HTTPError(req.full_url, status, "error", {}, io.BytesIO(b'{"status":"error","message":"unauthorized"}'))
        return FakeResponse(200, {"status": "success", "data": {}})

    return opener


def split(req):
    u = urllib.parse.urlsplit(req.full_url)
    return u.scheme + "://" + u.netloc + u.path, dict(urllib.parse.parse_qsl(u.query))


# (name, call, method, path, query, body)
CASES = [
    ("whoami", lambda c: c.whoami(), "GET", "/payments/whoami", {}, None),
    ("collections.stk_push", lambda c: c.collections.stk_push(1000, "+254700000000", remarks="Order 1", channel="mpesa"),
     "POST", "/payments/collections/stk-push", {}, {"amount_cents": 1000, "customer_phone": "+254700000000", "remarks": "Order 1", "channel": "mpesa"}),
    ("checkout.create_session", lambda c: c.checkout.create_session(2500, ["mpesa_stk", "card"], description="Cart"),
     "POST", "/payments/checkout/sessions", {}, {"amount_cents": 2500, "allowed_rails": ["mpesa_stk", "card"], "description": "Cart"}),
    ("checkout.get_session", lambda c: c.checkout.get_session("s1"), "GET", "/payments/checkout/sessions/s1", {}, None),
    ("payouts.create", lambda c: c.payouts.create(15000, "Jane", "May", destination_type="PAYBILL", shortcode="123456", account_reference="ACC1"),
     "POST", "/payments/payouts", {}, {"amount": 15000, "recipient_name": "Jane", "remarks": "May", "destination_type": "PAYBILL", "shortcode": "123456", "account_reference": "ACC1"}),
    ("payouts.fee_estimate", lambda c: c.payouts.fee_estimate(15000, "BANK_ACCOUNT"), "GET", "/payments/payouts/fee-estimate",
     {"amount_cents": "15000", "destination_type": "BANK_ACCOUNT"}, None),
    ("payouts.approval_ceiling", lambda c: c.payouts.approval_ceiling(9000000), "GET", "/payments/payouts/approval-ceiling", {"amount_cents": "9000000"}, None),
    ("bulk_payouts.create", lambda c: c.bulk_payouts.create([{"amount_cents": 100, "recipient_name": "A", "phone_number": "0700000001"},
                                                             {"amount_cents": 200, "recipient_name": "B"}], funding_source="MAIN"),
     "POST", "/payments/bulk-payouts", {}, {"funding_source": "MAIN", "items": [{"amount_cents": 100, "recipient_name": "A", "phone_number": "0700000001"},
                                                                               {"amount_cents": 200, "recipient_name": "B"}]}),
    ("bulk_payouts.list", lambda c: c.bulk_payouts.list(), "GET", "/payments/bulk-payouts", {}, None),
    ("bulk_payouts.get", lambda c: c.bulk_payouts.get("b1"), "GET", "/payments/bulk-payouts/b1", {}, None),
    ("transfers.create", lambda c: c.transfers.create("org", "br1", 1000, remarks="float"), "POST", "/payments/transfers", {},
     {"from": "org", "to": "br1", "amount": 1000, "remarks": "float"}),
    ("transfers.lookup", lambda c: c.transfers.lookup("ACME01"), "GET", "/payments/transfers/lookup", {"code": "ACME01"}, None),
    ("transfers.start_external", lambda c: c.transfers.start_external("ACME01", 5000, "stock"), "POST", "/payments/transfers/external", {},
     {"recipient_collection_code": "ACME01", "amount": 5000, "remarks": "stock"}),
    ("transfers.confirm_external", lambda c: c.transfers.confirm_external("t1", "123456"), "POST", "/payments/transfers/external/confirm", {},
     {"transfer_id": "t1", "otp": "123456"}),
    ("transfers.cancel_external", lambda c: c.transfers.cancel_external("t1"), "POST", "/payments/transfers/external/cancel", {}, {"transfer_id": "t1"}),
    ("balance.get", lambda c: c.balance.get("br1"), "GET", "/payments/balance", {"branch_id": "br1"}, None),
    ("transactions.list", lambda c: c.transactions.list(page=2, direction="CREDIT", min_amount=100), "GET", "/payments/transactions",
     {"page": "2", "direction": "CREDIT", "min_amount": "100"}, None),
    ("transactions.get", lambda c: c.transactions.get("x1"), "GET", "/payments/transactions/x1", {}, None),
    ("payment_links.create", lambda c: c.payment_links.create("KES", amount_cents=1000, name="Deposit", is_reusable=True, expires_in_days=7),
     "POST", "/payments/payment-links", {}, {"amount_cents": 1000, "currency": "KES", "name": "Deposit", "is_reusable": True, "expires_in_days": 7}),
    ("payment_links.list", lambda c: c.payment_links.list(page=1), "GET", "/payments/payment-links", {"page": "1"}, None),
    ("payment_links.get", lambda c: c.payment_links.get("l1"), "GET", "/payments/payment-links/l1", {}, None),
    ("payment_links.close", lambda c: c.payment_links.close("l1"), "POST", "/payments/payment-links/l1/close", {}, None),
    ("invoices.create", lambda c: c.invoices.create("Acme", "0700000000", "KES", "2026-12-01T00:00:00+03:00", [{"description": "Widget", "quantity": 2}]),
     "POST", "/payments/invoices", {}, {"customer_name": "Acme", "customer_phone": "0700000000", "currency": "KES",
                                        "due_date": "2026-12-01T00:00:00+03:00", "items": [{"description": "Widget", "quantity": 2}]}),
    ("invoices.list", lambda c: c.invoices.list(status="PAID"), "GET", "/payments/invoices", {"status": "PAID"}, None),
    ("invoices.get", lambda c: c.invoices.get("i1"), "GET", "/payments/invoices/i1", {}, None),
    ("invoices.cancel", lambda c: c.invoices.cancel("i1"), "POST", "/payments/invoices/i1/cancel", {}, None),
]


class PaymentsRequestTests(unittest.TestCase):
    def test_each_method_sends_the_documented_request(self):
        for name, call, method, path, query, body in CASES:
            with self.subTest(name):
                calls = []
                call(Client("k", opener=scripted([], calls), sleep=lambda s: None))
                self.assertEqual(len(calls), 1)
                req = calls[0]
                url, got_query = split(req)
                self.assertEqual(req.get_method(), method)
                self.assertEqual(url, BASE + path)
                self.assertEqual(got_query, query)
                self.assertEqual(None if req.data is None else json.loads(req.data), body)
                self.assertEqual(req.get_header("Authorization"), "Bearer k")
                key = req.get_header("X-idempotency-key")
                if method == "POST":
                    self.assertTrue(key, "every POST carries an idempotency key")
                else:
                    self.assertIsNone(key)

    def test_flat_bodies_come_back_whole(self):
        calls = []
        client = Client("k", opener=scripted([
            (202, {"status": "approval_required", "payout_id": "p1", "approval_id": "a1", "amount": 15000, "fee_cents": 50}),
            (202, {"status": "otp_required", "transfer_id": "t1", "expires_in": 600}),
            (201, {"status": "success", "data": {"id": "l1"}, "url": "https://pay.example/abc"}),
        ], calls), sleep=lambda s: None)
        self.assertEqual(client.payouts.create(15000, "J", "r", phone_number="0700000000")["approval_id"], "a1")
        self.assertEqual(client.transfers.start_external("C", 1000, "r")["transfer_id"], "t1")
        link = client.payment_links.create("KES", amount_cents=1000)
        self.assertEqual((link["url"], link["data"]["id"]), ("https://pay.example/abc", "l1"))

    def test_payout_retry_reuses_the_idempotency_key(self):
        calls = []
        client = Client("k", max_retries=2, opener=scripted([(503, {}), (502, {}), (202, {"status": "success", "payout_id": "p1"})], calls), sleep=lambda s: None)
        self.assertEqual(client.payouts.create(100, "J", "r", phone_number="0700000000")["payout_id"], "p1")
        keys = [c.get_header("X-idempotency-key") for c in calls]
        self.assertEqual(len(keys), 3)
        self.assertTrue(keys[0] and all(k == keys[0] for k in keys))

    def test_amounts_must_be_integer_cents(self):
        client = Client("k", opener=scripted([], []), sleep=lambda s: None)
        with self.assertRaises(TypeError):
            client.collections.stk_push(10.5, "0700000000")
        with self.assertRaises(TypeError):
            client.payouts.create(True, "J", "r")
        with self.assertRaises(TypeError):
            client.bulk_payouts.create([{"amount_cents": 1.25, "recipient_name": "A"}, {"amount_cents": 100, "recipient_name": "B"}])


class ClientCredentialsTests(unittest.TestCase):
    def test_token_is_unauthenticated_and_cached_until_shortly_before_expiry(self):
        calls = []
        now = [1_000_000.0]
        client = Client(client_id="cid", client_secret="csecret", clock=lambda: now[0], opener=token_server(calls), sleep=lambda s: None)
        client.whoami()
        client.balance.get()
        token_calls = [c for c in calls if c.full_url.endswith("/oauth/token")]
        self.assertEqual(len(token_calls), 1)
        self.assertEqual(token_calls[0].get_method(), "POST")
        self.assertIsNone(token_calls[0].get_header("Authorization"))
        self.assertIsNone(token_calls[0].get_header("X-idempotency-key"))
        self.assertEqual(json.loads(token_calls[0].data), {"grant_type": "client_credentials", "client_id": "cid", "client_secret": "csecret"})
        self.assertTrue(all(c.get_header("Authorization") == "Bearer at-1" for c in calls if c not in token_calls))

        now[0] += 3600 - 61
        client.whoami()
        self.assertEqual(sum(c.full_url.endswith("/oauth/token") for c in calls), 1)
        now[0] += 2
        client.whoami()
        self.assertEqual(sum(c.full_url.endswith("/oauth/token") for c in calls), 2)
        self.assertEqual(calls[-1].get_header("Authorization"), "Bearer at-2")

    def test_401_refreshes_once_with_the_same_key(self):
        calls = []
        client = Client(client_id="cid", client_secret="s", opener=token_server(calls, [401, 200]), sleep=lambda s: None)
        client.collections.stk_push(1000, "0700000000")
        api = [c for c in calls if not c.full_url.endswith("/oauth/token")]
        self.assertEqual([c.get_header("Authorization") for c in api], ["Bearer at-1", "Bearer at-2"])
        self.assertEqual(api[0].get_header("X-idempotency-key"), api[1].get_header("X-idempotency-key"))

        stuck = Client(client_id="cid", client_secret="s", opener=token_server([], [401, 401]), sleep=lambda s: None)
        with self.assertRaises(RigePayError) as ctx:
            stuck.whoami()
        self.assertEqual(ctx.exception.status, 401)

    def test_oauth_error_code_comes_from_the_error_field(self):
        client = Client("k", opener=scripted([(401, {"status": "error", "error": "invalid_client", "message": "bad"})], []), sleep=lambda s: None)
        with self.assertRaises(RigePayError) as ctx:
            client.oauth.create_token("a", "b")
        self.assertEqual(ctx.exception.code, "invalid_client")

    def test_a_credential_is_required(self):
        with self.assertRaises(ValueError):
            Client()
        with self.assertRaises(ValueError):
            Client(client_id="only-id")


if __name__ == "__main__":
    unittest.main()
