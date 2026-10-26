<?php
// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Writes the .ser fixtures of codec/phpser from real PHP serialize()
// output, plus the hand-made inputs PHP never writes but accepts or
// refuses. Run from this folder: php -n gen.php [outdir]
// The output is committed; TestGenerated checks it still matches.

enum Suit: string
{
    case Hearts = 'H';
}

class Customer
{
    public $name = 'Jane Roe';
    protected $email = 'jane@example.invalid';
    private $phone = '+1 555 0100';
}

$dir = $argv[1] ?? __DIR__;

$customer = new Customer();
$shared = new stdClass();
$shared->email = 'shared@example.invalid';
$list = ['first', 'second'];

$fixtures = [
    'multibyte' => serialize('Zoë Ångström'),
    'nul-props' => serialize($customer),
    'nested' => serialize(['a' => ['b' => ['c' => 'deep@example.invalid', 'n' => 7]], 'list' => [1.5, true, null, 'x']]),
    'enum' => serialize(['suit' => Suit::Hearts, 'name' => 'card']),
    'refs' => serialize(['one' => $shared, 'two' => $shared, 'list' => &$list, 'again' => &$list]),
    'inner' => serialize(['data' => serialize(['email' => 'inner@example.invalid'])]),
    'floats' => serialize([INF, -INF, NAN, 1.0E+25, -0.0, 0.1]),
];

// Hand-made inputs: PHP accepts these but serialize() never writes them.
$fixtures['leading-zero'] = 'a:01:{i:0;s:3:"abc";}';
$fixtures['plus-int'] = 'i:+5;';
// PHP refuses a boolean key.
$fixtures['invalid-bool-key'] = 'a:1:{b:0;s:1:"x";}';

foreach ($fixtures as $name => $value) {
    file_put_contents($dir . '/' . $name . '.ser', $value);
}
