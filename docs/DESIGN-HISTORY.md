# Archived design notes

These notes describe the project **before implementation**. They are not current requirements;
use the [service contract](CONTRACT.md), [operations guide](OPERATIONS.md) and
[verification results](VERIFICATION.md) instead.

At the time, implementation was paused while work continued on agent-mysql with Docker
as its runtime. Incus had been considered and set aside; it was not a dependency of agent-s3.

## Original goal and decisions

Build an open-source Fulcrum agent for S3-compatible object storage.

- Use a shared backend: one service represents one bucket and its credentials,
  not a dedicated storage instance or container.
- The provider deploys RustFS, optionally using Docker with persistent storage.
- The agent manages buckets, credentials and quotas through APIs, potentially remotely.
- Host, container and cluster management remain outside the initial scope.
- Restrict customer credentials to their own bucket; never distribute administrative credentials.
- Keep the MySQL and S3 agents separate. S3 operations do not justify a shared Docker library.

These decisions did not yet define a complete implementation contract or validate RustFS
for any particular workload.

## Technical proposal at the time

- Use Go and agent-lib-go, following agent-proxmox-fae's architecture.
- Use an S3 SDK for standard operations.
- Use RustFS-specific administrative APIs for users, policies, credentials and quotas.
- Configure an administrative endpoint and suitable credentials.
- Return endpoint, bucket, optional region and secure access information through core mechanisms.
- Define a bucket lifecycle rather than copying VM/container start and stop behavior.

SDK selection, schemas, actions, states, metrics and secret delivery were still open.

## Findings from the initial research

The documentation described Apache-2.0 licensing for RustFS, Docker/Podman deployment,
IAM users/policies/service accounts/temporary credentials, a tested S3 API subset,
and byte-based bucket quotas rather than request or performance quotas.
The agent's own license had not yet been selected.

The GitHub release API listed 1.0.4, published on October 25, 2026, without a prerelease flag.
That was a research observation, not a version pin or a production qualification.

### Historical quota warnings

The documentation reviewed at that stage warned that:

- unreadable or invalid configuration could allow writes with
  `Bucket quota check degraded to allow`;
- usage statistics could lag behind changes;
- overwrites could require free space for the complete replacement object;
- quota preflight was advisory rather than a capacity reservation.

These were reasons to test the selected release, especially concurrency and multipart.
Later implementation checks found durable reservation support in 1.0.4; do not treat
the historical warnings as a complete description of the current test target.

### Durability and isolation

A persistent Docker volume is not a backup, replication or failover.
Policy isolation is logical: CPU, I/O, storage capacity and failures remain shared.
Storage topology, durability guarantees and recovery procedures belong to the provider.

## Questions that were open

1. Required clients and S3 features: basic operations, multipart, presigned URLs,
   versioning, lifecycle, encryption and Object Lock.
2. RustFS release and deployment topology for development and verification.
3. One or multiple backends per agent, and backend assignment.
4. Service properties and actions.
5. Credential count, generation, delivery, storage, rotation and revocation.
6. Mandatory quota semantics, shared capacity and unavailable enforcement.
7. Deletion of objects, versions, incomplete uploads and retained data.
8. Idempotence and partial-failure recovery without touching unmanaged resources.
9. Metrics, health and acceptance criteria.
10. License, packaging and distribution.

## Proposed verification

- Two-service isolation across reads, writes and administrative operations.
- Repeated creation and recovery without duplicate or orphaned resources.
- Effective rotation and revocation, including previously signed requests where applicable.
- Quotas on ordinary/multipart uploads, copies, overwrites and concurrent writes.
- Restart persistence and a separately verified backup/restore procedure.
- Backend outage and recovery without false success.
- Deletion matching the chosen policy, including non-empty and versioned buckets.

These were proposed checks, not completed test results.

## Research references

- [RustFS repository](https://github.com/rustfs/rustfs)
- [RustFS releases](https://github.com/rustfs/rustfs/releases)
- [Container installation](https://docs.rustfs.com/en/installation/container)
- [S3 compatibility](https://docs.rustfs.com/en/reference/s3-compatibility)
- [IAM](https://docs.rustfs.com/en/security-compliance/iam)
- [Bucket quotas](https://docs.rustfs.com/en/administration/data/bucket/quota)

Online documentation may track development rather than the deployed release.
Compare documentation, code and observed behavior for the selected version.
