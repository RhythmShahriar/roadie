# Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
# SPDX-License-Identifier: MIT

# The image the testbed's own programs run in: the sinkhole, the stubs,
# Echo and the gate. The programs are static Linux binaries built on the
# host into runtime/bin (make binaries) and mounted in; the image only adds
# iptables (the sinkhole's redirect of every port, the gate's egress
# block), nginx with its stream module (the gate) and the CA store.
FROM alpine:3.22@sha256:2e0b81d60597f449baa56dc25d355367869d2dca4cd57e866f1e2b52af3eb55f
RUN apk add --no-cache ca-certificates iptables nginx nginx-mod-stream
