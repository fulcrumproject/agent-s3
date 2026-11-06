# Verification

Implementation baseline: [301e9e7](https://github.com/fulcrumproject/agent-s3/commit/301e9e7ac9bf9e6e84069268d2a085d6406e3cc7).
The [CI run](https://github.com/fulcrumproject/agent-s3/actions/runs/42237157601)
passed both unit and acceptance jobs.

Local environment: Linux amd64, Go 1.27.0, Python 3.14.10, Docker 29.9.3 and Compose v5.7.0.
All data was synthetic.

## Reference versions

- Unchanged core checkout: `3212fc7d41422be92b53997770d3311fcab8bf36`.
- RustFS 1.0.4 OCI index:
  `sha256:ed9210d6411d77ffceb70d94b964a3cb7d225983368bc25bebe01e995293dcc2`.
- nginx 1.30.6-alpine gateway:
  `sha256:583b7f2fce1cdb8da5ad5c39ebd3c33b98b87d493190e548bdee1fa8c619c311`.
- Go 1.27.0-alpine builder:
  `sha256:4c9fe60190a2a3350ddc51de80d0224b8a6698d12bdfc999fee45ea9d6c46dbc`.
- Go dependencies: `go.mod` and `go.sum`.
- boto3, AWS CLI and Python dependencies: `tests/requirements.txt`.

## Results

| Check | Result |
| --- | --- |
| `go test ./...` | Passed |
| `go test -race ./...` | Passed |
| `go vet ./...` | Passed |
| Python syntax checks | Passed |
| `pip check` | Passed |
| Agent image build and `--help` with a read-only filesystem | Passed as user 10001:10001 |
| `tests/backend_contract.py` through the gateway | Passed |
| `tests/test_iam_boundaries.py` through the gateway | 3 tests passed |
| `tests/network_boundary.sh` | Customer network reaches the gateway, not the native backend, including by IP |
| `tests/e2e.py` with the unchanged core and race-instrumented agent | Passed; no race reported |

## Coverage

1. **Creation and ownership:** two real services with generated names and distinct users/policies.
   Renaming the display name leaves the bucket unchanged; the schema rejects endpoint changes.
   Unit tests cover retries without duplicates, foreign resources, policy drift, unexpected groups,
   invalid UUIDs and uncertain-ownership windows that must fail without further mutations.
2. **Secrets:** administrative configuration resolves from the vault; customer secrets are removed
   after normal completion. Runtime data and captured agent logs contain no secret values.
   Logger tests cover raw JSON bytes, groups, LogValuer values and panic data.
3. **Isolation:** cross-bucket reads/writes and anonymous access are denied. The gateway blocks
   administrative, self-service and STS requests. Route aliases and normalization are tested
   even with valid administrative signatures. Native backend limitations still apply.
4. **Quotas:** required limits are read back before access is enabled. Tests cover oversized PUTs,
   rejected overwrites preserving existing data, copies over quota, eight concurrent 256 KiB writes
   against a 1 MiB limit, and oversized multipart uploads without a visible object.
   Increases succeed; rejected decreases leave the backend limit unchanged.
   These observations are not a universal hard-limit guarantee.
5. **Clients and rotation:** AWS CLI, boto3, URL-encoded keys, presigned PUT/GET and multipart uploads
   with 5+1 MiB parts, checking content and ordering. The old secret and URL are rejected after rotation.
6. **Suspension:** new requests are blocked; rotation while suspended does not enable access.
   Restarting the agent preserves suspension without a local customer secret, and resume succeeds.
7. **Force delete:** more than 1000 objects, versions, delete markers and an incomplete multipart upload.
   Bucket, user and policy are absent on completion. A real legal hold causes a partial failure,
   leaves credentials disabled and prevents resume without overriding the hold.
8. **Recovery:** with the fixture backend stopped, the agent enters Error and a new job stays Pending.
   After restart, the agent reconnects and the hold/deleting marker remain present.
   Deletion still fails until the test operator releases its own hold; an explicit retry then completes.
   Unit tests also cover lost acknowledgements, quota/IAM errors and deletion responses with no effect.
9. **Metrics:** all three metric types reach the core through stdagent. Missing data or absent quota
   causes an error rather than a zero sample. Ratios above 1 are not hidden.
10. **Packaging and boundaries:** schemas are accepted by the real core, the binary and non-root
    image build successfully, and the gateway remains separate from the agent runtime.

## Acceptance commands

Prepare the fixtures as described in the [README](../README.md), then run:

```sh
go test ./...
go test -race ./...
go vet ./...
.local/venv/bin/python tests/backend_contract.py
.local/venv/bin/python tests/test_iam_boundaries.py
bash tests/network_boundary.sh
go build -race -o .local/agent ./cmd/agent
.local/venv/bin/python -u tests/e2e.py
docker build -t fulcrum-agent-s3:dev .
docker run --rm --read-only --cap-drop ALL fulcrum-agent-s3:dev --help
```

The pagination test is not a benchmark. It uses limited concurrency and bounded standard
client retries for temporary accounting 503s, without removing quotas or accepting missing
uploads. The suite may take several minutes.

## Cleanup and limits

Successful runs delete their S3 data through the agent. The test releases only the legal
hold it created; the agent sends no bypass flag. Agent processes started by the suite are
stopped and their tokens revoked. Core metadata and local fixture volumes remain available
for inspection; generated credentials stay in ignored files with mode 0600.

Not verified or claimed: TLS, browser/CORS support, other clients or releases, cluster/HA,
filesystem crash consistency, backup/restore, production readiness, billing or a complete
core security audit. Processing-job recovery after crashes remains the existing core behavior.
