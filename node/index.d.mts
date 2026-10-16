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

export interface RigePayOptions {
  token: string
  baseUrl?: string
  maxRetries?: number
  timeoutMs?: number
  fetchImpl?: FetchLike
}

export interface RequestOptions {
  body?: unknown
  query?: Record<string, string | number | boolean | null | undefined>
  idempotencyKey?: string
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

export class RigePay {
  constructor(options: RigePayOptions)
  token: string
  baseUrl: string
  maxRetries: number
  timeoutMs: number
  fetch: FetchLike
  escrows: Escrows
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
