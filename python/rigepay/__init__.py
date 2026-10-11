"""RigePay API client for Python 3.9+ with no dependencies.

Idempotency keys are added to every POST and reused when a request is retried; webhook
signatures are verified with the same scheme the server signs with.
"""
from __future__ import annotations

import hashlib
import hmac
import json
import random
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
    def __init__(self, token: str, base_url: str = DEFAULT_BASE_URL, max_retries: int = 3, timeout: float = 30.0,
                 opener: Optional[Callable[..., Any]] = None, sleep: Callable[[float], None] = time.sleep):
        if not token:
            raise ValueError("token is required")
        self.token = token
        self.base_url = base_url.rstrip("/")
        self.max_retries = max_retries
        self.timeout = timeout
        self._open = opener or urllib.request.urlopen
        self._sleep = sleep
        self.escrows = Escrows(self)

    def request(self, method: str, path: str, body: Optional[Mapping[str, Any]] = None,
                query: Optional[Mapping[str, Any]] = None, idempotency_key: Optional[str] = None) -> dict:
        url = self.base_url + path
        if query:
            url += "?" + urllib.parse.urlencode({k: v for k, v in query.items() if v is not None})
        payload = None if body is None else json.dumps(body).encode()
        key = idempotency_key or (str(uuid.uuid4()) if method != "GET" else None)
        for attempt in range(self.max_retries + 1):
            headers = {"Authorization": f"Bearer {self.token}", "Accept": "application/json"}
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
            if status in RETRY_STATUSES and attempt < self.max_retries:
                self._sleep(_backoff(attempt, retry_after))
                continue
            try:
                parsed = json.loads(raw) if raw else {}
            except ValueError:
                parsed = {"message": raw.decode(errors="replace")}
            if status >= 400:
                raise RigePayError(status, parsed.get("code"), parsed.get("message", ""), parsed)
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
