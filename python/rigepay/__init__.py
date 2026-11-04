"""RigePay API client for Python 3.9+ with no dependencies.

Idempotency keys are added to every POST and reused when a request is retried; webhook
signatures are verified with the same scheme the server signs with.
"""
from __future__ import annotations

import hashlib
import hmac
import json
import random
import threading
import time
import urllib.error
import urllib.parse
import urllib.request
import uuid
from typing import Any, Callable, Mapping, Optional

DEFAULT_BASE_URL = "https://api.rigepay.co.ke/api/v1"
RETRY_STATUSES = {429, 502, 503, 504}


class RigePayError(Exception):
    def __init__(self, status: int, code: Optional[str], message: str, body: Any = None):
        super().__init__(f"{status} {code or ''}: {message}".strip())
        self.status = status
        self.code = code
        self.message = message
        self.body = body


def _backoff(attempt: int, retry_after: float) -> float:
    if retry_after > 0:
        return min(retry_after, 30.0)
    base = min(0.5 * (2 ** attempt), 8.0)
    return base / 2 + random.random() * (base / 2)


class Client:
    """Pass a static `token`, or `client_id` and `client_secret` to use an OAuth access token that is
    fetched from /oauth/token and cached until `token_refresh_skew` seconds before it expires."""

    def __init__(self, token: Optional[str] = None, base_url: str = DEFAULT_BASE_URL, max_retries: int = 3, timeout: float = 30.0,
                 opener: Optional[Callable[..., Any]] = None, sleep: Callable[[float], None] = time.sleep,
                 client_id: Optional[str] = None, client_secret: Optional[str] = None,
                 token_refresh_skew: float = 60.0, clock: Callable[[], float] = time.time):
        if not token and not (client_id and client_secret):
            raise ValueError("token, or client_id and client_secret, is required")
        self.token = token
        self.client_id = client_id
        self.client_secret = client_secret
        self.token_refresh_skew = token_refresh_skew
        self._clock = clock
        self._access_token: Optional[str] = None
        self._refresh_at = 0.0
        self._token_lock = threading.Lock()
        self.base_url = base_url.rstrip("/")
        self.max_retries = max_retries
        self.timeout = timeout
        self._open = opener or urllib.request.urlopen
        self._sleep = sleep
        self.escrows = Escrows(self)
        self.oauth = OAuth(self)
        self.collections = Collections(self)
        self.checkout = Checkout(self)
        self.payouts = Payouts(self)
        self.bulk_payouts = BulkPayouts(self)
        self.transfers = Transfers(self)
        self.balance = Balance(self)
        self.transactions = Transactions(self)
        self.payment_links = PaymentLinks(self)
        self.invoices = Invoices(self)

    def get_token(self) -> str:
        """The static token, or a cached client-credentials access token."""
        if self.token:
            return self.token
        with self._token_lock:
            if self._access_token and self._clock() < self._refresh_at:
                return self._access_token
            t = self.oauth.create_token(self.client_id or "", self.client_secret or "")
            self._access_token = t["access_token"]
            self._refresh_at = self._clock() + float(t["expires_in"]) - self.token_refresh_skew
            return self._access_token

    def whoami(self) -> Any:
        return self.data("GET", "/payments/whoami")

    def request(self, method: str, path: str, body: Optional[Mapping[str, Any]] = None,
                query: Optional[Mapping[str, Any]] = None, idempotency_key: Optional[str] = None, auth: bool = True) -> dict:
        url = self.base_url + path
        if query:
            url += "?" + urllib.parse.urlencode({k: v for k, v in query.items() if v is not None})
        payload = None if body is None else json.dumps(body).encode()
        key = idempotency_key or (str(uuid.uuid4()) if method != "GET" and auth else None)
        refreshed = False
        for attempt in range(self.max_retries + 2):
            headers = {"Accept": "application/json"}
            if auth:
                headers["Authorization"] = f"Bearer {self.get_token()}"
            if payload is not None:
                headers["Content-Type"] = "application/json"
            if key:
                headers["X-Idempotency-Key"] = key
            req = urllib.request.Request(url, data=payload, headers=headers, method=method)
            try:
                with self._open(req, timeout=self.timeout) as resp:
                    raw, status = resp.read(), resp.status
                    retry_after = 0.0
            except urllib.error.HTTPError as err:
                raw, status = err.read(), err.code
                retry_after = float(err.headers.get("Retry-After") or 0) if err.headers else 0.0
            except urllib.error.URLError:
                if attempt >= self.max_retries:
                    raise
                self._sleep(_backoff(attempt, 0))
                continue
            if status == 401 and auth and not self.token and not refreshed:
                # The cached access token was revoked or expired early: fetch a new one once, same key.
                refreshed = True
                self._access_token = None
                continue
            if status in RETRY_STATUSES and attempt < self.max_retries:
                self._sleep(_backoff(attempt, retry_after))
                continue
            try:
                parsed = json.loads(raw) if raw else {}
            except ValueError:
                parsed = {"message": raw.decode(errors="replace")}
            if status >= 400:
                raise RigePayError(status, parsed.get("code") or parsed.get("error"), parsed.get("message", ""), parsed)
            return parsed
        raise RuntimeError("unreachable")

    def data(self, *args: Any, **kwargs: Any) -> Any:
        return self.request(*args, **kwargs).get("data")


def _compact(d: Mapping[str, Any]) -> dict:
    return {k: v for k, v in d.items() if v is not None}


class Escrows:
    def __init__(self, client: Client):
        self.c = client

    def create(self, input: Mapping[str, Any], idempotency_key: Optional[str] = None):
        return self.c.data("POST", "/escrows", body=input, idempotency_key=idempotency_key)

    def get(self, escrow_id: str):
        return self.c.data("GET", f"/escrows/{escrow_id}")

    def fund(self, escrow_id: str, idempotency_key: Optional[str] = None):
        return self.c.data("POST", f"/escrows/{escrow_id}/fund", idempotency_key=idempotency_key)

    def release(self, escrow_id: str, amount_cents: Optional[int] = None, milestone_id: Optional[str] = None,
                confirmation_id: Optional[str] = None, remarks: Optional[str] = None, idempotency_key: Optional[str] = None):
        body = _compact({"amount_cents": amount_cents, "milestone_id": milestone_id, "confirmation_id": confirmation_id, "remarks": remarks})
        return self.c.data("POST", f"/escrows/{escrow_id}/release", body=body, idempotency_key=idempotency_key)

    def refund(self, escrow_id: str, amount_cents: Optional[int] = None, milestone_id: Optional[str] = None,
               confirmation_id: Optional[str] = None, remarks: Optional[str] = None, idempotency_key: Optional[str] = None):
        body = _compact({"amount_cents": amount_cents, "milestone_id": milestone_id, "confirmation_id": confirmation_id, "remarks": remarks})
        return self.c.data("POST", f"/escrows/{escrow_id}/refund", body=body, idempotency_key=idempotency_key)

    def cancel(self, escrow_id: str, idempotency_key: Optional[str] = None):
        return self.c.data("POST", f"/escrows/{escrow_id}/cancel", idempotency_key=idempotency_key)

    def open_dispute(self, escrow_id: str, reason: str, idempotency_key: Optional[str] = None):
        return self.c.data("POST", f"/escrows/{escrow_id}/disputes", body={"reason": reason}, idempotency_key=idempotency_key)

    def concede(self, escrow_id: str, dispute_id: str, idempotency_key: Optional[str] = None):
        return self.c.data("POST", f"/escrows/{escrow_id}/disputes/{dispute_id}/concede", idempotency_key=idempotency_key)


def _cents(name: str, value: Any) -> Any:
    """Amounts are integer cents; refuse floats (and bools, which are ints in Python)."""
    if value is not None and (isinstance(value, bool) or not isinstance(value, int)):
        raise TypeError(f"{name} must be an integer number of cents")
    return value


def _destination(destination_type: Optional[str], phone_number: Optional[str], bank_code: Optional[str],
                 bank_account_number: Optional[str], shortcode: Optional[str], account_reference: Optional[str]) -> dict:
    return {"destination_type": destination_type, "phone_number": phone_number, "bank_code": bank_code,
            "bank_account_number": bank_account_number, "shortcode": shortcode, "account_reference": account_reference}


class OAuth:
    def __init__(self, client: Client):
        self.c = client

    def create_token(self, client_id: str, client_secret: str) -> dict:
        """POST /oauth/token. Returns the flat token response; Client(client_id=..., client_secret=...) calls this for you."""
        body = {"grant_type": "client_credentials", "client_id": client_id, "client_secret": client_secret}
        return self.c.request("POST", "/oauth/token", body=body, auth=False)


class Collections:
    def __init__(self, client: Client):
        self.c = client

    def stk_push(self, amount_cents: int, customer_phone: str, remarks: Optional[str] = None, branch_id: Optional[str] = None,
                 metadata: Optional[Mapping[str, Any]] = None, channel: Optional[str] = None, idempotency_key: Optional[str] = None):
        body = _compact({"amount_cents": _cents("amount_cents", amount_cents), "customer_phone": customer_phone, "remarks": remarks,
                         "branch_id": branch_id, "metadata": metadata, "channel": channel})
        return self.c.data("POST", "/payments/collections/stk-push", body=body, idempotency_key=idempotency_key)


class Checkout:
    def __init__(self, client: Client):
        self.c = client

    def create_session(self, amount_cents: int, allowed_rails: list, branch_id: Optional[str] = None, description: Optional[str] = None,
                       metadata: Optional[Mapping[str, Any]] = None, idempotency_key: Optional[str] = None):
        body = _compact({"amount_cents": _cents("amount_cents", amount_cents), "allowed_rails": allowed_rails, "branch_id": branch_id,
                         "description": description, "metadata": metadata})
        return self.c.data("POST", "/payments/checkout/sessions", body=body, idempotency_key=idempotency_key)

    def get_session(self, session_id: str):
        return self.c.data("GET", f"/payments/checkout/sessions/{session_id}")


class Payouts:
    def __init__(self, client: Client):
        self.c = client

    def create(self, amount_cents: int, recipient_name: str, remarks: str, destination_type: Optional[str] = None,
               phone_number: Optional[str] = None, bank_code: Optional[str] = None, bank_account_number: Optional[str] = None,
               shortcode: Optional[str] = None, account_reference: Optional[str] = None, branch_id: Optional[str] = None,
               metadata: Optional[Mapping[str, Any]] = None, idempotency_key: Optional[str] = None) -> dict:
        """Returns the whole 202 body: read `status` (success or approval_required), `payout_id`, `approval_id`."""
        body = _compact({"amount": _cents("amount_cents", amount_cents), "recipient_name": recipient_name, "remarks": remarks,
                         **_destination(destination_type, phone_number, bank_code, bank_account_number, shortcode, account_reference),
                         "branch_id": branch_id, "metadata": metadata})
        return self.c.request("POST", "/payments/payouts", body=body, idempotency_key=idempotency_key)

    def fee_estimate(self, amount_cents: int, destination_type: Optional[str] = None):
        query = {"amount_cents": _cents("amount_cents", amount_cents), "destination_type": destination_type}
        return self.c.data("GET", "/payments/payouts/fee-estimate", query=query)

    def approval_ceiling(self, amount_cents: Optional[int] = None):
        return self.c.data("GET", "/payments/payouts/approval-ceiling", query={"amount_cents": _cents("amount_cents", amount_cents)})


class BulkPayouts:
    def __init__(self, client: Client):
        self.c = client

    def create(self, items: list, funding_source: Optional[str] = None, vault_id: Optional[str] = None,
               remarks: Optional[str] = None, idempotency_key: Optional[str] = None):
        """`items` are dicts with the API's own names (amount_cents, recipient_name, destination_type, phone_number, ...)."""
        for item in items:
            _cents("amount_cents", item.get("amount_cents"))
        body = _compact({"funding_source": funding_source, "vault_id": vault_id, "remarks": remarks, "items": [_compact(i) for i in items]})
        return self.c.data("POST", "/payments/bulk-payouts", body=body, idempotency_key=idempotency_key)

    def list(self):
        return self.c.data("GET", "/payments/bulk-payouts")

    def get(self, batch_id: str):
        return self.c.data("GET", f"/payments/bulk-payouts/{batch_id}")


class Transfers:
    def __init__(self, client: Client):
        self.c = client

    def create(self, from_: str, to: str, amount_cents: int, remarks: Optional[str] = None, idempotency_key: Optional[str] = None):
        """Move money between the organization ("org") and its branches (UUIDs)."""
        body = _compact({"from": from_, "to": to, "amount": _cents("amount_cents", amount_cents), "remarks": remarks})
        return self.c.data("POST", "/payments/transfers", body=body, idempotency_key=idempotency_key)

    def lookup(self, code: str):
        return self.c.data("GET", "/payments/transfers/lookup", query={"code": code})

    def start_external(self, recipient_collection_code: str, amount_cents: int, remarks: str, from_: Optional[str] = None,
                       idempotency_key: Optional[str] = None) -> dict:
        """Returns the whole 202 body: `status` is otp_required, with `transfer_id` and `expires_in`."""
        body = _compact({"recipient_collection_code": recipient_collection_code, "amount": _cents("amount_cents", amount_cents),
                         "from": from_, "remarks": remarks})
        return self.c.request("POST", "/payments/transfers/external", body=body, idempotency_key=idempotency_key)

    def confirm_external(self, transfer_id: str, otp: str, idempotency_key: Optional[str] = None):
        return self.c.data("POST", "/payments/transfers/external/confirm", body={"transfer_id": transfer_id, "otp": otp},
                           idempotency_key=idempotency_key)

    def cancel_external(self, transfer_id: str, idempotency_key: Optional[str] = None):
        return self.c.data("POST", "/payments/transfers/external/cancel", body={"transfer_id": transfer_id}, idempotency_key=idempotency_key)


class Balance:
    def __init__(self, client: Client):
        self.c = client

    def get(self, branch_id: Optional[str] = None):
        return self.c.data("GET", "/payments/balance", query={"branch_id": branch_id})


class Transactions:
    def __init__(self, client: Client):
        self.c = client

    def list(self, **query: Any):
        """Filters use the API's names: page, page_size, search, status, direction, type, rail, start_date, end_date,
        min_amount, max_amount (cents), branch_id."""
        for name in ("min_amount", "max_amount"):
            _cents(name, query.get(name))
        return self.c.data("GET", "/payments/transactions", query=query)

    def get(self, transaction_id: str):
        return self.c.data("GET", f"/payments/transactions/{transaction_id}")


class PaymentLinks:
    def __init__(self, client: Client):
        self.c = client

    def create(self, currency: str, amount_cents: Optional[int] = None, payment_methods: Optional[str] = None,
               description: Optional[str] = None, name: Optional[str] = None, is_reusable: Optional[bool] = None,
               allow_open_amount: Optional[bool] = None, expires_in_days: Optional[int] = None, max_uses: Optional[int] = None,
               redirect_url: Optional[str] = None, cancel_url: Optional[str] = None, link_type: Optional[str] = None,
               idempotency_key: Optional[str] = None) -> dict:
        """Returns the whole body: the link in `data`, its shareable address in `url`."""
        body = _compact({"amount_cents": _cents("amount_cents", amount_cents), "currency": currency, "payment_methods": payment_methods,
                         "description": description, "name": name, "is_reusable": is_reusable, "allow_open_amount": allow_open_amount,
                         "expires_in_days": expires_in_days, "max_uses": max_uses, "redirect_url": redirect_url,
                         "cancel_url": cancel_url, "link_type": link_type})
        return self.c.request("POST", "/payments/payment-links", body=body, idempotency_key=idempotency_key)

    def list(self, **query: Any) -> dict:
        return self.c.request("GET", "/payments/payment-links", query=query)

    def get(self, link_id: str) -> dict:
        return self.c.request("GET", f"/payments/payment-links/{link_id}")

    def close(self, link_id: str, idempotency_key: Optional[str] = None) -> dict:
        return self.c.request("POST", f"/payments/payment-links/{link_id}/close", idempotency_key=idempotency_key)


class Invoices:
    def __init__(self, client: Client):
        self.c = client

    def create(self, customer_name: str, customer_phone: str, currency: str, due_date: str, items: list,
               customer_email: Optional[str] = None, notes: Optional[str] = None, prices_include_vat: Optional[bool] = None,
               idempotency_key: Optional[str] = None) -> dict:
        """Returns the whole body: the invoice in `data`, its shareable address in `url`. Items are sent as given."""
        body = _compact({"customer_name": customer_name, "customer_phone": customer_phone, "customer_email": customer_email,
                         "currency": currency, "due_date": due_date, "notes": notes, "prices_include_vat": prices_include_vat,
                         "items": items})
        return self.c.request("POST", "/payments/invoices", body=body, idempotency_key=idempotency_key)

    def list(self, **query: Any) -> dict:
        return self.c.request("GET", "/payments/invoices", query=query)

    def get(self, invoice_id: str):
        return self.c.data("GET", f"/payments/invoices/{invoice_id}")

    def cancel(self, invoice_id: str, idempotency_key: Optional[str] = None):
        return self.c.data("POST", f"/payments/invoices/{invoice_id}/cancel", idempotency_key=idempotency_key)


def sign(secret: str, url: str, timestamp: str, request_id: str, body: bytes, method: str = "POST") -> str:
    body_hash = hashlib.sha256(body).hexdigest()
    message = f"{method}:{url}:{timestamp}:{request_id}:{body_hash}"
    return hmac.new(secret.encode(), message.encode(), hashlib.sha256).hexdigest()


def verify_webhook(raw_body: bytes, headers: Mapping[str, str], secret: str, url: str,
                   tolerance_seconds: int = 300, now: Optional[Callable[[], float]] = None) -> bool:
    """`url` must be the endpoint URL exactly as registered with RigePay; `raw_body` the exact bytes received."""
    lower = {k.lower(): v for k, v in headers.items()}
    timestamp = lower.get("x-rigepay-timestamp")
    request_id = lower.get("x-rigepay-request-id")
    signature = lower.get("x-rigepay-signature", "")
    if not timestamp or not request_id or not signature:
        return False
    expected = sign(secret, url, timestamp, request_id, raw_body)
    if not hmac.compare_digest(expected, signature):
        return False
    try:
        age = abs((now() if now else time.time()) - int(timestamp))
    except ValueError:
        return False
    return age <= tolerance_seconds
