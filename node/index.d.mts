export interface RigePayErrorInit {
  status: number
  code?: string
  message?: string
  requestId?: string
  body?: unknown
}

export class RigePayError extends Error {
  constructor(init: RigePayErrorInit)
  name: 'RigePayError'
  status: number
  code?: string
  requestId?: string
  body?: unknown
}

export type FetchLike = (input: string | URL, init?: RequestInit) => Promise<Response>

interface BaseOptions {
  baseUrl?: string
  maxRetries?: number
  timeoutMs?: number
  fetchImpl?: FetchLike
}

export interface TokenOptions extends BaseOptions {
  /** A static API key (OAK_...) or an access token you manage yourself. */
  token: string
  clientId?: never
  clientSecret?: never
}

export interface ClientCredentialsOptions extends BaseOptions {
  token?: never
  /** OAuth2 client credentials. The access token is fetched from /oauth/token and cached. */
  clientId: string
  clientSecret: string
  /** Refresh this long before the token expires. Default 60000. */
  tokenRefreshSkewMs?: number
  /** Clock in milliseconds, for tests. Default Date.now. */
  now?: () => number
}

export type RigePayOptions = TokenOptions | ClientCredentialsOptions

export interface RequestOptions {
  body?: unknown
  query?: Record<string, string | number | boolean | null | undefined>
  idempotencyKey?: string
  /** false sends no Authorization or idempotency header (used for /oauth/token). */
  auth?: boolean
}

export interface WriteOptions {
  idempotencyKey?: string
}

export type Json = Record<string, unknown>

export interface ListResponse<T = Json> {
  status: string
  data: T[]
  [key: string]: unknown
}

export interface ReleaseOrRefundInput extends WriteOptions {
  amountCents?: number
  milestoneId?: string
  confirmationId?: string
  remarks?: string
}

export interface RequestConfirmationInput {
  purpose: string
  amountCents?: number
  milestoneId?: string
}

export interface VerifyConfirmationInput extends WriteOptions {
  purpose: string
  code: string
  amountCents?: number
  milestoneId?: string
  remarks?: string
}

export interface CreateProofInput {
  filename: string
  contentType: string
  sizeBytes: number
  sha256: string
  description?: string
  milestoneId?: string
}

export interface UploadProofInput {
  bytes: Uint8Array
  filename: string
  contentType: string
  description?: string
  milestoneId?: string
}

export interface EscrowDisputes {
  list(id: string): Promise<Json[]>
  get(id: string, disputeId: string): Promise<Json>
  open(id: string, reason: string, opts?: WriteOptions): Promise<Json>
  message(id: string, disputeId: string, body: string, opts?: WriteOptions): Promise<Json>
  appeal(id: string, disputeId: string, reason: string, opts?: WriteOptions): Promise<Json>
  concede(id: string, disputeId: string, opts?: WriteOptions): Promise<Json>
}

export interface Escrows {
  create(input: Json, opts?: WriteOptions): Promise<Json>
  get(id: string): Promise<Json>
  list(query?: RequestOptions['query']): Promise<ListResponse>
  events(id: string, query?: RequestOptions['query']): Promise<Json[]>
  fund(id: string, opts?: WriteOptions & { body?: unknown }): Promise<Json>
  fundingLink(id: string, opts?: WriteOptions & { body?: unknown }): Promise<Json>
  release(id: string, input?: ReleaseOrRefundInput): Promise<Json>
  refund(id: string, input?: ReleaseOrRefundInput): Promise<Json>
  cancel(id: string, opts?: WriteOptions & { body?: unknown }): Promise<Json>
  requestConfirmation(id: string, input: RequestConfirmationInput): Promise<Json>
  verifyConfirmation(id: string, input: VerifyConfirmationInput): Promise<Json>
  createProof(id: string, input: CreateProofInput, opts?: WriteOptions): Promise<Json>
  finalizeProof(id: string, proofId: string, opts?: WriteOptions): Promise<Json>
  listProofs(id: string): Promise<Json[]>
  proofDownload(id: string, proofId: string): Promise<Json>
  uploadProof(id: string, input: UploadProofInput): Promise<Json>
  disputes: EscrowDisputes
}

/** Integer cents. Floats are rejected with a TypeError. */
export type Cents = number

export interface TokenResponse {
  access_token: string
  token_type: 'Bearer'
  expires_in: number
  scope: string
}

export interface OAuth {
  createToken(input: { clientId: string; clientSecret: string }): Promise<TokenResponse>
}

export interface WhoAmI {
  organization_id?: string
  organization_status?: string
  branch_ids?: string[] | null
  api_client_id?: string
  api_client_status?: string
  scopes?: string[]
  role?: string
  auth_type?: string
  expires_in?: number
}

export type Channel = 'mpesa' | 'airtel' | 'tkash'
export type Rail = 'mpesa_stk' | 'mpesa_c2b' | 'bank_transfer' | 'card' | 'mobile_money' | 'paybill' | 'till' | 'other'
export type DestinationType = 'PHONE_NUMBER' | 'POCHI' | 'BANK_ACCOUNT' | 'PAYBILL' | 'TILL_NUMBER'

export interface StkPushInput extends WriteOptions {
  amountCents: Cents
  customerPhone: string
  remarks?: string
  branchId?: string
  metadata?: Json
  channel?: Channel
}

export interface StkPushResult {
  reference: string
  checkout_request_id?: string
  merchant_request_id?: string
  customer_message?: string
  rail?: 'mpesa_stk'
  channel?: string
  requires_otp?: boolean
}

export interface Collections {
  stkPush(input: StkPushInput): Promise<StkPushResult>
}

export interface CheckoutSessionInput extends WriteOptions {
  amountCents: Cents
  allowedRails: Array<'mpesa_stk' | 'mpesa_c2b' | 'bank_transfer' | 'card'>
  branchId?: string
  description?: string
  metadata?: Json
}

export interface CheckoutSession {
  session_id: string
  status: 'pending' | 'paid' | 'expired' | 'failed'
  completed_via_rail?: Rail
  expires_at: string
  amount_cents: number
  currency: string
  reference: string
  checkout_url: string
}

export interface Checkout {
  createSession(input: CheckoutSessionInput): Promise<CheckoutSession>
  getSession(id: string): Promise<CheckoutSession>
}

export interface Destination {
  destinationType?: DestinationType
  phoneNumber?: string
  bankCode?: string
  bankAccountNumber?: string
  shortcode?: string
  accountReference?: string
}

export interface PayoutInput extends Destination, WriteOptions {
  /** Sent as `amount` (cents). */
  amountCents: Cents
  recipientName: string
  remarks: string
  branchId?: string
  metadata?: Json
}

export interface PayoutResult {
  status: 'success' | 'approval_required'
  message?: string
  payout_id?: string
  approval_id?: string
  amount?: number
  fee_cents?: number
}

export interface FeeEstimate {
  amount_cents: number
  fee_cents: number
  total_cents: number
  is_estimate: boolean
}

export interface ApprovalCeiling {
  applies?: boolean
  configured?: boolean
  amount_cents?: number
  active?: boolean
  active_owner_count?: number
  enforceable?: boolean
}

export interface Payouts {
  create(input: PayoutInput): Promise<PayoutResult>
  feeEstimate(input: { amountCents: Cents; destinationType?: DestinationType }): Promise<FeeEstimate>
  approvalCeiling(input?: { amountCents?: Cents }): Promise<ApprovalCeiling>
}

export interface BulkPayoutItem extends Destination {
  amountCents: Cents
  recipientName: string
  remarks?: string
}

export interface BulkPayoutInput extends WriteOptions {
  items: BulkPayoutItem[]
  fundingSource?: 'MAIN' | 'VAULT'
  vaultId?: string
  remarks?: string
}

export interface BulkPayoutBatch {
  id: string
  item_count: number
  total_amount_cents: number
  total_fee_reserve_cents: number
  status?: 'ESCROWED' | 'DISPATCHED'
  dispatched_count?: number
  rejected_count?: number
  remarks?: string
  created_at?: string
}

export interface BulkPayoutDetail {
  batch: BulkPayoutBatch
  items: Array<{ row_number: number; amount_cents: number; status: 'PENDING' | 'DISPATCHED' | 'REJECTED'; payout_id?: string; payout_status?: string; rejection_reason?: string }>
  escrow_balance_cents?: number
}

export interface BulkPayouts {
  create(input: BulkPayoutInput): Promise<BulkPayoutBatch>
  list(): Promise<BulkPayoutBatch[]>
  get(batchId: string): Promise<BulkPayoutDetail>
}

export interface TransferInput extends WriteOptions {
  /** "org" or a branch UUID. */
  from: string
  to: string
  /** Sent as `amount` (cents). */
  amountCents: Cents
  remarks?: string
}

export interface ExternalTransferInput extends WriteOptions {
  recipientCollectionCode: string
  /** Sent as `amount` (cents). */
  amountCents: Cents
  /** A branch UUID to send from; omit for the organization wallet. */
  from?: string
  remarks: string
}

export interface Transfers {
  create(input: TransferInput): Promise<{ txn_id: string; amount_cents: number; from: string; to: string }>
  lookup(code: string): Promise<{ name: string; type: string; collection_code: string }>
  startExternal(input: ExternalTransferInput): Promise<{ status: 'otp_required'; message?: string; transfer_id: string; expires_in: number }>
  confirmExternal(input: { transferId: string; otp: string } & WriteOptions): Promise<{ transfer_id: string; amount_cents: number; from: string; to: string }>
  cancelExternal(transferId: string, opts?: WriteOptions): Promise<{ transfer_id: string }>
}

export interface BalanceResult {
  branch_id?: string
  balances: { main_cents: number; escrow_cents: number; chargeback_cents: number }
  wallets: Array<{ type: 'MAIN' | 'ESCROW' | 'CHARGEBACK'; wallet_name: string; currency: string; balance_bigint: number; available_bigint: number; status: 'active' | 'frozen' | 'closed' }>
}

export interface Balance {
  get(input?: { branchId?: string }): Promise<BalanceResult>
}

export interface Transaction {
  id: string
  type: string
  direction?: 'CREDIT' | 'DEBIT'
  status: string
  amountCents: number
  feeCents: number
  currency: string
  rail?: Rail
  reference: string
  externalTxnId?: string | null
  description?: string
  customer_phone?: string
  created_at: string
  updated_at: string
}

export interface TransactionQuery {
  page?: number
  page_size?: number
  search?: string
  status?: string
  direction?: 'CREDIT' | 'DEBIT'
  type?: string
  rail?: Rail
  start_date?: string
  end_date?: string
  min_amount?: number
  max_amount?: number
  branch_id?: string
}

export interface TransactionPage {
  total_count: number
  page: number
  page_size: number
  total_pages: number
  transactions: Transaction[]
}

export interface Transactions {
  list(query?: TransactionQuery): Promise<TransactionPage>
  get(id: string): Promise<Transaction>
}

export interface PaymentLinkInput extends WriteOptions {
  amountCents?: Cents
  currency: 'KES' | 'USD'
  /** Comma-separated: mobile_money, card, bank. */
  paymentMethods?: string
  description?: string
  name?: string
  isReusable?: boolean
  allowOpenAmount?: boolean
  expiresInDays?: number
  maxUses?: number
  redirectUrl?: string
  cancelUrl?: string
  linkType?: 'STANDARD' | 'INVOICE' | 'CUSTOMER' | 'DONATION'
}

export interface PaymentLinks {
  /** The whole response body: the link in `data`, the shareable address in `url`. */
  create(input: PaymentLinkInput): Promise<Json>
  list(query?: RequestOptions['query']): Promise<Json>
  get(id: string): Promise<Json>
  close(id: string, opts?: WriteOptions): Promise<Json>
}

export interface InvoiceInput extends WriteOptions {
  customerName: string
  customerPhone: string
  customerEmail?: string | null
  currency: string
  /** RFC 3339 date-time. */
  dueDate: string
  notes?: string | null
  pricesIncludeVat?: boolean
  items: Json[]
}

export interface Invoices {
  /** The whole response body: the invoice in `data`, the shareable address in `url`. */
  create(input: InvoiceInput): Promise<Json>
  list(query?: RequestOptions['query']): Promise<Json>
  get(id: string): Promise<Json>
  cancel(id: string, opts?: WriteOptions): Promise<Json>
}

export class RigePay {
  constructor(options: RigePayOptions)
  token?: string
  clientId?: string
  baseUrl: string
  maxRetries: number
  timeoutMs: number
  fetch: FetchLike
  escrows: Escrows
  oauth: OAuth
  collections: Collections
  checkout: Checkout
  payouts: Payouts
  bulkPayouts: BulkPayouts
  transfers: Transfers
  balance: Balance
  transactions: Transactions
  paymentLinks: PaymentLinks
  invoices: Invoices
  /** The static token, or a cached client-credentials access token. */
  getToken(): Promise<string>
  whoami(): Promise<WhoAmI>
  request(method: string, path: string, options?: RequestOptions): Promise<Json>
  data<T = Json>(method: string, path: string, options?: RequestOptions): Promise<T>
}

export interface SignInput {
  secret: string
  method?: string
  url: string
  timestamp: string | number
  requestId: string
  body: string | Uint8Array
}

export function sign(input: SignInput): string

export type WebhookHeaders = Record<string, string | undefined>

export interface VerifyWebhookInput {
  rawBody: string | Uint8Array
  headers: WebhookHeaders
  secret: string
  url: string
  toleranceSeconds?: number
  now?: () => number
}

export function verifyWebhook(input: VerifyWebhookInput): boolean

export interface ParsedWebhook {
  eventType?: string
  eventId?: string
  body: unknown
}

export function parseWebhook(input: Omit<VerifyWebhookInput, 'now'>): ParsedWebhook
