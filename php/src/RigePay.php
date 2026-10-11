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

    public function __construct(
        private string $token,
        private string $baseUrl = 'https://api.rigepay.co.ke/api/v1',
        private int $maxRetries = 3,
        private int $timeout = 30,
        private $transport = null,
        private $sleep = null,
    ) {
        if ($token === '') {
            throw new \InvalidArgumentException('token is required');
        }
        $this->baseUrl = rtrim($baseUrl, '/');
        $this->escrows = new Escrows($this);
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

    public function request(string $method, string $path, array|object|null $body = null, array $query = [], ?string $idempotencyKey = null): array
    {
        $url = $this->baseUrl . $path;
        $query = array_filter($query, fn($v) => $v !== null);
        if ($query) {
            $url .= '?' . http_build_query($query);
        }
        $payload = $body === null ? null : json_encode($body, JSON_THROW_ON_ERROR);
        $key = $idempotencyKey ?? ($method !== 'GET' ? self::uuid() : null);
        for ($attempt = 0; ; $attempt++) {
            $headers = ['Authorization' => "Bearer {$this->token}", 'Accept' => 'application/json'];
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
            if (in_array($res['status'], [429, 502, 503, 504], true) && $attempt < $this->maxRetries) {
                $this->pause($this->backoff($attempt, (float) ($res['headers']['retry-after'] ?? 0)));
                continue;
            }
            $parsed = json_decode($res['body'], true);
            $parsed = is_array($parsed) ? $parsed : ['message' => $res['body']];
            if ($res['status'] >= 400) {
                throw new RigePayException($res['status'], $parsed['code'] ?? null, (string) ($parsed['message'] ?? ''), $parsed);
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
