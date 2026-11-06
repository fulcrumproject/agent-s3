# Service contract — agent-s3 v1

## Scope

- Go, following agent-proxmox-fae's architecture: cmd/agent, internal/config,
  internal/s3agent for handlers, health and metrics, and internal/rustfsapi behind an interface.
- Reuse agent-lib-go/stdagent and Fulcrum utilities without extending their APIs.
- AWS SDK for Go v2 for S3 and SigV4; an internal RustFS administrative client.
- One shared backend per agent, one private bucket per service, one active replica per agentId.
- Provider-managed backend and gateway. No Docker, host or cluster management in the agent.
- RustFS 1.0.4, tested with a digest-pinned image.
- HTTP and path-style requests on localhost or an isolated test network.
  Separate administrative and customer endpoints; no Internet exposure or production qualification.

## Acceptance criteria

1. **Creation:** derive immutable bucket names and accessKeyId values from serviceId.
   The display name is independent. Never implicitly adopt existing resources or use
   their names alone as proof of ownership.
2. **Secrets:** accept the customer's secret through an ephemeral property.
   Never return it in agentInstanceData, persist it on the agent's disk or log it.
   The customer keeps a copy and resubmits it when a retry needs a value already removed from the vault.
3. **Access:** allow the agreed S3 operations only on the service's bucket: reading, writing,
   listing and deleting objects, multipart uploads, and presigned PUT/GET URLs.
   Deny other buckets, anonymous access, IAM/policy/ACL/bucket administration,
   additional credentials, and direct bucket creation or deletion.
4. **Quota:** require a positive byte limit, applied and read back before enabling access.
   Never fall back to unlimited storage. Updates allow increases or no-ops;
   rejected decreases must leave the backend limit unchanged.
5. **Rotation:** update the secret for the same accessKeyId, with one active credential pair
   and no overlap. Verify rejection of the old secret and its presigned URLs.
6. **Suspend/resume:** disable and re-enable credentials without resubmitting the secret.
   Preserve data and quota. Do not promise to interrupt in-flight requests;
   unexpired URLs may become usable again after resumption.
7. **Deletion:** force delete objects, versions/delete markers, incomplete multipart uploads,
   the bucket, credentials and policy belonging to the service.
   Never bypass retention or Object Lock. Disable access before deleting data.
   Partial deletion failures must not re-enable access; a new delete resumes cleanup.
   Never report success while resources remain.
8. **Recovery:** make handlers idempotent for resources with verified ownership.
   Never destructively retry against foreign or uncertain resources.
   Do not introduce another recovery framework.
9. **Monitoring:** OnHealth checks backend reachability and authentication.
   OnMetrics reports backend usage, quota and their ratio. Never turn errors into zero samples.
   Usage may lag and is not a billing measurement.
10. **Logging:** prevent stdagent's resolved-configuration dump from reaching logs,
    without changing the shared library, and cover it with a regression test.
11. **Verification:** require unit tests and real tests against RustFS 1.0.4 and the unchanged core,
    using AWS CLI and boto3. Cover two-service isolation, forbidden APIs, multipart/presigned requests,
    quotas including concurrent writes, copies and overwrites, rotation, suspension,
    force delete and partial failures. Report incompatibilities rather than weakening assertions
    or modifying other repositories to conceal them.
12. **Delivery:** provide a Go binary build, non-root Dockerfile, test Compose stack,
    AgentType/ServiceType examples, documentation and an Apache-2.0 license.
    Pin dependency versions.

## Core and runtime limitations

stdagent processes Pending jobs; it does not automatically resume Processing jobs after a crash.
When enabled, the core maintenance worker can mark timed-out jobs Failed, allowing an explicit retry.
The timeout uses createdAt rather than claimedAt. That path does not apply lifecycle transitions
or clean up secrets: ephemeral is not a guaranteed expiration time after a crash.
Configure fixture timeouts explicitly.

Do not introduce hidden persistent secret copies to work around these limitations.
Report backend or core blockers instead of extending those repositories.

## Customer boundary

Customers reach only the gateway's S3 endpoint. The native RustFS administrative endpoint
is private to the agent and trusted operators. The gateway preserves S3 signatures and
traffic while blocking administrative, self-service and STS routes.

The agent remains a control-plane component and does not proxy objects.
Use dedicated IAM users, not service accounts with a shared administrative parent.
Run IAM boundary tests through the customer endpoint and retain the documented native
backend behavior. Direct customer access to RustFS invalidates the security model.

## Exclusions

Customer-managed versioning, lifecycle, Object Lock or encryption; TLS infrastructure;
agent/backend HA; host/container/cluster management; bucket imports or custom names;
decreasing or unlimited quotas; overlapping credentials; request/traffic metrics;
durability, billing or complete S3-compatibility guarantees; Helm and DEB/RPM packages.
