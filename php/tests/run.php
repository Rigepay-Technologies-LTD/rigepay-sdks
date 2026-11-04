<?php

declare(strict_types=1);

require __DIR__ . '/../src/RigePay.php';

use RigePay\Client;
use RigePay\RigePayException;
use RigePay\Webhook;

$v = json_decode(file_get_contents(__DIR__ . '/../../vectors.json'), true)['signature'];
$failures = 0;
function check(bool $ok, string $name): void
{
    global $failures;
    echo ($ok ? 'ok   ' : 'FAIL ') . $name . "\n";
    if (!$ok) {
        $failures++;
    }
}

check(Webhook::sign($v['secret'], $v['url'], $v['timestamp'], $v['request_id'], $v['body']) === $v['signature'], 'signature matches the server signer');

$headers = ['X-RigePay-Timestamp' => $v['timestamp'], 'X-RigePay-Request-ID' => $v['request_id'], 'X-RigePay-Signature' => $v['signature']];
$now = (int) $v['timestamp'] + 10;
check(Webhook::verify($v['body'], $headers, $v['secret'], $v['url'], 300, $now), 'verify accepts the pinned delivery');
check(!Webhook::verify($v['body'] . ' ', $headers, $v['secret'], $v['url'], 300, $now), 'verify rejects a changed body');
check(!Webhook::verify($v['body'], $headers, $v['secret'], str_replace('?src=rigepay', '', $v['url']), 300, $now), 'the URL is part of the signed message');
check(!Webhook::verify($v['body'], $headers, $v['secret'], $v['url'], 300, (int) $v['timestamp'] + 301), 'verify rejects an old timestamp');

$calls = [];
$responses = [[503, []], [200, ['data' => ['id' => 'e1']]]];
$transport = function ($method, $url, $headers, $payload) use (&$calls, &$responses) {
    $calls[] = ['headers' => $headers, 'payload' => $payload];
    [$status, $body] = array_shift($responses);
    return ['status' => $status, 'headers' => [], 'body' => json_encode($body)];
};
$client = new Client('t', transport: $transport, sleep: fn($s) => null);
$escrow = $client->escrows->create(['reference' => 'r']);
check($escrow['id'] === 'e1' && count($calls) === 2, 'a 503 is retried');
check($calls[0]['headers']['X-Idempotency-Key'] !== '' && $calls[0]['headers']['X-Idempotency-Key'] === $calls[1]['headers']['X-Idempotency-Key'], 'a retry reuses the idempotency key');

$calls = [];
$responses = [[409, ['code' => 'dispute_active', 'message' => 'frozen']]];
try {
    $client->escrows->release('e1', milestoneId: 'm1');
    check(false, 'a 409 raises');
} catch (RigePayException $e) {
    check($e->errorCode === 'dispute_active' && count($calls) === 1, 'a 409 raises with its code and is not retried');
    check(json_decode($calls[0]['payload'], true) === ['milestone_id' => 'm1'], 'release sends only the milestone');
}

// --- payments resources ---

$base = 'https://api.rigepay.co.ke/api/v1';
$record = function (array &$calls, array &$responses): callable {
    return function ($method, $url, $headers, $payload) use (&$calls, &$responses) {
        $calls[] = ['method' => $method, 'url' => $url, 'headers' => $headers, 'payload' => $payload];
        [$status, $body] = $responses ? array_shift($responses) : [200, ['status' => 'success', 'data' => []]];
        return ['status' => $status, 'headers' => [], 'body' => json_encode($body)];
    };
};

// [name, call, method, path, query, body]
$cases = [
    ['whoami', fn(Client $c) => $c->whoami(), 'GET', '/payments/whoami', [], null],
    ['collections.stkPush', fn(Client $c) => $c->collections->stkPush(1000, '+254700000000', remarks: 'Order 1', channel: 'mpesa'), 'POST', '/payments/collections/stk-push', [], ['amount_cents' => 1000, 'customer_phone' => '+254700000000', 'remarks' => 'Order 1', 'channel' => 'mpesa']],
    ['checkout.createSession', fn(Client $c) => $c->checkout->createSession(2500, ['mpesa_stk', 'card'], description: 'Cart'), 'POST', '/payments/checkout/sessions', [], ['amount_cents' => 2500, 'allowed_rails' => ['mpesa_stk', 'card'], 'description' => 'Cart']],
    ['checkout.getSession', fn(Client $c) => $c->checkout->getSession('s1'), 'GET', '/payments/checkout/sessions/s1', [], null],
    ['payouts.create', fn(Client $c) => $c->payouts->create(15000, 'Jane', 'May', destinationType: 'PAYBILL', shortcode: '123456', accountReference: 'ACC1'), 'POST', '/payments/payouts', [], ['amount' => 15000, 'recipient_name' => 'Jane', 'remarks' => 'May', 'destination_type' => 'PAYBILL', 'shortcode' => '123456', 'account_reference' => 'ACC1']],
    ['payouts.feeEstimate', fn(Client $c) => $c->payouts->feeEstimate(15000, 'BANK_ACCOUNT'), 'GET', '/payments/payouts/fee-estimate', ['amount_cents' => '15000', 'destination_type' => 'BANK_ACCOUNT'], null],
    ['payouts.approvalCeiling', fn(Client $c) => $c->payouts->approvalCeiling(9000000), 'GET', '/payments/payouts/approval-ceiling', ['amount_cents' => '9000000'], null],
    ['bulkPayouts.create', fn(Client $c) => $c->bulkPayouts->create([['amount_cents' => 100, 'recipient_name' => 'A', 'phone_number' => '0700000001'], ['amount_cents' => 200, 'recipient_name' => 'B']], fundingSource: 'MAIN'), 'POST', '/payments/bulk-payouts', [], ['funding_source' => 'MAIN', 'items' => [['amount_cents' => 100, 'recipient_name' => 'A', 'phone_number' => '0700000001'], ['amount_cents' => 200, 'recipient_name' => 'B']]]],
    ['bulkPayouts.list', fn(Client $c) => $c->bulkPayouts->list(), 'GET', '/payments/bulk-payouts', [], null],
    ['bulkPayouts.get', fn(Client $c) => $c->bulkPayouts->get('b1'), 'GET', '/payments/bulk-payouts/b1', [], null],
    ['transfers.create', fn(Client $c) => $c->transfers->create('org', 'br1', 1000, 'float'), 'POST', '/payments/transfers', [], ['from' => 'org', 'to' => 'br1', 'amount' => 1000, 'remarks' => 'float']],
    ['transfers.lookup', fn(Client $c) => $c->transfers->lookup('ACME01'), 'GET', '/payments/transfers/lookup', ['code' => 'ACME01'], null],
    ['transfers.startExternal', fn(Client $c) => $c->transfers->startExternal('ACME01', 5000, 'stock'), 'POST', '/payments/transfers/external', [], ['recipient_collection_code' => 'ACME01', 'amount' => 5000, 'remarks' => 'stock']],
    ['transfers.confirmExternal', fn(Client $c) => $c->transfers->confirmExternal('t1', '123456'), 'POST', '/payments/transfers/external/confirm', [], ['transfer_id' => 't1', 'otp' => '123456']],
    ['transfers.cancelExternal', fn(Client $c) => $c->transfers->cancelExternal('t1'), 'POST', '/payments/transfers/external/cancel', [], ['transfer_id' => 't1']],
    ['balance.get', fn(Client $c) => $c->balance->get('br1'), 'GET', '/payments/balance', ['branch_id' => 'br1'], null],
    ['transactions.list', fn(Client $c) => $c->transactions->list(['page' => 2, 'direction' => 'CREDIT', 'min_amount' => 100]), 'GET', '/payments/transactions', ['page' => '2', 'direction' => 'CREDIT', 'min_amount' => '100'], null],
    ['transactions.get', fn(Client $c) => $c->transactions->get('x1'), 'GET', '/payments/transactions/x1', [], null],
    ['paymentLinks.create', fn(Client $c) => $c->paymentLinks->create('KES', amountCents: 1000, name: 'Deposit', isReusable: true, expiresInDays: 7), 'POST', '/payments/payment-links', [], ['amount_cents' => 1000, 'currency' => 'KES', 'name' => 'Deposit', 'is_reusable' => true, 'expires_in_days' => 7]],
    ['paymentLinks.list', fn(Client $c) => $c->paymentLinks->list(['page' => 1]), 'GET', '/payments/payment-links', ['page' => '1'], null],
    ['paymentLinks.get', fn(Client $c) => $c->paymentLinks->get('l1'), 'GET', '/payments/payment-links/l1', [], null],
    ['paymentLinks.close', fn(Client $c) => $c->paymentLinks->close('l1'), 'POST', '/payments/payment-links/l1/close', [], null],
    ['invoices.create', fn(Client $c) => $c->invoices->create('Acme', '0700000000', 'KES', '2026-12-01T00:00:00+03:00', [['description' => 'Widget', 'quantity' => 2]]), 'POST', '/payments/invoices', [], ['customer_name' => 'Acme', 'customer_phone' => '0700000000', 'currency' => 'KES', 'due_date' => '2026-12-01T00:00:00+03:00', 'items' => [['description' => 'Widget', 'quantity' => 2]]]],
    ['invoices.list', fn(Client $c) => $c->invoices->list(['status' => 'PAID']), 'GET', '/payments/invoices', ['status' => 'PAID'], null],
    ['invoices.get', fn(Client $c) => $c->invoices->get('i1'), 'GET', '/payments/invoices/i1', [], null],
    ['invoices.cancel', fn(Client $c) => $c->invoices->cancel('i1'), 'POST', '/payments/invoices/i1/cancel', [], null],
];
foreach ($cases as [$name, $call, $method, $path, $query, $body]) {
    $calls = [];
    $responses = [];
    $call(new Client('k', transport: $record($calls, $responses), sleep: fn($s) => null));
    $c0 = $calls[0] ?? null;
    $parts = $c0 ? parse_url($c0['url']) : [];
    parse_str($parts['query'] ?? '', $gotQuery);
    $gotUrl = ($parts['scheme'] ?? '') . '://' . ($parts['host'] ?? '') . ($parts['path'] ?? '');
    $key = $c0['headers']['X-Idempotency-Key'] ?? null;
    $ok = count($calls) === 1
        && $c0['method'] === $method
        && $gotUrl === $base . $path
        && $gotQuery == $query
        && ($c0['payload'] === null ? null : json_decode($c0['payload'], true)) === $body
        && $c0['headers']['Authorization'] === 'Bearer k'
        && ($method === 'POST' ? is_string($key) && $key !== '' : $key === null);
    check($ok, "$name sends $method $path");
}

$calls = [];
$responses = [
    [202, ['status' => 'approval_required', 'payout_id' => 'p1', 'approval_id' => 'a1', 'amount' => 15000, 'fee_cents' => 50]],
    [202, ['status' => 'otp_required', 'transfer_id' => 't1', 'expires_in' => 600]],
    [201, ['status' => 'success', 'data' => ['id' => 'l1'], 'url' => 'https://pay.example/abc']],
];
$client = new Client('k', transport: $record($calls, $responses), sleep: fn($s) => null);
$payout = $client->payouts->create(15000, 'J', 'r', phoneNumber: '0700000000');
$started = $client->transfers->startExternal('C', 1000, 'r');
$link = $client->paymentLinks->create('KES', amountCents: 1000);
check($payout['status'] === 'approval_required' && $payout['approval_id'] === 'a1' && $started['transfer_id'] === 't1' && $link['url'] === 'https://pay.example/abc' && $link['data']['id'] === 'l1', 'flat bodies come back whole');

$calls = [];
$responses = [[503, []], [502, []], [202, ['status' => 'success', 'payout_id' => 'p1']]];
$client = new Client('k', maxRetries: 2, transport: $record($calls, $responses), sleep: fn($s) => null);
$client->payouts->create(100, 'J', 'r', phoneNumber: '0700000000');
$keys = array_map(fn($c) => $c['headers']['X-Idempotency-Key'], $calls);
check(count($keys) === 3 && $keys[0] !== '' && count(array_unique($keys)) === 1, 'a payout retried after 503/502 reuses its idempotency key');

$threw = 0;
try {
    $client->bulkPayouts->create([['amount_cents' => 1.25, 'recipient_name' => 'A'], ['amount_cents' => 100, 'recipient_name' => 'B']]);
} catch (\InvalidArgumentException $e) {
    $threw++;
}
try {
    $client->collections->stkPush(10.5, '0700000000');
} catch (\TypeError $e) {
    $threw++;
}
check($threw === 2, 'amounts must be integer cents');

$tokenServer = function (array &$calls, array $apiStatuses = []): callable {
    $issued = 0;
    return function ($method, $url, $headers, $payload) use (&$calls, &$issued, &$apiStatuses) {
        $calls[] = ['method' => $method, 'url' => $url, 'headers' => $headers, 'payload' => $payload];
        if (str_ends_with($url, '/oauth/token')) {
            $issued++;
            return ['status' => 200, 'headers' => [], 'body' => json_encode(['access_token' => "at-$issued", 'token_type' => 'Bearer', 'expires_in' => 3600, 'scope' => 'x'])];
        }
        $status = $apiStatuses ? array_shift($apiStatuses) : 200;
        return ['status' => $status, 'headers' => [], 'body' => json_encode($status === 200 ? ['status' => 'success', 'data' => []] : ['status' => 'error', 'message' => 'unauthorized'])];
    };
};
$isToken = fn($c) => str_ends_with($c['url'], '/oauth/token');

$calls = [];
$now = 1000000.0;
$client = new Client(clientId: 'cid', clientSecret: 'csecret', transport: $tokenServer($calls), sleep: fn($s) => null, clock: function () use (&$now) {
    return $now;
});
$client->whoami();
$client->balance->get();
$tokenCalls = array_values(array_filter($calls, $isToken));
$apiCalls = array_values(array_filter($calls, fn($c) => !$isToken($c)));
check(count($tokenCalls) === 1 && $tokenCalls[0]['method'] === 'POST' && !isset($tokenCalls[0]['headers']['Authorization']) && !isset($tokenCalls[0]['headers']['X-Idempotency-Key'])
    && json_decode($tokenCalls[0]['payload'], true) === ['grant_type' => 'client_credentials', 'client_id' => 'cid', 'client_secret' => 'csecret'], 'the token request is unauthenticated and sent once');
check(count($apiCalls) === 2 && $apiCalls[0]['headers']['Authorization'] === 'Bearer at-1' && $apiCalls[1]['headers']['Authorization'] === 'Bearer at-1', 'the cached token is reused');
$now += 3600 - 61;
$client->whoami();
$before = count(array_filter($calls, $isToken));
$now += 2;
$client->whoami();
$after = count(array_filter($calls, $isToken));
check($before === 1 && $after === 2 && end($calls)['headers']['Authorization'] === 'Bearer at-2', 'the token is refreshed within 60s of expiry');

$calls = [];
$client = new Client(clientId: 'cid', clientSecret: 's', transport: $tokenServer($calls, [401, 200]), sleep: fn($s) => null);
$client->collections->stkPush(1000, '0700000000');
$api = array_values(array_filter($calls, fn($c) => !$isToken($c)));
check(count($api) === 2 && $api[0]['headers']['Authorization'] === 'Bearer at-1' && $api[1]['headers']['Authorization'] === 'Bearer at-2'
    && $api[0]['headers']['X-Idempotency-Key'] === $api[1]['headers']['X-Idempotency-Key'], 'a 401 refreshes the token once with the same idempotency key');

$calls = [];
$responses = [];
$client = new Client('k', transport: $record($calls, $responses), sleep: fn($s) => null);
$client->transactions->get('a/b?c');
$client->paymentLinks->close('../x');
check($calls[0]['url'] === $base . '/payments/transactions/a%2Fb%3Fc' && $calls[1]['url'] === $base . '/payments/payment-links/..%2Fx/close', 'path ids are URL-encoded');

// A late 401 for at-1 arrives after another request already cached at-2: at-2 must be kept and used.
$calls = [];
$tokenFetches = 0;
$client = null;
$client = new Client(clientId: 'cid', clientSecret: 's', sleep: fn($s) => null, transport: function ($method, $url, $headers, $payload) use (&$calls, &$tokenFetches, &$client) {
    $calls[] = ['url' => $url, 'headers' => $headers];
    if (str_ends_with($url, '/oauth/token')) {
        $tokenFetches++;
        return ['status' => 200, 'headers' => [], 'body' => json_encode(['access_token' => "at-$tokenFetches", 'token_type' => 'Bearer', 'expires_in' => 3600, 'scope' => 'x'])];
    }
    if ($headers['Authorization'] === 'Bearer at-1') {
        \Closure::bind(function () {
            $this->accessToken = 'at-2';
        }, $client, Client::class)();
        return ['status' => 401, 'headers' => [], 'body' => json_encode(['status' => 'error', 'message' => 'revoked'])];
    }
    return ['status' => 200, 'headers' => [], 'body' => json_encode(['status' => 'success', 'data' => []])];
});
$client->whoami();
check($tokenFetches === 1 && end($calls)['headers']['Authorization'] === 'Bearer at-2', 'a late 401 does not discard a token another request just fetched');

$calls = [];
$responses = [[401, ['status' => 'error', 'error' => 'invalid_client', 'message' => 'bad']]];
$client = new Client('k', transport: $record($calls, $responses), sleep: fn($s) => null);
try {
    $client->oauth->createToken('a', 'b');
    check(false, 'an OAuth error raises');
} catch (RigePayException $e) {
    check($e->status === 401 && $e->errorCode === 'invalid_client', 'OAuth errors surface `error` as the code');
}

exit($failures === 0 ? 0 : 1);
