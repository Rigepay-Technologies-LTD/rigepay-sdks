import { test } from 'node:test'
import assert from 'node:assert/strict'
import { RigePay, RigePayError } from './index.mjs'

function fakeFetch(responses, calls) {
  return async (url, init) => {
    calls.push({ url: new URL(String(url)), init })
    const r = responses.shift() ?? { status: 200, body: { status: 'success', data: {} } }
    return new Response(JSON.stringify(r.body ?? {}), { status: r.status, headers: r.headers })
  }
}

const BASE = 'https://api.rigepay.co.ke/api/v1'

// [name, call, method, path, query, body]
const cases = [
  ['whoami', (c) => c.whoami(), 'GET', '/payments/whoami'],
  ['collections.stkPush', (c) => c.collections.stkPush({ amountCents: 1000, customerPhone: '+254700000000', remarks: 'Order 1', channel: 'mpesa', metadata: { order: '1' } }), 'POST', '/payments/collections/stk-push', {}, { amount_cents: 1000, customer_phone: '+254700000000', remarks: 'Order 1', channel: 'mpesa', metadata: { order: '1' } }],
  ['checkout.createSession', (c) => c.checkout.createSession({ amountCents: 2500, allowedRails: ['mpesa_stk', 'card'], description: 'Cart' }), 'POST', '/payments/checkout/sessions', {}, { amount_cents: 2500, allowed_rails: ['mpesa_stk', 'card'], description: 'Cart' }],
  ['checkout.getSession', (c) => c.checkout.getSession('s1'), 'GET', '/payments/checkout/sessions/s1'],
  ['payouts.create', (c) => c.payouts.create({ amountCents: 15000, recipientName: 'Jane', remarks: 'May', destinationType: 'PAYBILL', shortcode: '123456', accountReference: 'ACC1' }), 'POST', '/payments/payouts', {}, { amount: 15000, recipient_name: 'Jane', remarks: 'May', destination_type: 'PAYBILL', shortcode: '123456', account_reference: 'ACC1' }],
  ['payouts.feeEstimate', (c) => c.payouts.feeEstimate({ amountCents: 15000, destinationType: 'BANK_ACCOUNT' }), 'GET', '/payments/payouts/fee-estimate', { amount_cents: '15000', destination_type: 'BANK_ACCOUNT' }],
  ['payouts.approvalCeiling', (c) => c.payouts.approvalCeiling({ amountCents: 9000000 }), 'GET', '/payments/payouts/approval-ceiling', { amount_cents: '9000000' }],
  ['bulkPayouts.create', (c) => c.bulkPayouts.create({ fundingSource: 'MAIN', items: [{ amountCents: 100, recipientName: 'A', phoneNumber: '0700000001' }, { amountCents: 200, recipientName: 'B', destinationType: 'BANK_ACCOUNT', bankCode: '01', bankAccountNumber: '123' }] }), 'POST', '/payments/bulk-payouts', {}, { funding_source: 'MAIN', items: [{ amount_cents: 100, recipient_name: 'A', phone_number: '0700000001' }, { amount_cents: 200, recipient_name: 'B', destination_type: 'BANK_ACCOUNT', bank_code: '01', bank_account_number: '123' }] }],
  ['bulkPayouts.list', (c) => c.bulkPayouts.list(), 'GET', '/payments/bulk-payouts'],
  ['bulkPayouts.get', (c) => c.bulkPayouts.get('b1'), 'GET', '/payments/bulk-payouts/b1'],
  ['transfers.create', (c) => c.transfers.create({ from: 'org', to: 'br1', amountCents: 1000, remarks: 'float' }), 'POST', '/payments/transfers', {}, { from: 'org', to: 'br1', amount: 1000, remarks: 'float' }],
  ['transfers.lookup', (c) => c.transfers.lookup('ACME01'), 'GET', '/payments/transfers/lookup', { code: 'ACME01' }],
  ['transfers.startExternal', (c) => c.transfers.startExternal({ recipientCollectionCode: 'ACME01', amountCents: 5000, remarks: 'stock' }), 'POST', '/payments/transfers/external', {}, { recipient_collection_code: 'ACME01', amount: 5000, remarks: 'stock' }],
  ['transfers.confirmExternal', (c) => c.transfers.confirmExternal({ transferId: 't1', otp: '123456' }), 'POST', '/payments/transfers/external/confirm', {}, { transfer_id: 't1', otp: '123456' }],
  ['transfers.cancelExternal', (c) => c.transfers.cancelExternal('t1'), 'POST', '/payments/transfers/external/cancel', {}, { transfer_id: 't1' }],
  ['balance.get', (c) => c.balance.get({ branchId: 'br1' }), 'GET', '/payments/balance', { branch_id: 'br1' }],
  ['transactions.list', (c) => c.transactions.list({ page: 2, page_size: 50, direction: 'CREDIT', min_amount: 100 }), 'GET', '/payments/transactions', { page: '2', page_size: '50', direction: 'CREDIT', min_amount: '100' }],
  ['transactions.get', (c) => c.transactions.get('x1'), 'GET', '/payments/transactions/x1'],
  ['paymentLinks.create', (c) => c.paymentLinks.create({ amountCents: 1000, currency: 'KES', name: 'Deposit', isReusable: true, expiresInDays: 7 }), 'POST', '/payments/payment-links', {}, { amount_cents: 1000, currency: 'KES', name: 'Deposit', is_reusable: true, expires_in_days: 7 }],
  ['paymentLinks.list', (c) => c.paymentLinks.list({ page: 1 }), 'GET', '/payments/payment-links', { page: '1' }],
  ['paymentLinks.get', (c) => c.paymentLinks.get('l1'), 'GET', '/payments/payment-links/l1'],
  ['paymentLinks.close', (c) => c.paymentLinks.close('l1'), 'POST', '/payments/payment-links/l1/close'],
  ['invoices.create', (c) => c.invoices.create({ customerName: 'Acme', customerPhone: '0700000000', currency: 'KES', dueDate: '2026-12-01T00:00:00+03:00', items: [{ description: 'Widget', quantity: 2, unit_price_cents: 5000 }] }), 'POST', '/payments/invoices', {}, { customer_name: 'Acme', customer_phone: '0700000000', currency: 'KES', due_date: '2026-12-01T00:00:00+03:00', items: [{ description: 'Widget', quantity: 2, unit_price_cents: 5000 }] }],
  ['invoices.list', (c) => c.invoices.list({ status: 'PAID' }), 'GET', '/payments/invoices', { status: 'PAID' }],
  ['invoices.get', (c) => c.invoices.get('i1'), 'GET', '/payments/invoices/i1'],
  ['invoices.cancel', (c) => c.invoices.cancel('i1'), 'POST', '/payments/invoices/i1/cancel'],
]

for (const [name, call, method, path, query = {}, body] of cases) {
  test(`${name} sends ${method} ${path}`, async () => {
    const calls = []
    const client = new RigePay({ token: 'k', fetchImpl: fakeFetch([], calls) })
    await call(client)
    assert.equal(calls.length, 1)
    const { url, init } = calls[0]
    assert.equal(init.method, method)
    assert.equal(url.origin + url.pathname, BASE + path)
    assert.deepEqual(Object.fromEntries(url.searchParams), query)
    assert.deepEqual(init.body === undefined ? undefined : JSON.parse(init.body), body)
    assert.equal(init.headers.Authorization, 'Bearer k')
    if (method === 'POST') assert.ok(init.headers['X-Idempotency-Key'], 'every POST carries an idempotency key')
    else assert.equal(init.headers['X-Idempotency-Key'], undefined)
  })
}

test('flat responses come back whole, data responses come back unwrapped', async () => {
  const calls = []
  const client = new RigePay({
    token: 'k',
    fetchImpl: fakeFetch([
      { status: 202, body: { status: 'approval_required', payout_id: 'p1', approval_id: 'a1', amount: 15000, fee_cents: 50 } },
      { status: 202, body: { status: 'otp_required', transfer_id: 't1', expires_in: 600 } },
      { status: 201, body: { status: 'success', data: { id: 'l1' }, url: 'https://pay.rigepay.co.ke/abc' } },
      { status: 200, body: { status: 'success', data: { amount_cents: 15000, fee_cents: 50, total_cents: 15050, is_estimate: false } } },
    ], calls),
  })
  const payout = await client.payouts.create({ amountCents: 15000, recipientName: 'J', remarks: 'r', phoneNumber: '0700000000' })
  assert.equal(payout.status, 'approval_required')
  assert.equal(payout.approval_id, 'a1')
  const started = await client.transfers.startExternal({ recipientCollectionCode: 'C', amountCents: 1000, remarks: 'r' })
  assert.equal(started.transfer_id, 't1')
  const link = await client.paymentLinks.create({ amountCents: 1000, currency: 'KES' })
  assert.equal(link.url, 'https://pay.rigepay.co.ke/abc')
  assert.equal(link.data.id, 'l1')
  const fee = await client.payouts.feeEstimate({ amountCents: 15000 })
  assert.equal(fee.total_cents, 15050)
})

test('a payout retried after 503 reuses its idempotency key; a caller key is used as given', async () => {
  const calls = []
  const client = new RigePay({ token: 'k', maxRetries: 2, fetchImpl: fakeFetch([{ status: 503 }, { status: 502 }, { status: 202, body: { status: 'success', payout_id: 'p1' } }, { status: 200, body: { data: {} } }], calls) })
  await client.payouts.create({ amountCents: 100, recipientName: 'J', remarks: 'r', phoneNumber: '0700000000' })
  const keys = calls.map((c) => c.init.headers['X-Idempotency-Key'])
  assert.equal(keys.length, 3)
  assert.ok(keys[0] && keys.every((k) => k === keys[0]))
  await client.collections.stkPush({ amountCents: 1000, customerPhone: '0700000000', idempotencyKey: 'order-9' })
  assert.equal(calls[3].init.headers['X-Idempotency-Key'], 'order-9')
})

test('amounts must be integer cents', () => {
  const client = new RigePay({ token: 'k', fetchImpl: fakeFetch([], []) })
  assert.throws(() => client.collections.stkPush({ amountCents: 10.5, customerPhone: '0700000000' }), TypeError)
  assert.throws(() => client.payouts.create({ amountCents: '100', recipientName: 'J', remarks: 'r' }), TypeError)
  assert.throws(() => client.bulkPayouts.create({ items: [{ amountCents: 1.25, recipientName: 'A' }, { amountCents: 100, recipientName: 'B' }] }), TypeError)
})

function tokenServer(calls, { expiresIn = 3600, apiStatuses = [] } = {}) {
  let issued = 0
  return async (url, init) => {
    const u = new URL(String(url))
    calls.push({ url: u, init })
    if (u.pathname.endsWith('/oauth/token')) {
      issued++
      return new Response(JSON.stringify({ access_token: `at-${issued}`, token_type: 'Bearer', expires_in: expiresIn, scope: 'payouts:read' }), { status: 200 })
    }
    const status = apiStatuses.shift() ?? 200
    return new Response(JSON.stringify(status === 200 ? { status: 'success', data: { ok: true } } : { status: 'error', message: 'unauthorized' }), { status })
  }
}

test('client credentials: the token request is unauthenticated and the token is cached until shortly before expiry', async () => {
  const calls = []
  let now = 1_000_000
  const client = new RigePay({ clientId: 'cid', clientSecret: 'csecret', now: () => now, fetchImpl: tokenServer(calls) })
  await Promise.all([client.whoami(), client.balance.get()])
  await client.transactions.list()
  const tokenCalls = calls.filter((c) => c.url.pathname.endsWith('/oauth/token'))
  assert.equal(tokenCalls.length, 1, 'concurrent and later calls share one token')
  assert.equal(tokenCalls[0].init.method, 'POST')
  assert.equal(tokenCalls[0].init.headers.Authorization, undefined)
  assert.deepEqual(JSON.parse(tokenCalls[0].init.body), { grant_type: 'client_credentials', client_id: 'cid', client_secret: 'csecret' })
  const apiCalls = calls.filter((c) => !c.url.pathname.endsWith('/oauth/token'))
  assert.ok(apiCalls.every((c) => c.init.headers.Authorization === 'Bearer at-1'))

  now += (3600 - 61) * 1000
  await client.whoami()
  assert.equal(calls.filter((c) => c.url.pathname.endsWith('/oauth/token')).length, 1, 'still inside the cache window')
  now += 2000
  await client.whoami()
  assert.equal(calls.filter((c) => c.url.pathname.endsWith('/oauth/token')).length, 2, 'refreshed within 60s of expiry')
  assert.equal(calls.at(-1).init.headers.Authorization, 'Bearer at-2')
})

test('client credentials: a 401 refreshes the token once and retries with the same idempotency key', async () => {
  const calls = []
  const client = new RigePay({ clientId: 'cid', clientSecret: 's', fetchImpl: tokenServer(calls, { apiStatuses: [401, 200] }) })
  await client.collections.stkPush({ amountCents: 1000, customerPhone: '0700000000' })
  const api = calls.filter((c) => !c.url.pathname.endsWith('/oauth/token'))
  assert.equal(api.length, 2)
  assert.equal(api[0].init.headers.Authorization, 'Bearer at-1')
  assert.equal(api[1].init.headers.Authorization, 'Bearer at-2')
  assert.equal(api[0].init.headers['X-Idempotency-Key'], api[1].init.headers['X-Idempotency-Key'])

  const again = []
  const stuck = new RigePay({ clientId: 'cid', clientSecret: 's', fetchImpl: tokenServer(again, { apiStatuses: [401, 401] }) })
  await assert.rejects(stuck.whoami(), (e) => e instanceof RigePayError && e.status === 401)
})

test('OAuth errors surface their `error` as the code', async () => {
  const calls = []
  const client = new RigePay({ token: 'k', fetchImpl: fakeFetch([{ status: 401, body: { status: 'error', error: 'invalid_client', message: 'bad secret' } }], calls) })
  await assert.rejects(client.oauth.createToken({ clientId: 'a', clientSecret: 'b' }), (e) => e.status === 401 && e.code === 'invalid_client')
  assert.equal(calls[0].init.headers['X-Idempotency-Key'], undefined)
})

test('path ids are URL-encoded', async () => {
  const calls = []
  const client = new RigePay({ token: 'k', fetchImpl: fakeFetch([], calls) })
  await client.transactions.get('a/b?c')
  await client.paymentLinks.close('../x')
  assert.equal(calls[0].url.pathname, '/api/v1/payments/transactions/a%2Fb%3Fc')
  assert.equal(calls[0].url.search, '')
  assert.equal(calls[1].url.pathname, '/api/v1/payments/payment-links/..%2Fx/close')
})

test('a late 401 for an old token does not discard the token another request just fetched', async () => {
  const calls = []
  let issued = 0
  let at2Issued
  const at2 = new Promise((resolve) => { at2Issued = resolve })
  const fetchImpl = async (url, init) => {
    const u = new URL(String(url))
    calls.push({ url: u, init })
    if (u.pathname.endsWith('/oauth/token')) {
      issued++
      if (issued === 2) at2Issued()
      return new Response(JSON.stringify({ access_token: `at-${issued}`, token_type: 'Bearer', expires_in: 3600, scope: 'x' }), { status: 200 })
    }
    if (init.headers.Authorization === 'Bearer at-1') {
      // The balance request's 401 arrives only after the whoami request has refreshed and finished.
      if (u.pathname.endsWith('/balance')) { await at2; await new Promise((r) => setTimeout(r, 20)) }
      return new Response(JSON.stringify({ status: 'error', message: 'revoked' }), { status: 401 })
    }
    return new Response(JSON.stringify({ status: 'success', data: {} }), { status: 200 })
  }
  const client = new RigePay({ clientId: 'cid', clientSecret: 's', fetchImpl })
  await Promise.all([client.whoami(), client.balance.get()])
  assert.equal(issued, 2, 'the second 401 must reuse at-2, not fetch at-3')
  assert.equal(calls.at(-1).init.headers.Authorization, 'Bearer at-2')
})
