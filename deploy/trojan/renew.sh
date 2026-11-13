#!/bin/sh
set -eu
cd "$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)"

# Compare certificate contents so daily checks do not disconnect players.
before=$(find certs/live -name fullchain.pem -exec sha256sum {} +)
docker compose --profile certificate run --rm --service-ports certbot renew --non-interactive
after=$(find certs/live -name fullchain.pem -exec sha256sum {} +)
if [ "$before" != "$after" ]; then
    docker compose restart tunnel
fi
