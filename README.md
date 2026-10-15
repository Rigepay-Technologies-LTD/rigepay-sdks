# RigePay SDKs

Small, dependency-free clients for the RigePay escrow API in Node.js, Go, Python and PHP. Each one:

- adds an `X-Idempotency-Key` to every `POST` (or uses the one you pass) and **reuses it on retries**;
- retries 429 and 502/503/504 and network errors with backoff, honouring `Retry-After`, and never retries other 4xx;
- raises a typed error carrying the HTTP status and RigePay `code`;
- verifies webhook signatures with the exact scheme the server signs with (`webhooks`/`Webhook::verify`). Pass the endpoint URL **exactly as registered**, the raw request bytes, and the whole secret including `whsec_`.

`vectors.json` holds the signature test vector. It comes from the server's own signer (`pkg/utils/outbound_signing_vector_test.go` in the API repository pins the same value), and every SDK's tests use it.

| SDK | Run the tests | Status |
|---|---|---|
| `node/` | `node --test` | tested |
| `go/` | `GOWORK=off go test ./...` | tested |
| `python/` | `python -m unittest discover -s tests` | tested (4 tests, python:3.12) |
| `php/` | `php tests/run.php` | tested (9 checks, php:8.3) |

Nothing here is published. Publishing (npm, pkg.go.dev, PyPI, Packagist), a licence and a git repository for this folder are decisions for RigePay.
