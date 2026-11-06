#!/usr/bin/env bash
# Build a clean, pinned core without changing its repository, then start isolated fixtures.
set -euo pipefail
root="$(cd "$(dirname "$0")/.." && pwd)"
cd "$root"
core_dir="${CORE_DIR:-$root/../core}"
read -r expected < tests/core-revision
if [[ "$(git -C "$core_dir" rev-parse HEAD)" != "$expected" ]] || [[ -n "$(git -C "$core_dir" status --porcelain)" ]]; then
  printf 'Core must be a clean checkout of %s. Set CORE_DIR to a separate checkout; this script will not modify it.\n' "$expected" >&2
  exit 1
fi
python3 scripts/test-env.py
python3 scripts/core-env.py
mkdir -p .local/core-build
CGO_ENABLED=0 go -C "$core_dir" build -mod=readonly -o "$root/.local/core-build/core" ./cmd/fulcrum
go build -o .local/agent ./cmd/agent
docker compose --project-name fulcrum-s3-test --env-file .local/rustfs.env --env-file .local/core.env \
  -f compose.yaml -f compose.e2e.yaml up -d --build
python3 - <<'PY'
import os
import time
import urllib.request
import urllib.error
port = str(int(os.environ.get("RUSTFS_TEST_PORT", "19000")))
targets = [("http://127.0.0.1:" + port + "/health", 200), ("http://127.0.0.1:18000/api/v1/services", 401)]
for target, expected in targets:
    for _ in range(60):
        try:
            with urllib.request.urlopen(target, timeout=2) as response:
                status = response.status
        except urllib.error.HTTPError as error:
            status = error.code
            error.close()
        except Exception:
            status = None
        if status == expected:
            break
        time.sleep(1)
    else:
        raise SystemExit("Fixture did not become ready")
print("Isolated backend and unchanged core are ready.")
PY
