<?php
// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// The testbed oracle's integrity check (gate 2). Reads values framed as an
// 8-digit byte length followed by the bytes from stdin and prints one line
// per value: 1 when unserialize() accepts it, 0 when it doesn't. Classes
// are never instantiated: allowed_classes is false, so an object comes back
// as __PHP_Incomplete_Class and no code in the value can run.
$in = stream_get_contents(STDIN);
$out = '';
for ($i = 0, $n = strlen($in); $i + 8 <= $n;) {
    $len = (int) substr($in, $i, 8);
    $value = substr($in, $i + 8, $len);
    $i += 8 + $len;
    // false is also what unserialize returns for a broken value, so the
    // serialized false itself is accepted by comparing the text.
    $ok = $value === 'b:0;' || @unserialize($value, ['allowed_classes' => false]) !== false;
    $out .= $ok ? "1\n" : "0\n";
}
echo $out;
