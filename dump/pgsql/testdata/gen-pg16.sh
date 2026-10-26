#!/bin/sh
# Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
# SPDX-License-Identifier: MIT
# Generates the pg_dump 16 fixtures of this folder from prisma-schema.sql:
# plain (COPY), --inserts and --column-inserts. It runs a throwaway
# postgres:16 container with no network and removes it afterwards.
# Run from this folder: sh gen-pg16.sh
set -eu

image=postgres:16
name=roadie-pgsql-fixture-$$
docker run -d --rm --name "$name" --network none \
	-e POSTGRES_HOST_AUTH_METHOD=trust -e POSTGRES_DB=app "$image" >/dev/null
trap 'docker rm -f "$name" >/dev/null 2>&1 || true' EXIT

i=0
until docker exec "$name" pg_isready -U postgres -d app -q; do
	i=$((i + 1))
	if [ "$i" -gt 60 ]; then
		echo "postgres did not start" >&2
		exit 1
	fi
	sleep 1
done
# pg_isready answers during the init restart; wait for the real server.
sleep 2
until docker exec "$name" psql -U postgres -d app -qtc 'SELECT 1' >/dev/null 2>&1; do sleep 1; done

docker exec -i "$name" psql -U postgres -d app -q -v ON_ERROR_STOP=1 <prisma-schema.sql
docker exec "$name" pg_dump -U postgres -Fp app >pg16-prisma.sql
docker exec "$name" pg_dump -U postgres -Fp --inserts app >pg16-inserts.sql
docker exec "$name" pg_dump -U postgres -Fp --column-inserts --rows-per-insert=2 app >pg16-column-inserts.sql
