import { RigePay, RigePayError, verifyWebhook, parseWebhook, sign } from './index.mjs'

const client = new RigePay({ token: 'k', maxRetries: 2 })
const created: Record<string, unknown> = await client.escrows.create({ amount_cents: 1000 }, { idempotencyKey: 'a' })
await client.escrows.release('id', { amountCents: 500, remarks: 'ok', idempotencyKey: 'b' })
await client.escrows.disputes.open('id', 'not delivered')
const list = await client.escrows.list({ status: 'FUNDED', page: 2 })
const rows: Record<string, unknown>[] = list.data
await client.escrows.uploadProof('id', { bytes: new TextEncoder().encode('x'), filename: 'a.pdf', contentType: 'application/pdf' })

const ok: boolean = verifyWebhook({ rawBody: new TextEncoder().encode('{}'), headers: { 'X-RigePay-Signature': 's' }, secret: 'k', url: 'https://x' })
const parsed = parseWebhook({ rawBody: '{}', headers: {}, secret: 'k', url: 'https://x' })
const sig: string = sign({ secret: 'k', url: 'https://x', timestamp: 1, requestId: 'r', body: '{}' })
try { void created; void rows; void ok; void parsed; void sig } catch (e) { if (e instanceof RigePayError) console.log(e.status, e.code) }

// @ts-expect-error token is required
new RigePay({})
// @ts-expect-error amountCents must be a number
await client.escrows.release('id', { amountCents: '500' })
