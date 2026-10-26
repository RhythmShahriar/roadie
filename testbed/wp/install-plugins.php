<?php
// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Downloads the pinned plugin zips, checks each one's sha256 and unpacks
// it into the plugins folder under the given name. Usage:
//   php install-plugins.php <plugins dir> "<url> <sha256> <name>\n..."
[$_, $dir, $list] = $argv;
foreach (preg_split('/\R/', trim($list)) as $line) {
    [$url, $sum, $name] = preg_split('/\s+/', trim($line));
    $zip = tempnam(sys_get_temp_dir(), 'plugin');
    if (!copy($url, $zip)) {
        fwrite(STDERR, "download failed: $url\n");
        exit(1);
    }
    $got = hash_file('sha256', $zip);
    if (!hash_equals($sum, $got)) {
        fwrite(STDERR, "sha256 mismatch for $url: got $got, want $sum\n");
        exit(1);
    }
    $z = new ZipArchive();
    $tmp = sys_get_temp_dir() . '/unpack-' . $name;
    if ($z->open($zip) !== true || !$z->extractTo($tmp)) {
        fwrite(STDERR, "unzip failed: $url\n");
        exit(1);
    }
    $z->close();
    // A zip holds one top folder (GitHub archives name it <repo>-<tag>).
    $top = glob($tmp . '/*', GLOB_ONLYDIR);
    if (count($top) !== 1 || !rename($top[0], "$dir/$name")) {
        fwrite(STDERR, "unexpected layout in $url\n");
        exit(1);
    }
    rmdir($tmp);
    unlink($zip);
    echo "installed $name\n";
}
