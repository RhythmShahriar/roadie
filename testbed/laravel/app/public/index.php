<?php
// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Answers 200 with the app's environment when the database answers, 500
// when it doesn't: the boot check of the Laravel scenarios.
$env = require __DIR__ . '/../bootstrap/app.php';
header('Content-Type: text/plain');
try {
    $dsn = sprintf('mysql:host=%s;port=%s;dbname=%s', $env['DB_HOST'] ?? 'laravel-db', $env['DB_PORT'] ?? '3306', $env['DB_DATABASE'] ?? 'laravel');
    $db = new PDO($dsn, $env['DB_USERNAME'] ?? 'laravel', $env['DB_PASSWORD'] ?? (getenv('DB_PASSWORD') ?: ''), [PDO::ATTR_TIMEOUT => 3]);
    $db->query('SELECT 1');
} catch (Throwable $e) {
    http_response_code(500);
    echo "database unavailable\n";
    exit;
}
echo ($env['APP_NAME'] ?? 'Bean & Leaf') . ' (' . ($env['APP_ENV'] ?? 'production') . ")\n";
