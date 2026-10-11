import { test } from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { RigePay, RigePayError, sign, verifyWebhook, parseWebhook } from './index.mjs'

const vectors = JSON.parse(readFileSync(new URL('../vectors.json', import.meta.url), 'utf8')).signature

test('signature matches the value the server signer produces', () => {
  const sig = sign({ secret: vectors.secret, url: vectors.url, timestamp: vectors.timestamp, requestId: vectors.request_id, body: Buffer.from(vectors.body) })
  assert.equal(sig, vectors.signature)
})

test('verifyWebhook accepts the pinned delivery, rejects tampering, wrong URL and old timestamps', () => {
  const headers = { 'X-RigePay-Timestamp': vectors.timestamp, 'X-RigePay-Request-ID': vectors.request_id, 'X-RigePay-Signature': vectors.signature }
  const now = () => Number(vectors.timestamp) + 10
  const base = { rawBody: Buffer.from(vectors.body), headers, secret: vectors.secret, url: vectors.url, now }
  assert.equal(verifyWebhook(base), true)
  assert.equal(verifyWebhook({ ...base, rawBody: Buffer.from(vectors.body + ' ') }), false)
  assert.equal(verifyWebhook({ ...base, url: vectors.url.replace('?src=rigepay', '') }), false, 'the URL is part of the signed message')
  assert.equal(verifyWebhook({ ...base, secret: vectors.secret.replace('whsec_', '') }), false, 'the whole secret including whsec_ is the key')
  assert.equal(verifyWebhook({ ...base, now: () => Number(vectors.timestamp) + 301 }), false)
  assert.equal(verifyWebhook({ ...base, headers: { ...headers, 'X-RigePay-Signature': '' } }), false)
  assert.throws(() => parseWebhook({ ...base, secret: 'whsec_wrong' }))
})

function fakeFetch(responses, calls) {
  return async (url, init) => {
    calls.push({ url: String(url), init })
    const r = responses.shift()
    return new Response(JSON.stringify(r.body ?? {}), { status: r.status, headers: r.headers })
  }
}

test('POST gets an idempotency key and a retry reuses the same key', async () => {
  const calls = []
  const client = new RigePay({ token: 't', maxRetries: 2, fetchImpl: fakeFetch([{ status: 503 }, { status: 200, body: { data: { id: 'e1' } } }], calls) })
  const escrow = await client.escrows.create({ reference: 'r', amount_cents: 100 })
  assert.equal(escrow.id, 'e1')
  assert.equal(calls.length, 2)
  const keys = calls.map((c) => c.init.headers['X-Idempotency-Key'])
  assert.ok(keys[0] && keys[0] === keys[1], 'a retry must use the same idempotency key')
  assert.equal(calls[0].init.headers.Authorization, 'Bearer t')
})

test('a caller supplied key is used, and GET sends none', async () => {
  const calls = []
  const client = new RigePay({ token: 't', fetchImpl: fakeFetch([{ status: 200, body: { data: {} } }, { status: 200, body: { data: {} } }], calls) })
  await client.escrows.release('e1', { milestoneId: 'm1', idempotencyKey: 'order-1-release' })
  await client.escrows.get('e1')
  assert.equal(calls[0].init.headers['X-Idempotency-Key'], 'order-1-release')
  assert.deepEqual(JSON.parse(calls[0].init.body), { milestone_id: 'm1' })
  assert.equal(calls[1].init.headers['X-Idempotency-Key'], undefined)
})

test('4xx errors are not retried and carry the code', async () => {
  const calls = []
  const client = new RigePay({ token: 't', maxRetries: 3, fetchImpl: fakeFetch([{ status: 409, body: { status: 'error', code: 'dispute_active', message: 'frozen' } }], calls) })
  await assert.rejects(client.escrows.release('e1', { amountCents: 5 }), (e) => e instanceof RigePayError && e.status === 409 && e.code === 'dispute_active')
  assert.equal(calls.length, 1)
})

test('429 honours Retry-After and gives up after maxRetries', async () => {
  const calls = []
  const client = new RigePay({ token: 't', maxRetries: 1, fetchImpl: fakeFetch([{ status: 429, headers: { 'retry-after': '0' } }, { status: 429, body: { code: 'rate_limit_exceeded' } }], calls) })
  await assert.rejects(client.escrows.get('e1'), (e) => e.status === 429 && e.code === 'rate_limit_exceeded')
  assert.equal(calls.length, 2)
})

test('uploadProof declares the SHA-256, uploads without a Content-Length header and finalizes', async () => {
  const calls = []
  const bytes = Buffer.from('signed delivery note')
  const client = new RigePay({
    token: 't',
    fetchImpl: fakeFetch([
      { status: 201, body: { data: { proof: { id: 'p1' }, upload: { url: 'https://storage.example.com/put', headers: { 'Content-Type': 'application/pdf', 'Content-Length': '20' } } } } },
      { status: 200, body: {} },
      { status: 200, body: { data: { proof: { id: 'p1', status: 'FINALIZED' } } } },
    ], calls),
  })
  const out = await client.escrows.uploadProof('e1', { bytes, filename: 'note.pdf', contentType: 'application/pdf', milestoneId: 'm1' })
  assert.equal(out.proof.status, 'FINALIZED')
  assert.equal(calls.length, 3)
  const declared = JSON.parse(calls[0].init.body)
  assert.equal(declared.size_bytes, bytes.length)
  assert.equal(declared.milestone_id, 'm1')
  assert.match(declared.sha256, /^[0-9a-f]{64}$/)
  assert.equal(calls[1].init.method, 'PUT')
  assert.deepEqual(Object.keys(calls[1].init.headers), ['Content-Type'])
  assert.ok(calls[2].url.endsWith('/escrows/e1/proofs/p1/finalize'))
})
