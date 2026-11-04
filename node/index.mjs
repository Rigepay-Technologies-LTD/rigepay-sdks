import { createHash, createHmac, randomUUID, timingSafeEqual } from 'node:crypto'

const DEFAULT_BASE_URL = 'https://api.rigepay.co.ke/api/v1'
const RETRY_STATUSES = new Set([429, 502, 503, 504])

export class RigePayError extends Error {
  constructor({ status, code, message, requestId, body }) {
    super(message || `RigePay request failed with status ${status}`)
    this.name = 'RigePayError'
    this.status = status
    this.code = code
    this.requestId = requestId
    this.body = body
  }
}

const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms))

function backoff(attempt, retryAfter) {
  if (retryAfter > 0) return Math.min(retryAfter * 1000, 30000)
  const base = Math.min(500 * 2 ** attempt, 8000)
  return base / 2 + Math.random() * (base / 2)
}

export class RigePay {
  constructor({ token, clientId, clientSecret, tokenRefreshSkewMs = 60000, now = Date.now, baseUrl = DEFAULT_BASE_URL, maxRetries = 3, timeoutMs = 30000, fetchImpl = globalThis.fetch } = {}) {
    if (!token && !(clientId && clientSecret)) throw new Error('token, or clientId and clientSecret, is required')
    this.token = token
    this.clientId = clientId
    this.clientSecret = clientSecret
    this.tokenRefreshSkewMs = tokenRefreshSkewMs
    this.now = now
    this.baseUrl = baseUrl.replace(/\/+$/, '')
    this.maxRetries = maxRetries
    this.timeoutMs = timeoutMs
    this.fetch = fetchImpl
    this.escrows = new Escrows(this)
    this.oauth = new OAuth(this)
    this.collections = new Collections(this)
    this.checkout = new Checkout(this)
    this.payouts = new Payouts(this)
    this.bulkPayouts = new BulkPayouts(this)
    this.transfers = new Transfers(this)
    this.balance = new Balance(this)
    this.transactions = new Transactions(this)
    this.paymentLinks = new PaymentLinks(this)
    this.invoices = new Invoices(this)
    this._cached = null
    this._pending = null
  }

  // Returns the bearer token: the static token, or a client-credentials access token cached until
  // tokenRefreshSkewMs before it expires. Concurrent callers share one token request.
  async getToken() {
    if (this.token) return this.token
    if (this._cached && this.now() < this._cached.refreshAt) return this._cached.accessToken
    if (!this._pending) {
      this._pending = this.oauth
        .createToken({ clientId: this.clientId, clientSecret: this.clientSecret })
        .then((t) => {
          this._cached = { accessToken: t.access_token, refreshAt: this.now() + Number(t.expires_in) * 1000 - this.tokenRefreshSkewMs }
          return t.access_token
        })
        .finally(() => {
          this._pending = null
        })
    }
    return this._pending
  }

  async request(method, path, { body, query, idempotencyKey, auth = true } = {}) {
    const url = new URL(this.baseUrl + path)
    for (const [k, v] of Object.entries(query || {})) if (v !== undefined && v !== null) url.searchParams.set(k, String(v))
    const headers = { Accept: 'application/json' }
    if (body !== undefined) headers['Content-Type'] = 'application/json'
    const key = method !== 'GET' && auth ? idempotencyKey || randomUUID() : undefined
    if (key) headers['X-Idempotency-Key'] = key
    const payload = body === undefined ? undefined : JSON.stringify(body)
    let refreshed = false

    for (let attempt = 0; ; attempt++) {
      const sent = auth ? { ...headers, Authorization: `Bearer ${await this.getToken()}` } : headers
      let res
      try {
        res = await this.fetch(url, { method, headers: sent, body: payload, signal: AbortSignal.timeout(this.timeoutMs) })
      } catch (err) {
        if (attempt >= this.maxRetries) throw err
        await sleep(backoff(attempt, 0))
        continue
      }
      if (res.status === 401 && auth && !this.token && !refreshed) {
        // The cached access token was revoked or expired early: fetch a new one once, same key.
        refreshed = true
        this._cached = null
        continue
      }
      if (RETRY_STATUSES.has(res.status) && attempt < this.maxRetries) {
        await sleep(backoff(attempt, Number(res.headers.get('retry-after')) || 0))
        continue
      }
      const text = await res.text()
      let json
      try {
        json = text ? JSON.parse(text) : {}
      } catch {
        json = { message: text }
      }
      if (!res.ok) {
        throw new RigePayError({ status: res.status, code: json.code ?? json.error, message: json.message, requestId: res.headers.get('x-request-id') || undefined, body: json })
      }
      return json
    }
  }

  whoami() { return this.data('GET', '/payments/whoami') }

  async data(method, path, options) {
    return (await this.request(method, path, options)).data
  }
}

class Escrows {
  constructor(client) {
    this.c = client
  }
  create(input, opts) { return this.c.data('POST', '/escrows', { body: input, ...opts }) }
  get(id) { return this.c.data('GET', `/escrows/${id}`) }
  list(query) { return this.c.request('GET', '/escrows', { query }) }
  events(id, query) { return this.c.data('GET', `/escrows/${id}/events`, { query }) }
  fund(id, opts) { return this.c.data('POST', `/escrows/${id}/fund`, opts) }
  fundingLink(id, opts) { return this.c.data('POST', `/escrows/${id}/funding-link`, opts) }
  release(id, { amountCents, milestoneId, confirmationId, remarks, idempotencyKey } = {}) {
    return this.c.data('POST', `/escrows/${id}/release`, { body: compact({ amount_cents: amountCents, milestone_id: milestoneId, confirmation_id: confirmationId, remarks }), idempotencyKey })
  }
  refund(id, { amountCents, milestoneId, confirmationId, remarks, idempotencyKey } = {}) {
    return this.c.data('POST', `/escrows/${id}/refund`, { body: compact({ amount_cents: amountCents, milestone_id: milestoneId, confirmation_id: confirmationId, remarks }), idempotencyKey })
  }
  cancel(id, opts) { return this.c.data('POST', `/escrows/${id}/cancel`, opts) }
  requestConfirmation(id, { purpose, amountCents, milestoneId }) {
    return this.c.data('POST', `/escrows/${id}/confirmations`, { body: compact({ purpose, amount_cents: amountCents, milestone_id: milestoneId }) })
  }
  verifyConfirmation(id, { purpose, code, amountCents, milestoneId, remarks, idempotencyKey }) {
    return this.c.data('POST', `/escrows/${id}/confirmations/verify`, { body: compact({ purpose, code, amount_cents: amountCents, milestone_id: milestoneId, remarks }), idempotencyKey })
  }
  createProof(id, { filename, contentType, sizeBytes, sha256, description, milestoneId }, opts) {
    return this.c.data('POST', `/escrows/${id}/proofs`, { body: compact({ filename, content_type: contentType, size_bytes: sizeBytes, sha256, description, milestone_id: milestoneId }), ...opts })
  }
  finalizeProof(id, proofId, opts) { return this.c.data('POST', `/escrows/${id}/proofs/${proofId}/finalize`, opts) }
  listProofs(id) { return this.c.data('GET', `/escrows/${id}/proofs`) }
  proofDownload(id, proofId) { return this.c.data('GET', `/escrows/${id}/proofs/${proofId}/download`) }
  async uploadProof(id, { bytes, filename, contentType, description, milestoneId }) {
    const sha256 = createHash('sha256').update(bytes).digest('hex')
    const created = await this.createProof(id, { filename, contentType, sizeBytes: bytes.length, sha256, description, milestoneId })
    const uploadHeaders = Object.fromEntries(Object.entries(created.upload.headers || {}).filter(([k]) => k.toLowerCase() !== 'content-length'))
    const put = await this.c.fetch(created.upload.url, { method: 'PUT', headers: uploadHeaders, body: bytes })
    if (!put.ok) throw new Error(`proof upload failed with status ${put.status}`)
    return this.finalizeProof(id, created.proof.id)
  }
  disputes = {
    list: (id) => this.c.data('GET', `/escrows/${id}/disputes`),
    get: (id, disputeId) => this.c.data('GET', `/escrows/${id}/disputes/${disputeId}`),
    open: (id, reason, opts) => this.c.data('POST', `/escrows/${id}/disputes`, { body: { reason }, ...opts }),
    message: (id, disputeId, body, opts) => this.c.data('POST', `/escrows/${id}/disputes/${disputeId}/messages`, { body: { body }, ...opts }),
    appeal: (id, disputeId, reason, opts) => this.c.data('POST', `/escrows/${id}/disputes/${disputeId}/appeal`, { body: { reason }, ...opts }),
    concede: (id, disputeId, opts) => this.c.data('POST', `/escrows/${id}/disputes/${disputeId}/concede`, opts),
  }
}

class OAuth {
  constructor(client) {
    this.c = client
  }
  // POST /oauth/token. Returns the flat token response; RigePay({ clientId, clientSecret }) calls this for you.
  createToken({ clientId, clientSecret }) {
    return this.c.request('POST', '/oauth/token', { body: { grant_type: 'client_credentials', client_id: clientId, client_secret: clientSecret }, auth: false })
  }
}

class Collections {
  constructor(client) {
    this.c = client
  }
  stkPush({ amountCents, customerPhone, remarks, branchId, metadata, channel, idempotencyKey }) {
    return this.c.data('POST', '/payments/collections/stk-push', { body: compact({ amount_cents: cents('amountCents', amountCents), customer_phone: customerPhone, remarks, branch_id: branchId, metadata, channel }), idempotencyKey })
  }
}

class Checkout {
  constructor(client) {
    this.c = client
  }
  createSession({ amountCents, allowedRails, branchId, description, metadata, idempotencyKey }) {
    return this.c.data('POST', '/payments/checkout/sessions', { body: compact({ amount_cents: cents('amountCents', amountCents), allowed_rails: allowedRails, branch_id: branchId, description, metadata }), idempotencyKey })
  }
  getSession(id) { return this.c.data('GET', `/payments/checkout/sessions/${id}`) }
}

function destination(o) {
  return { destination_type: o.destinationType, phone_number: o.phoneNumber, bank_code: o.bankCode, bank_account_number: o.bankAccountNumber, shortcode: o.shortcode, account_reference: o.accountReference }
}

class Payouts {
  constructor(client) {
    this.c = client
  }
  // Returns the whole 202 body: read `status` (success or approval_required), `payout_id`, `approval_id`.
  create({ amountCents, recipientName, remarks, branchId, metadata, idempotencyKey, ...dest }) {
    return this.c.request('POST', '/payments/payouts', { body: compact({ amount: cents('amountCents', amountCents), recipient_name: recipientName, remarks, ...destination(dest), branch_id: branchId, metadata }), idempotencyKey })
  }
  feeEstimate({ amountCents, destinationType }) {
    return this.c.data('GET', '/payments/payouts/fee-estimate', { query: { amount_cents: cents('amountCents', amountCents), destination_type: destinationType } })
  }
  approvalCeiling({ amountCents } = {}) {
    return this.c.data('GET', '/payments/payouts/approval-ceiling', { query: { amount_cents: cents('amountCents', amountCents) } })
  }
}

class BulkPayouts {
  constructor(client) {
    this.c = client
  }
  create({ items, fundingSource, vaultId, remarks, idempotencyKey }) {
    const rows = items.map(({ amountCents, recipientName, remarks: rowRemarks, ...dest }) =>
      compact({ amount_cents: cents('amountCents', amountCents), recipient_name: recipientName, ...destination(dest), remarks: rowRemarks }))
    return this.c.data('POST', '/payments/bulk-payouts', { body: compact({ funding_source: fundingSource, vault_id: vaultId, remarks, items: rows }), idempotencyKey })
  }
  list() { return this.c.data('GET', '/payments/bulk-payouts') }
  get(batchId) { return this.c.data('GET', `/payments/bulk-payouts/${batchId}`) }
}

class Transfers {
  constructor(client) {
    this.c = client
  }
  create({ from, to, amountCents, remarks, idempotencyKey }) {
    return this.c.data('POST', '/payments/transfers', { body: compact({ from, to, amount: cents('amountCents', amountCents), remarks }), idempotencyKey })
  }
  lookup(code) { return this.c.data('GET', '/payments/transfers/lookup', { query: { code } }) }
  // Returns the whole 202 body: `status` is otp_required, with `transfer_id` and `expires_in`.
  startExternal({ recipientCollectionCode, amountCents, from, remarks, idempotencyKey }) {
    return this.c.request('POST', '/payments/transfers/external', { body: compact({ recipient_collection_code: recipientCollectionCode, amount: cents('amountCents', amountCents), from, remarks }), idempotencyKey })
  }
  confirmExternal({ transferId, otp, idempotencyKey }) {
    return this.c.data('POST', '/payments/transfers/external/confirm', { body: { transfer_id: transferId, otp }, idempotencyKey })
  }
  cancelExternal(transferId, opts) {
    return this.c.data('POST', '/payments/transfers/external/cancel', { body: { transfer_id: transferId }, ...opts })
  }
}

class Balance {
  constructor(client) {
    this.c = client
  }
  get({ branchId } = {}) { return this.c.data('GET', '/payments/balance', { query: { branch_id: branchId } }) }
}

class Transactions {
  constructor(client) {
    this.c = client
  }
  // query uses the API's names: page, page_size, search, status, direction, type, rail, start_date, end_date, min_amount, max_amount, branch_id.
  list(query) { return this.c.data('GET', '/payments/transactions', { query }) }
  get(id) { return this.c.data('GET', `/payments/transactions/${id}`) }
}

class PaymentLinks {
  constructor(client) {
    this.c = client
  }
  // Returns the whole body: the link is in `data`, its shareable address in `url`.
  create({ amountCents, currency, paymentMethods, description, name, isReusable, allowOpenAmount, expiresInDays, maxUses, redirectUrl, cancelUrl, linkType, idempotencyKey }) {
    const body = compact({ amount_cents: cents('amountCents', amountCents), currency, payment_methods: paymentMethods, description, name, is_reusable: isReusable, allow_open_amount: allowOpenAmount, expires_in_days: expiresInDays, max_uses: maxUses, redirect_url: redirectUrl, cancel_url: cancelUrl, link_type: linkType })
    return this.c.request('POST', '/payments/payment-links', { body, idempotencyKey })
  }
  list(query) { return this.c.request('GET', '/payments/payment-links', { query }) }
  get(id) { return this.c.request('GET', `/payments/payment-links/${id}`) }
  close(id, opts) { return this.c.request('POST', `/payments/payment-links/${id}/close`, opts) }
}

class Invoices {
  constructor(client) {
    this.c = client
  }
  // Returns the whole body: the invoice is in `data`, its shareable address in `url`. Item objects are sent as given.
  create({ customerName, customerPhone, customerEmail, currency, dueDate, notes, pricesIncludeVat, items, idempotencyKey }) {
    const body = compact({ customer_name: customerName, customer_phone: customerPhone, customer_email: customerEmail, currency, due_date: dueDate, notes, prices_include_vat: pricesIncludeVat, items })
    return this.c.request('POST', '/payments/invoices', { body, idempotencyKey })
  }
  list(query) { return this.c.request('GET', '/payments/invoices', { query }) }
  get(id) { return this.c.data('GET', `/payments/invoices/${id}`) }
  cancel(id, opts) { return this.c.data('POST', `/payments/invoices/${id}/cancel`, opts) }
}

// Amounts are integer cents. A float would be truncated or rejected server-side, so refuse it here.
function cents(name, v) {
  if (v !== undefined && v !== null && !Number.isSafeInteger(v)) throw new TypeError(`${name} must be an integer number of cents`)
  return v
}

function compact(o) {
  return Object.fromEntries(Object.entries(o).filter(([, v]) => v !== undefined && v !== null))
}

export function sign({ secret, method = 'POST', url, timestamp, requestId, body }) {
  const bodyHash = createHash('sha256').update(body).digest('hex')
  return createHmac('sha256', secret).update(`${method}:${url}:${timestamp}:${requestId}:${bodyHash}`).digest('hex')
}

export function verifyWebhook({ rawBody, headers, secret, url, toleranceSeconds = 300, now = () => Date.now() / 1000 }) {
  const h = (name) => headers[name] ?? headers[name.toLowerCase()]
  const timestamp = h('X-RigePay-Timestamp')
  const requestId = h('X-RigePay-Request-ID')
  const signature = h('X-RigePay-Signature') || ''
  if (!timestamp || !requestId || !signature) return false
  const expected = sign({ secret, url, timestamp, requestId, body: rawBody })
  if (signature.length !== expected.length) return false
  if (!timingSafeEqual(Buffer.from(signature), Buffer.from(expected))) return false
  return Math.abs(now() - Number(timestamp)) <= toleranceSeconds
}

export function parseWebhook({ rawBody, headers, secret, url, toleranceSeconds }) {
  if (!verifyWebhook({ rawBody, headers, secret, url, toleranceSeconds })) throw new Error('invalid RigePay webhook signature')
  const h = (name) => headers[name] ?? headers[name.toLowerCase()]
  return { eventType: h('X-RigePay-Event-Type'), eventId: h('X-RigePay-Event-ID'), body: JSON.parse(rawBody.toString('utf8')) }
}
