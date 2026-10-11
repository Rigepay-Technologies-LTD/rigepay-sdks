import io
import json
import os
import sys
import unittest
import urllib.error

sys.path.insert(0, os.path.join(os.path.dirname(__file__), ".."))

from rigepay import Client, RigePayError, sign, verify_webhook  # noqa: E402

with open(os.path.join(os.path.dirname(__file__), "..", "..", "vectors.json")) as f:
    V = json.load(f)["signature"]


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


def scripted(responses, calls):
    def opener(req, timeout=None):
        calls.append(req)
        status, body = responses.pop(0)
        if status >= 400:
            raise urllib.error.HTTPError(req.full_url, status, "error", {}, io.BytesIO(json.dumps(body).encode()))
        return FakeResponse(status, body)

    return opener


class SignatureTests(unittest.TestCase):
    def test_matches_the_server_signer(self):
        self.assertEqual(sign(V["secret"], V["url"], V["timestamp"], V["request_id"], V["body"].encode()), V["signature"])

    def test_verify(self):
        headers = {"X-RigePay-Timestamp": V["timestamp"], "X-RigePay-Request-ID": V["request_id"], "X-RigePay-Signature": V["signature"]}
        now = lambda: int(V["timestamp"]) + 10  # noqa: E731
        body = V["body"].encode()
        self.assertTrue(verify_webhook(body, headers, V["secret"], V["url"], now=now))
        self.assertFalse(verify_webhook(body + b" ", headers, V["secret"], V["url"], now=now))
        self.assertFalse(verify_webhook(body, headers, V["secret"], V["url"].replace("?src=rigepay", ""), now=now))
        self.assertFalse(verify_webhook(body, headers, V["secret"], V["url"], now=lambda: int(V["timestamp"]) + 301))


class ClientTests(unittest.TestCase):
    def test_retry_reuses_the_idempotency_key(self):
        calls = []
        client = Client("t", opener=scripted([(503, {}), (200, {"data": {"id": "e1"}})], calls), sleep=lambda s: None)
        self.assertEqual(client.escrows.create({"reference": "r"})["id"], "e1")
        keys = [c.get_header("X-idempotency-key") for c in calls]
        self.assertEqual(len(keys), 2)
        self.assertTrue(keys[0] and keys[0] == keys[1])

    def test_errors_carry_the_code_and_are_not_retried(self):
        calls = []
        client = Client("t", opener=scripted([(409, {"code": "dispute_active", "message": "frozen"})], calls), sleep=lambda s: None)
        with self.assertRaises(RigePayError) as ctx:
            client.escrows.release("e1", milestone_id="m1")
        self.assertEqual(ctx.exception.code, "dispute_active")
        self.assertEqual(len(calls), 1)
        self.assertEqual(json.loads(calls[0].data), {"milestone_id": "m1"})


if __name__ == "__main__":
    unittest.main()
