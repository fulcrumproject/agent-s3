# RustFS 1.0.4 compatibility

The service security model requires an **S3-only customer gateway** and a private
administrative endpoint. The native IAM behavior described below has not been changed.
See [verification](VERIFICATION.md) for the complete agent and core acceptance results.

## Reference environment

- Official RustFS 1.0.4 OCI index:
  `sha256:ed9210d6411d77ffceb70d94b964a3cb7d225983368bc25bebe01e995293dcc2`.
- Linux amd64 manifest:
  `sha256:e287e99762a4a3e7b706526e01f2ff755f67db165e9be9f6b907adc6fb6d6ee1`.
- Docker 29.9.3, Compose v5.7.0, loopback HTTP endpoints and a dedicated persistent volume.
- boto3 1.43.125 and botocore 1.43.125; remaining versions are in `tests/requirements.txt`.
- Synthetic data with random `fulcrum-probe-` or `fc-boundary-` names.
  Credentials are generated locally, never displayed or committed.
- Each probe cleans up only resources it created. No production resources are used.

## S3 and administrative behavior

`tests/backend_contract.py` covers:

- Bucket creation and ownership-tag round trips.
- Mandatory quota configuration and readback.
- Accounting readiness before using a new bucket. PUT can return 503 immediately
  after creation and succeed once accounting becomes available.
- Bucket-scoped IAM access: PUT/GET work, access to another bucket is denied,
  and customer tag/policy changes are rejected. ListBuckets exposes only the permitted bucket.
- Rejection of additional users, service accounts and STS credentials.
- Presigned GET/PUT and multipart upload.
- Rejection of oversized PUT without creating an object.
- Administrative rotation preserving policy, with rejection of the old secret and URL.
- Suspend/resume without resubmitting the secret.

The initial probes ran directly against RustFS. The acceptance scripts now use the
customer gateway for customer requests and the private endpoint for setup and inspection.

## API details

- Use separate Admin and STS policy statements, without `Resource: "*"`.
  Use S3 ARNs for S3 resources; keep the explicit Deny statements.
- `set-user-secret-key` expects `secret_key` in snake_case, unlike `add-user`,
  which expects `secretKey`.
- Quota rejection uses `InvalidRequest` with a `Bucket quota exceeded. ...` message.
  Tests check both the error and the absence of the rejected object.
- `add-user` does not attach its policy. The agent performs an explicit policy binding.

## Native IAM limitations

### IAM users can change their own secret

With a dedicated user, a bucket-scoped policy and explicit Deny statements for
`admin:*` and `sts:*`:

1. Sign `POST /rustfs/admin/v3/account/password` with the customer's credentials.
2. Submit the current and replacement secrets using the release's expected fields.
3. The data-only service model requires rejection.
4. The native endpoint returns HTTP 200, and the replacement secret authenticates successfully.

This self-service operation is separate from administrative action checks. It does not
grant access to another bucket, but it conflicts with agent-only credential administration.

### Shared-parent service accounts can revoke siblings

The alternative of separate bucket service accounts under a shared administrative parent
also fails the required isolation:

1. Create A and B with separate policies and explicit `Deny admin:*`.
2. Use A to request deletion of B's credentials.
3. B should remain present and the request should be denied.
4. The native endpoint returns HTTP 200; an administrative lookup of B then returns 404.

The release's ownership fallback uses the shared parent. This observation does not apply
to every possible IAM model. The agent uses dedicated IAM users, not this alternative.

## Gateway enforcement

The provider must keep customers away from the native RustFS endpoint and expose only the
gateway's S3 routes. The agent continues to call private administrative APIs directly.
No permission exception or backend/core/shared-library modification is required.

`tests/test_iam_boundaries.py` runs the negative cases through the customer endpoint
on port 19080, while setup and state checks use private port 19000. All three tests pass,
including administrative route aliases. They are not skipped or marked as expected failures.

`tests/network_boundary.sh` verifies that the customer network cannot reach the native
backend, even by IP. These checks validate the deployment boundary, not a fix to RustFS's
native IAM behavior or production readiness.

## Reproduce

With the isolated fixtures running:

```sh
.local/venv/bin/python tests/backend_contract.py
.local/venv/bin/python tests/test_iam_boundaries.py
bash tests/network_boundary.sh
```

Complete core integration, build and lifecycle checks are listed in [verification](VERIFICATION.md).
