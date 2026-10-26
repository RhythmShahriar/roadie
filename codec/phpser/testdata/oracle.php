<?php
// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// The differential oracle for codec/phpser. It first writes a line with
// PHP_VERSION_ID, then reads frames from stdin, each a 4-byte big-endian
// length and that many bytes, and answers one byte per frame: '1' when
// PHP's unserialize accepts the bytes and '0' when it doesn't. Classes are never loaded (allowed_classes false), so
// no object code runs. Run as: php -n oracle.php

error_reporting(0);
ini_set('display_errors', '0');

$in = fopen('php://stdin', 'rb');
$out = fopen('php://stdout', 'wb');

// The first line says which PHP answers, since the grammar changed in 8.3.
fwrite($out, PHP_VERSION_ID . "\n");
fflush($out);

function read_exactly($in, int $n): ?string
{
    $buf = '';
    while (strlen($buf) < $n) {
        $chunk = fread($in, $n - strlen($buf));
        if ($chunk === false || $chunk === '') {
            return null;
        }
        $buf .= $chunk;
    }
    return $buf;
}

while (true) {
    $head = read_exactly($in, 4);
    if ($head === null) {
        break;
    }
    $n = unpack('N', $head)[1];
    $s = $n > 0 ? read_exactly($in, $n) : '';
    if ($s === null) {
        break;
    }
    // b:0; is the one valid value that is false itself; PHP ignores any
    // bytes after a value, so b:0; followed by anything is valid too.
    $ok = @unserialize($s, ['allowed_classes' => false]) !== false || strncmp($s, 'b:0;', 4) === 0;
    fwrite($out, $ok ? '1' : '0');
    fflush($out);
}
