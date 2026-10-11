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
  constructor({ token, baseUrl = DEFAULT_BASE_URL, maxRetries = 3, timeoutMs = 30000, fetchImpl = globalThis.fetch } = {}) {
    if (!token) throw new Error('token is required')
    this.token = token
    this.baseUrl = baseUrl.replace(/\/+$/, '')
    this.maxRetries = maxRetries
    this.timeoutMs = timeoutMs
    this.fetch = fetchImpl
    this.escrows = new Escrows(this)
  }

  async request(method, path, { body, query, idempotencyKey } = {}) {
    const url = new URL(this.baseUrl + path)
    for (const [k, v] of Object.entries(query || {})) if (v !== undefined && v !== null) url.searchParams.set(k, String(v))
    const headers = { Authorization: `Bearer ${this.token}`, Accept: 'application/json' }
    if (body !== undefined) headers['Content-Type'] = 'application/json'
    const key = method !== 'GET' ? idempotencyKey || randomUUID() : undefined
    if (key) headers['X-Idempotency-Key'] = key
    const payload = body === undefined ? undefined : JSON.stringify(body)

    for (let attempt = 0; ; attempt++) {
      let res
      try {
        res = await this.fetch(url, { method, headers, body: payload, signal: AbortSignal.timeout(this.timeoutMs) })
      } catch (err) {
        if (attempt >= this.maxRetries) throw err
        await sleep(backoff(attempt, 0))
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
        throw new RigePayError({ status: res.status, code: json.code, message: json.message, requestId: res.headers.get('x-request-id') || undefined, body: json })
      }
      return json
    }
  }

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
