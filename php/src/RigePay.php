<?php

declare(strict_types=1);

namespace RigePay;

class RigePayException extends \RuntimeException
{
    public function __construct(public int $status, public ?string $errorCode, string $message, public mixed $body = null)
    {
        parent::__construct("{$status} " . ($errorCode ?? '') . ": {$message}");
    }
}

/** Dependency-free RigePay client (PHP 8.1+, ext-curl). POSTs get an idempotency key that retries reuse. */
class Client
{
    public Escrows $escrows;
    public OAuth $oauth;
    public Collections $collections;
    public Checkout $checkout;
    public Payouts $payouts;
    public BulkPayouts $bulkPayouts;
    public Transfers $transfers;
    public Balance $balance;
    public Transactions $transactions;
    public PaymentLinks $paymentLinks;
    public Invoices $invoices;

    private ?string $accessToken = null;
    private float $refreshAt = 0.0;

    /**
     * Pass a static $token, or $clientId and $clientSecret to use an OAuth access token that is fetched
     * from /oauth/token and cached until $tokenRefreshSkew seconds before it expires.
     */
    public function __construct(
        private ?string $token = null,
        private string $baseUrl = 'https://api.rigepay.co.ke/api/v1',
        private int $maxRetries = 3,
        private int $timeout = 30,
        private $transport = null,
        private $sleep = null,
        private ?string $clientId = null,
        private ?string $clientSecret = null,
        private int $tokenRefreshSkew = 60,
        private $clock = null,
    ) {
        if (($token ?? '') === '' && (($clientId ?? '') === '' || ($clientSecret ?? '') === '')) {
            throw new \InvalidArgumentException('token, or clientId and clientSecret, is required');
        }
        $this->baseUrl = rtrim($baseUrl, '/');
        $this->escrows = new Escrows($this);
        $this->oauth = new OAuth($this);
        $this->collections = new Collections($this);
        $this->checkout = new Checkout($this);
        $this->payouts = new Payouts($this);
        $this->bulkPayouts = new BulkPayouts($this);
        $this->transfers = new Transfers($this);
        $this->balance = new Balance($this);
        $this->transactions = new Transactions($this);
        $this->paymentLinks = new PaymentLinks($this);
        $this->invoices = new Invoices($this);
    }

    private function usesClientCredentials(): bool
    {
        return ($this->token ?? '') === '';
    }

    private function now(): float
    {
        return $this->clock !== null ? (float) ($this->clock)() : microtime(true);
    }

    /** The static token, or a cached client-credentials access token. */
    public function getToken(): string
    {
        if (!$this->usesClientCredentials()) {
            return $this->token;
        }
        if ($this->accessToken !== null && $this->now() < $this->refreshAt) {
            return $this->accessToken;
        }
        $t = $this->oauth->createToken((string) $this->clientId, (string) $this->clientSecret);
        $this->accessToken = (string) $t['access_token'];
        $this->refreshAt = $this->now() + (float) $t['expires_in'] - $this->tokenRefreshSkew;
        return $this->accessToken;
    }

    public function whoami(): mixed
    {
        return $this->data('GET', '/payments/whoami');
    }

    public static function uuid(): string
    {
        $b = random_bytes(16);
        $b[6] = chr((ord($b[6]) & 0x0f) | 0x40);
        $b[8] = chr((ord($b[8]) & 0x3f) | 0x80);
        return vsprintf('%s%s-%s-%s-%s-%s%s%s', str_split(bin2hex($b), 4));
    }

    private function backoff(int $attempt, float $retryAfter): float
    {
        if ($retryAfter > 0) {
            return min($retryAfter, 30.0);
        }
        $base = min(0.5 * (2 ** $attempt), 8.0);
        return $base / 2 + (mt_rand() / mt_getrandmax()) * ($base / 2);
    }

    /** @return array{status:int, headers:array<string,string>, body:string} */
    private function send(string $method, string $url, array $headers, ?string $payload): array
    {
        if ($this->transport !== null) {
            return ($this->transport)($method, $url, $headers, $payload);
        }
        $ch = curl_init($url);
        $responseHeaders = [];
        curl_setopt_array($ch, [
            CURLOPT_CUSTOMREQUEST => $method,
            CURLOPT_RETURNTRANSFER => true,
            CURLOPT_TIMEOUT => $this->timeout,
            CURLOPT_HTTPHEADER => array_map(fn($k, $v) => "$k: $v", array_keys($headers), $headers),
            CURLOPT_HEADERFUNCTION => function ($c, $line) use (&$responseHeaders) {
                $parts = explode(':', $line, 2);
                if (count($parts) === 2) {
                    $responseHeaders[strtolower(trim($parts[0]))] = trim($parts[1]);
                }
                return strlen($line);
            },
        ]);
        if ($payload !== null) {
            curl_setopt($ch, CURLOPT_POSTFIELDS, $payload);
        }
        $body = curl_exec($ch);
        if ($body === false) {
            $error = curl_error($ch);
            curl_close($ch);
            throw new \RuntimeException("network error: $error");
        }
        $status = (int) curl_getinfo($ch, CURLINFO_RESPONSE_CODE);
        curl_close($ch);
        return ['status' => $status, 'headers' => $responseHeaders, 'body' => (string) $body];
    }

    public function request(string $method, string $path, array|object|null $body = null, array $query = [], ?string $idempotencyKey = null, bool $auth = true): array
    {
        $url = $this->baseUrl . $path;
        $query = array_filter($query, fn($v) => $v !== null);
        if ($query) {
            $url .= '?' . http_build_query($query);
        }
        $payload = $body === null ? null : json_encode($body, JSON_THROW_ON_ERROR);
        $key = $idempotencyKey ?? ($method !== 'GET' && $auth ? self::uuid() : null);
        $refreshed = false;
        for ($attempt = 0; ; $attempt++) {
            $headers = ['Accept' => 'application/json'];
            if ($auth) {
                $bearer = $this->getToken();
                $headers['Authorization'] = 'Bearer ' . $bearer;
            }
            if ($payload !== null) {
                $headers['Content-Type'] = 'application/json';
            }
            if ($key !== null) {
                $headers['X-Idempotency-Key'] = $key;
            }
            try {
                $res = $this->send($method, $url, $headers, $payload);
            } catch (\RuntimeException $e) {
                if ($attempt >= $this->maxRetries) {
                    throw $e;
                }
                $this->pause($this->backoff($attempt, 0));
                continue;
            }
            if ($res['status'] === 401 && $auth && $this->usesClientCredentials() && !$refreshed) {
                // The cached access token was revoked or expired early: fetch a new one once, same key.
                $refreshed = true;
                // Clear only if no concurrent request has already replaced the token that was refused.
                if ($this->accessToken === $bearer) {
                    $this->accessToken = null;
                }
                continue;
            }
            if (in_array($res['status'], [429, 502, 503, 504], true) && $attempt < $this->maxRetries) {
                $this->pause($this->backoff($attempt, (float) ($res['headers']['retry-after'] ?? 0)));
                continue;
            }
            $parsed = json_decode($res['body'], true);
            $parsed = is_array($parsed) ? $parsed : ['message' => $res['body']];
            if ($res['status'] >= 400) {
                throw new RigePayException($res['status'], $parsed['code'] ?? $parsed['error'] ?? null, (string) ($parsed['message'] ?? ''), $parsed);
            }
            return $parsed;
        }
    }

    private function pause(float $seconds): void
    {
        $this->sleep !== null ? ($this->sleep)($seconds) : usleep((int) ($seconds * 1_000_000));
    }

    public function data(string $method, string $path, array|object|null $body = null, array $query = [], ?string $idempotencyKey = null): mixed
    {
        return $this->request($method, $path, $body, $query, $idempotencyKey)['data'] ?? null;
    }
}

class Escrows
{
    public function __construct(private Client $c)
    {
    }

    private static function compact(array $a): array
    {
        return array_filter($a, fn($v) => $v !== null);
    }

    public function create(array $input, ?string $idempotencyKey = null): mixed
    {
        return $this->c->data('POST', '/escrows', $input, [], $idempotencyKey);
    }

    public function get(string $id): mixed
    {
        return $this->c->data('GET', "/escrows/$id");
    }

    public function fund(string $id, ?string $idempotencyKey = null): mixed
    {
        return $this->c->data('POST', "/escrows/$id/fund", null, [], $idempotencyKey);
    }

    public function release(string $id, ?int $amountCents = null, ?string $milestoneId = null, ?string $confirmationId = null, ?string $remarks = null, ?string $idempotencyKey = null): mixed
    {
        $body = self::compact(['amount_cents' => $amountCents, 'milestone_id' => $milestoneId, 'confirmation_id' => $confirmationId, 'remarks' => $remarks]);
        return $this->c->data('POST', "/escrows/$id/release", $body ?: new \stdClass(), [], $idempotencyKey);
    }

    public function refund(string $id, ?int $amountCents = null, ?string $milestoneId = null, ?string $confirmationId = null, ?string $remarks = null, ?string $idempotencyKey = null): mixed
    {
        $body = self::compact(['amount_cents' => $amountCents, 'milestone_id' => $milestoneId, 'confirmation_id' => $confirmationId, 'remarks' => $remarks]);
        return $this->c->data('POST', "/escrows/$id/refund", $body ?: new \stdClass(), [], $idempotencyKey);
    }

    public function cancel(string $id, ?string $idempotencyKey = null): mixed
    {
        return $this->c->data('POST', "/escrows/$id/cancel", null, [], $idempotencyKey);
    }

    public function openDispute(string $id, string $reason, ?string $idempotencyKey = null): mixed
    {
        return $this->c->data('POST', "/escrows/$id/disputes", ['reason' => $reason], [], $idempotencyKey);
    }

    public function concede(string $id, string $disputeId, ?string $idempotencyKey = null): mixed
    {
        return $this->c->data('POST', "/escrows/$id/disputes/$disputeId/concede", null, [], $idempotencyKey);
    }
}

/** Shared helpers for the payments resources. Amounts are integer cents. */
abstract class Resource
{
    public function __construct(protected Client $c)
    {
    }

    protected static function compact(array $a): array
    {
        return array_filter($a, fn($v) => $v !== null);
    }

    protected static function cents(string $name, mixed $v): ?int
    {
        if ($v !== null && !is_int($v)) {
            throw new \InvalidArgumentException("$name must be an integer number of cents");
        }
        return $v;
    }

    protected static function destination(?string $destinationType, ?string $phoneNumber, ?string $bankCode, ?string $bankAccountNumber, ?string $shortcode, ?string $accountReference): array
    {
        return ['destination_type' => $destinationType, 'phone_number' => $phoneNumber, 'bank_code' => $bankCode, 'bank_account_number' => $bankAccountNumber, 'shortcode' => $shortcode, 'account_reference' => $accountReference];
    }
}

class OAuth extends Resource
{
    /** POST /oauth/token. Returns the flat token response; a client made with clientId/clientSecret calls this for you. */
    public function createToken(string $clientId, string $clientSecret): array
    {
        return $this->c->request('POST', '/oauth/token', ['grant_type' => 'client_credentials', 'client_id' => $clientId, 'client_secret' => $clientSecret], [], null, false);
    }
}

class Collections extends Resource
{
    public function stkPush(int $amountCents, string $customerPhone, ?string $remarks = null, ?string $branchId = null, ?array $metadata = null, ?string $channel = null, ?string $idempotencyKey = null): mixed
    {
        $body = self::compact(['amount_cents' => $amountCents, 'customer_phone' => $customerPhone, 'remarks' => $remarks, 'branch_id' => $branchId, 'metadata' => $metadata, 'channel' => $channel]);
        return $this->c->data('POST', '/payments/collections/stk-push', $body, [], $idempotencyKey);
    }
}

class Checkout extends Resource
{
    public function createSession(int $amountCents, array $allowedRails, ?string $branchId = null, ?string $description = null, ?array $metadata = null, ?string $idempotencyKey = null): mixed
    {
        $body = self::compact(['amount_cents' => $amountCents, 'allowed_rails' => array_values($allowedRails), 'branch_id' => $branchId, 'description' => $description, 'metadata' => $metadata]);
        return $this->c->data('POST', '/payments/checkout/sessions', $body, [], $idempotencyKey);
    }

    public function getSession(string $id): mixed
    {
        return $this->c->data('GET', '/payments/checkout/sessions/' . rawurlencode($id));
    }
}

class Payouts extends Resource
{
    /** Returns the whole 202 body: read `status` (success or approval_required), `payout_id`, `approval_id`. */
    public function create(int $amountCents, string $recipientName, string $remarks, ?string $destinationType = null, ?string $phoneNumber = null, ?string $bankCode = null, ?string $bankAccountNumber = null, ?string $shortcode = null, ?string $accountReference = null, ?string $branchId = null, ?array $metadata = null, ?string $idempotencyKey = null): array
    {
        $body = self::compact(['amount' => $amountCents, 'recipient_name' => $recipientName, 'remarks' => $remarks]
            + self::destination($destinationType, $phoneNumber, $bankCode, $bankAccountNumber, $shortcode, $accountReference)
            + ['branch_id' => $branchId, 'metadata' => $metadata]);
        return $this->c->request('POST', '/payments/payouts', $body, [], $idempotencyKey);
    }

    public function feeEstimate(int $amountCents, ?string $destinationType = null): mixed
    {
        return $this->c->data('GET', '/payments/payouts/fee-estimate', null, ['amount_cents' => $amountCents, 'destination_type' => $destinationType]);
    }

    public function approvalCeiling(?int $amountCents = null): mixed
    {
        return $this->c->data('GET', '/payments/payouts/approval-ceiling', null, ['amount_cents' => $amountCents]);
    }
}

class BulkPayouts extends Resource
{
    /** $items are arrays with the API's own keys (amount_cents, recipient_name, destination_type, phone_number, ...). */
    public function create(array $items, ?string $fundingSource = null, ?string $vaultId = null, ?string $remarks = null, ?string $idempotencyKey = null): mixed
    {
        $rows = [];
        foreach ($items as $item) {
            self::cents('amount_cents', $item['amount_cents'] ?? null);
            $rows[] = self::compact($item);
        }
        $body = self::compact(['funding_source' => $fundingSource, 'vault_id' => $vaultId, 'remarks' => $remarks, 'items' => $rows]);
        return $this->c->data('POST', '/payments/bulk-payouts', $body, [], $idempotencyKey);
    }

    public function list(): mixed
    {
        return $this->c->data('GET', '/payments/bulk-payouts');
    }

    public function get(string $batchId): mixed
    {
        return $this->c->data('GET', '/payments/bulk-payouts/' . rawurlencode($batchId));
    }
}

class Transfers extends Resource
{
    /** Move money between the organization ("org") and its branches (UUIDs). */
    public function create(string $from, string $to, int $amountCents, ?string $remarks = null, ?string $idempotencyKey = null): mixed
    {
        return $this->c->data('POST', '/payments/transfers', self::compact(['from' => $from, 'to' => $to, 'amount' => $amountCents, 'remarks' => $remarks]), [], $idempotencyKey);
    }

    public function lookup(string $code): mixed
    {
        return $this->c->data('GET', '/payments/transfers/lookup', null, ['code' => $code]);
    }

    /** Returns the whole 202 body: `status` is otp_required, with `transfer_id` and `expires_in`. */
    public function startExternal(string $recipientCollectionCode, int $amountCents, string $remarks, ?string $from = null, ?string $idempotencyKey = null): array
    {
        $body = self::compact(['recipient_collection_code' => $recipientCollectionCode, 'amount' => $amountCents, 'from' => $from, 'remarks' => $remarks]);
        return $this->c->request('POST', '/payments/transfers/external', $body, [], $idempotencyKey);
    }

    public function confirmExternal(string $transferId, string $otp, ?string $idempotencyKey = null): mixed
    {
        return $this->c->data('POST', '/payments/transfers/external/confirm', ['transfer_id' => $transferId, 'otp' => $otp], [], $idempotencyKey);
    }

    public function cancelExternal(string $transferId, ?string $idempotencyKey = null): mixed
    {
        return $this->c->data('POST', '/payments/transfers/external/cancel', ['transfer_id' => $transferId], [], $idempotencyKey);
    }
}

class Balance extends Resource
{
    public function get(?string $branchId = null): mixed
    {
        return $this->c->data('GET', '/payments/balance', null, ['branch_id' => $branchId]);
    }
}

class Transactions extends Resource
{
    /** $query uses the API's names: page, page_size, search, status, direction, type, rail, start_date, end_date, min_amount, max_amount (cents), branch_id. */
    public function list(array $query = []): mixed
    {
        self::cents('min_amount', $query['min_amount'] ?? null);
        self::cents('max_amount', $query['max_amount'] ?? null);
        return $this->c->data('GET', '/payments/transactions', null, $query);
    }

    public function get(string $id): mixed
    {
        return $this->c->data('GET', '/payments/transactions/' . rawurlencode($id));
    }
}

class PaymentLinks extends Resource
{
    /** Returns the whole body: the link in `data`, its shareable address in `url`. */
    public function create(string $currency, ?int $amountCents = null, ?string $paymentMethods = null, ?string $description = null, ?string $name = null, ?bool $isReusable = null, ?bool $allowOpenAmount = null, ?int $expiresInDays = null, ?int $maxUses = null, ?string $redirectUrl = null, ?string $cancelUrl = null, ?string $linkType = null, ?string $idempotencyKey = null): array
    {
        $body = self::compact(['amount_cents' => $amountCents, 'currency' => $currency, 'payment_methods' => $paymentMethods, 'description' => $description, 'name' => $name, 'is_reusable' => $isReusable, 'allow_open_amount' => $allowOpenAmount, 'expires_in_days' => $expiresInDays, 'max_uses' => $maxUses, 'redirect_url' => $redirectUrl, 'cancel_url' => $cancelUrl, 'link_type' => $linkType]);
        return $this->c->request('POST', '/payments/payment-links', $body, [], $idempotencyKey);
    }

    public function list(array $query = []): array
    {
        return $this->c->request('GET', '/payments/payment-links', null, $query);
    }

    public function get(string $id): array
    {
        return $this->c->request('GET', '/payments/payment-links/' . rawurlencode($id));
    }

    public function close(string $id, ?string $idempotencyKey = null): array
    {
        return $this->c->request('POST', '/payments/payment-links/' . rawurlencode($id) . '/close', null, [], $idempotencyKey);
    }
}

class Invoices extends Resource
{
    /** Returns the whole body: the invoice in `data`, its shareable address in `url`. Items are sent as given. */
    public function create(string $customerName, string $customerPhone, string $currency, string $dueDate, array $items, ?string $customerEmail = null, ?string $notes = null, ?bool $pricesIncludeVat = null, ?string $idempotencyKey = null): array
    {
        $body = self::compact(['customer_name' => $customerName, 'customer_phone' => $customerPhone, 'customer_email' => $customerEmail, 'currency' => $currency, 'due_date' => $dueDate, 'notes' => $notes, 'prices_include_vat' => $pricesIncludeVat, 'items' => array_values($items)]);
        return $this->c->request('POST', '/payments/invoices', $body, [], $idempotencyKey);
    }

    public function list(array $query = []): array
    {
        return $this->c->request('GET', '/payments/invoices', null, $query);
    }

    public function get(string $id): mixed
    {
        return $this->c->data('GET', '/payments/invoices/' . rawurlencode($id));
    }

    public function cancel(string $id, ?string $idempotencyKey = null): mixed
    {
        return $this->c->data('POST', '/payments/invoices/' . rawurlencode($id) . '/cancel', null, [], $idempotencyKey);
    }
}

final class Webhook
{
    public static function sign(string $secret, string $url, string $timestamp, string $requestId, string $body, string $method = 'POST'): string
    {
        return hash_hmac('sha256', "$method:$url:$timestamp:$requestId:" . hash('sha256', $body), $secret);
    }

    /** $url must be the endpoint URL exactly as registered with RigePay; $rawBody the exact bytes received. */
    public static function verify(string $rawBody, array $headers, string $secret, string $url, int $toleranceSeconds = 300, ?int $now = null): bool
    {
        $h = array_change_key_case($headers, CASE_LOWER);
        $timestamp = $h['x-rigepay-timestamp'] ?? '';
        $requestId = $h['x-rigepay-request-id'] ?? '';
        $signature = $h['x-rigepay-signature'] ?? '';
        if ($timestamp === '' || $requestId === '' || $signature === '') {
            return false;
        }
        if (!hash_equals(self::sign($secret, $url, $timestamp, $requestId, $rawBody), $signature)) {
            return false;
        }
        return abs(($now ?? time()) - (int) $timestamp) <= $toleranceSeconds;
    }
}
