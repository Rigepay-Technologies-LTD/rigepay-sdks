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

exit($failures === 0 ? 0 : 1);
