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

const oauthClient = new RigePay({ clientId: 'id', clientSecret: 'secret' })
const payout = await oauthClient.payouts.create({ amountCents: 10000, recipientName: 'Jane', remarks: 'May', phoneNumber: '0700000000' })
const pending: boolean = payout.status === 'approval_required'
const page = await oauthClient.transactions.list({ page: 1, direction: 'CREDIT' })
const firstAmount: number | undefined = page.transactions[0]?.amountCents
const push = await oauthClient.collections.stkPush({ amountCents: 1000, customerPhone: '+254700000000', channel: 'mpesa' })
const started = await oauthClient.transfers.startExternal({ recipientCollectionCode: 'ABC', amountCents: 1000, remarks: 'r' })
await oauthClient.transfers.confirmExternal({ transferId: started.transfer_id, otp: '123456' })
void pending; void firstAmount; void push.reference

// @ts-expect-error token is required
new RigePay({})
// @ts-expect-error clientSecret is required with clientId
new RigePay({ clientId: 'id' })
// @ts-expect-error amountCents must be a number
await client.escrows.release('id', { amountCents: '500' })
