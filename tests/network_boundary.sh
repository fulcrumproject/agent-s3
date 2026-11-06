#!/usr/bin/env bash
# Verify the isolated customer network cannot reach RustFS directly, even by IP.
set -euo pipefail
root="$(cd "$(dirname "$0")/.." && pwd)"
cd "$root"
.local/venv/bin/python -c 'import sys; sys.path.insert(0, "tests"); from backend_contract import require_fixtures; require_fixtures()'
backend_ip="$(docker inspect fulcrum-s3-test-rustfs-1 --format '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}')"
image="nginx:1.30.6-alpine@sha256:583b7f2fce1cdb8da5ad5c39ebd3c33b98b87d493190e548bdee1fa8c619c311"
docker run --rm --network fulcrum-s3-test_clients --user 101:101 --read-only --cap-drop ALL \
  --security-opt no-new-privileges:true --entrypoint /bin/sh "$image" -c '
    if wget -q -T 2 -O /dev/null "http://$1:9000/health" 2>/dev/null; then
      echo "FAIL direct backend reachable from customer network"; exit 1
    fi
    if wget -q -T 2 -O /dev/null "http://rustfs:9000/health" 2>/dev/null; then
      echo "FAIL private backend name visible to customer network"; exit 1
    fi
    response="$(wget -S -T 5 -O /dev/null http://gateway:8080/rustfs/admin/v3/list-users 2>&1 || true)"
    case "$response" in
      *"403 Forbidden"*) echo "PASS customer network reaches gateway but not administrative backend";;
      *) echo "FAIL gateway denial not observed"; exit 1;;
    esac
  ' sh "$backend_ip"
