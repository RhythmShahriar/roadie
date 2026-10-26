<?php
// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Checks Roadie's password hashes the way WordPress and Laravel do.
//
// Run as php -n testdata/check_password.php. Each line on stdin is
// "<mode> <base64 password> <base64 hash>"; each answer is one line of
// "1" (the password matches) or "0". Modes:
//   phpass   phpass CheckPassword, as WordPress's class-phpass.php has it
//   bcrypt   password_verify, and password_get_info must name bcrypt (Laravel's check)
//   make     prints crypt_private(password, hash as setting) instead of 1 or 0

$itoa64 = './0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz';

function encode64($input, $count)
{
    global $itoa64;
    $output = '';
    $i = 0;
    do {
        $value = ord($input[$i++]);
        $output .= $itoa64[$value & 0x3f];
        if ($i < $count) {
            $value |= ord($input[$i]) << 8;
        }
        $output .= $itoa64[($value >> 6) & 0x3f];
        if ($i++ >= $count) {
            break;
        }
        if ($i < $count) {
            $value |= ord($input[$i]) << 16;
        }
        $output .= $itoa64[($value >> 12) & 0x3f];
        if ($i++ >= $count) {
            break;
        }
        $output .= $itoa64[($value >> 18) & 0x3f];
    } while ($i < $count);
    return $output;
}

function crypt_private($password, $setting)
{
    global $itoa64;
    $output = '*0';
    if (substr($setting, 0, 2) === $output) {
        $output = '*1';
    }
    $id = substr($setting, 0, 3);
    if ($id !== '$P$' && $id !== '$H$') {
        return $output;
    }
    $count_log2 = strpos($itoa64, $setting[3]);
    if ($count_log2 < 7 || $count_log2 > 30) {
        return $output;
    }
    $count = 1 << $count_log2;
    $salt = substr($setting, 4, 8);
    if (strlen($salt) !== 8) {
        return $output;
    }
    $hash = md5($salt . $password, true);
    do {
        $hash = md5($hash . $password, true);
    } while (--$count);
    $output = substr($setting, 0, 12);
    $output .= encode64($hash, 16);
    return $output;
}

function check_password($password, $stored_hash)
{
    if (strlen($password) > 4096) {
        return false;
    }
    $hash = crypt_private($password, $stored_hash);
    if ($hash[0] === '*') {
        $hash = crypt($password, $stored_hash);
    }
    return hash_equals($stored_hash, $hash);
}

while (($line = fgets(STDIN)) !== false) {
    $parts = explode(' ', trim($line));
    if (count($parts) !== 3) {
        fwrite(STDERR, "bad line\n");
        exit(2);
    }
    $password = base64_decode($parts[1], true);
    $hash = base64_decode($parts[2], true);
    switch ($parts[0]) {
        case 'phpass':
            $ok = check_password($password, $hash);
            break;
        case 'bcrypt':
            $ok = password_get_info($hash)['algoName'] === 'bcrypt' && password_verify($password, $hash);
            break;
        case 'make':
            echo crypt_private($password, $hash), "\n";
            continue 2;
        default:
            fwrite(STDERR, "bad mode\n");
            exit(2);
    }
    echo $ok ? "1\n" : "0\n";
}
