# RigePay SDKs

Small, dependency-free clients for the RigePay developer API (payments and escrow) in Node.js, Go, Python and PHP. Each one:

- adds an `X-Idempotency-Key` to every `POST` (or uses the one you pass) and **reuses it on retries**;
- retries 429 and 502/503/504 and network errors with backoff, honouring `Retry-After`, and never retries other 4xx;
- raises a typed error carrying the HTTP status and RigePay `code` (for `/oauth/token` errors, the OAuth `error`);
- authenticates with a static key (`OAK_...`) or with OAuth client credentials: the access token is fetched from `POST /oauth/token`, cached until 60 seconds before `expires_in` runs out, and refetched once if a request answers 401 (the retry keeps its idempotency key);
- takes and returns amounts as **integer cents**. Node and Python throw a `TypeError` on a float, and Go uses `int64`. PHP amount parameters are typed `int`, and bulk-payout rows are checked with `is_int`. In PHP, a calling file without `declare(strict_types=1)` that passes `10.5` gets a deprecation notice and the value is truncated to `10`, so declare strict types;
- verifies webhook signatures with the exact scheme the server signs with (`webhooks`/`Webhook::verify`). Pass the endpoint URL **exactly as registered**, the raw request bytes, and the whole secret including `whsec_`.

`vectors.json` holds the signature test vector. It comes from the server's own signer (`pkg/utils/outbound_signing_vector_test.go` in the API repository pins the same value), and every SDK's tests use it.

## Resources

The paths and fields are those of `openapi.yaml` in rigepay-docs. Payouts and transfers name the amount `amount` on the wire and everything else names it `amount_cents`; the SDKs call it `amountCents` / `AmountCents` / `amount_cents` everywhere and map it.

| Endpoint | Node | Go | Python | PHP |
|---|---|---|---|---|
| `POST /oauth/token` | `oauth.createToken` | `OAuth.CreateToken` | `oauth.create_token` | `oauth->createToken` |
| `GET /payments/whoami` | `whoami` | `WhoAmI` | `whoami` | `whoami` |
| `POST /payments/collections/stk-push` | `collections.stkPush` | `Collections.STKPush` | `collections.stk_push` | `collections->stkPush` |
| `POST /payments/checkout/sessions` | `checkout.createSession` | `Checkout.CreateSession` | `checkout.create_session` | `checkout->createSession` |
| `GET /payments/checkout/sessions/{id}` | `checkout.getSession` | `Checkout.GetSession` | `checkout.get_session` | `checkout->getSession` |
| `POST /payments/payouts` | `payouts.create` * | `Payouts.Create` | `payouts.create` * | `payouts->create` * |
| `GET /payments/payouts/fee-estimate` | `payouts.feeEstimate` | `Payouts.FeeEstimate` | `payouts.fee_estimate` | `payouts->feeEstimate` |
| `GET /payments/payouts/approval-ceiling` | `payouts.approvalCeiling` | `Payouts.ApprovalCeiling` | `payouts.approval_ceiling` | `payouts->approvalCeiling` |
| `POST /payments/bulk-payouts` | `bulkPayouts.create` | `BulkPayouts.Create` | `bulk_payouts.create` | `bulkPayouts->create` |
| `GET /payments/bulk-payouts` | `bulkPayouts.list` | `BulkPayouts.List` | `bulk_payouts.list` | `bulkPayouts->list` |
| `GET /payments/bulk-payouts/{batchId}` | `bulkPayouts.get` | `BulkPayouts.Get` | `bulk_payouts.get` | `bulkPayouts->get` |
| `POST /payments/transfers` | `transfers.create` | `Transfers.Create` | `transfers.create` | `transfers->create` |
| `GET /payments/transfers/lookup` | `transfers.lookup` | `Transfers.Lookup` | `transfers.lookup` | `transfers->lookup` |
| `POST /payments/transfers/external` | `transfers.startExternal` * | `Transfers.StartExternal` | `transfers.start_external` * | `transfers->startExternal` * |
| `POST /payments/transfers/external/confirm` | `transfers.confirmExternal` | `Transfers.ConfirmExternal` | `transfers.confirm_external` | `transfers->confirmExternal` |
| `POST /payments/transfers/external/cancel` | `transfers.cancelExternal` | `Transfers.CancelExternal` | `transfers.cancel_external` | `transfers->cancelExternal` |
| `GET /payments/balance` | `balance.get` | `Balance.Get` | `balance.get` | `balance->get` |
| `GET /payments/transactions` | `transactions.list` | `Transactions.List` | `transactions.list` | `transactions->list` |
| `GET /payments/transactions/{id}` | `transactions.get` | `Transactions.Get` | `transactions.get` | `transactions->get` |
| `POST /payments/payment-links` | `paymentLinks.create` * | `PaymentLinks.Create` † | `payment_links.create` * | `paymentLinks->create` * |
| `GET /payments/payment-links` | `paymentLinks.list` * | `PaymentLinks.List` † | `payment_links.list` * | `paymentLinks->list` * |
| `GET /payments/payment-links/{id}` | `paymentLinks.get` * | `PaymentLinks.Get` † | `payment_links.get` * | `paymentLinks->get` * |
| `POST /payments/payment-links/{id}/close` | `paymentLinks.close` * | `PaymentLinks.Close` † | `payment_links.close` * | `paymentLinks->close` * |
| `POST /payments/invoices` | `invoices.create` * | `Invoices.Create` † | `invoices.create` * | `invoices->create` * |
| `GET /payments/invoices` | `invoices.list` * | `Invoices.List` † | `invoices.list` * | `invoices->list` * |
| `GET /payments/invoices/{id}` | `invoices.get` | `Invoices.Get` † | `invoices.get` | `invoices->get` |
| `POST /payments/invoices/{id}/cancel` | `invoices.cancel` | `Invoices.Cancel` † | `invoices.cancel` | `invoices->cancel` |
| `/escrows/...` | `escrows.*` | `Escrows.*` | `escrows.*` | `escrows->*` |

Most methods return the response's `data`. Methods marked * return the **whole body**, because the useful fields sit outside `data`. For example, a payout's `status` (`success` or `approval_required`) and `payout_id`, an external transfer's `otp_required` and `transfer_id`, and the shareable `url` next to a new link or invoice. Go decodes documented responses into typed structs with `int64` cents. † The spec leaves the payment-link and invoice bodies open, so Go returns them as `json.RawMessage`. Decode those yourself, with cents as integers.

## Examples

Node:

```js
import { RigePay } from '@rigepay/sdk'

const rp = new RigePay({ clientId: process.env.RIGEPAY_CLIENT_ID, clientSecret: process.env.RIGEPAY_CLIENT_SECRET })
const push = await rp.collections.stkPush({ amountCents: 150000, customerPhone: '+254700000000', remarks: 'Order 1042', idempotencyKey: 'order-1042' })
// push.reference: wait for the collection.completed webhook before treating it as paid.

const payout = await rp.payouts.create({ amountCents: 250000, recipientName: 'Jane W', remarks: 'May commission', phoneNumber: '0700000000', idempotencyKey: 'commission-2026-05-jane' })
if (payout.status === 'approval_required') console.log('awaiting approval', payout.approval_id)

const started = await rp.transfers.startExternal({ recipientCollectionCode: 'ACME01', amountCents: 500000, remarks: 'Stock' })
await rp.transfers.confirmExternal({ transferId: started.transfer_id, otp: '123456' })

const { balances } = await rp.balance.get()
const page = await rp.transactions.list({ page: 1, page_size: 50, direction: 'CREDIT' })
```

Go:

```go
c := rigepay.NewWithClientCredentials(os.Getenv("RIGEPAY_CLIENT_ID"), os.Getenv("RIGEPAY_CLIENT_SECRET"))
push, err := c.Collections.STKPush(ctx, rigepay.STKPushInput{AmountCents: 150000, CustomerPhone: "+254700000000"}, "order-1042")
fee, err := c.Payouts.FeeEstimate(ctx, 250000, "PHONE_NUMBER")
p, err := c.Payouts.Create(ctx, rigepay.PayoutInput{AmountCents: 250000, RecipientName: "Jane W", Remarks: "May", PhoneNumber: "0700000000"}, "")
page, err := c.Transactions.List(ctx, url.Values{"page": {"1"}, "direction": {"CREDIT"}})
```

Python:

```python
rp = Client(client_id=os.environ["RIGEPAY_CLIENT_ID"], client_secret=os.environ["RIGEPAY_CLIENT_SECRET"])
rp.collections.stk_push(150000, "+254700000000", remarks="Order 1042", idempotency_key="order-1042")
payout = rp.payouts.create(250000, "Jane W", "May commission", phone_number="0700000000")
link = rp.payment_links.create("KES", amount_cents=100000, name="Deposit")   # link["url"]
rp.bulk_payouts.create([{"amount_cents": 1000, "recipient_name": "A", "phone_number": "0700000001"},
                        {"amount_cents": 2000, "recipient_name": "B", "phone_number": "0700000002"}])
```

PHP:

```php
$rp = new RigePay\Client(clientId: getenv('RIGEPAY_CLIENT_ID'), clientSecret: getenv('RIGEPAY_CLIENT_SECRET'));
$rp->collections->stkPush(150000, '+254700000000', remarks: 'Order 1042', idempotencyKey: 'order-1042');
$payout = $rp->payouts->create(250000, 'Jane W', 'May commission', phoneNumber: '0700000000');
$page = $rp->transactions->list(['page' => 1, 'direction' => 'CREDIT']);
```

A static key still works everywhere: `new RigePay({ token })`, `rigepay.New(token)`, `Client(token)`, `new Client($token)`.

## Tests

The tests use mocked transports and make no network calls.

| SDK | Run the tests | Status |
|---|---|---|
| `node/` | `node --test` | tested (41 tests, node 24) |
| `go/` | `GOWORK=off go test ./...` | tested (12 tests, 26 subtests, go 1.26) |
| `python/` | `python -m unittest discover -s tests` | tested (14 tests, python 3.12 and 3.10) |
| `php/` | `php tests/run.php` | escrow checks tested (9 checks, php:8.3); the payments checks have not been run yet |

Nothing here is published. Publishing (npm, pkg.go.dev, PyPI, Packagist), a licence and a git repository for this folder are decisions for RigePay.
