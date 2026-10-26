#!/usr/bin/env bash
# Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
# SPDX-License-Identifier: MIT

# Checks a downloaded Roadie release: the cosign bundle over checksums.txt,
# then the archive's hash. The check itself lives in scripts/verify-release.sh,
# which the Action runs next to its other scripts; this copy only calls it,
# so the two can't drift apart.
#
#   verify-release.sh <dir> <archive> <version>
set -euo pipefail

exec "$(dirname "$0")/../../scripts/verify-release.sh" "$@"
