#!/bin/sh
# Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
# SPDX-License-Identifier: MIT

# The gate is the only container with a second network, for the ports
# published on 127.0.0.1. It may answer connections that come in, never
# open one out: everything outbound except the sealed network, loopback
# and replies is dropped before nginx starts.
set -eu
subnet=${TESTBED_SUBNET:-172.31.120.0/24}
iptables -A OUTPUT -o lo -j ACCEPT
iptables -A OUTPUT -d "$subnet" -j ACCEPT
iptables -A OUTPUT -m conntrack --ctstate ESTABLISHED,RELATED -j ACCEPT
iptables -A OUTPUT -j DROP
exec nginx -c /gate/nginx.conf -g 'daemon off;'
