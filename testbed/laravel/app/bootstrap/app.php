<?php
// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// The fixture's stand-in for Laravel's bootstrap: it reads .env the way
// Laravel does (KEY=value lines, quotes optional) so the page can report
// the environment and the database it uses.
function testbed_env(string $path): array
{
    $env = [];
    foreach (is_readable($path) ? file($path, FILE_IGNORE_NEW_LINES) : [] as $line) {
        if ($line === '' || $line[0] === '#' || !str_contains($line, '=')) {
            continue;
        }
        [$k, $v] = explode('=', $line, 2);
        $env[trim($k)] = trim(trim($v), '"\'');
    }
    return $env;
}

return testbed_env(dirname(__DIR__) . '/.env');
