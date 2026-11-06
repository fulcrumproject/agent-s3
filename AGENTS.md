# Agent instructions

## Project context

The implementation follows `docs/CONTRACT.md`. Read `README.md`,
`docs/OPERATIONS.md` and `docs/VERIFICATION.md` before changing behavior.
The native backend limitations in `docs/COMPATIBILITY.md` still apply:
preserve the S3-only gateway and network boundary. `docs/DESIGN-HISTORY.md`
contains archived notes, not current requirements.

Use the same architecture as `../agent-proxmox-fae`: stdagent with a separate
RustFS client. Do not modify core, the shared library or other repositories
without explicit authorization.

## Documentation and workflow

- Write project documentation, README text, comments, and issue/PR descriptions in English.
  Use the user's language for conversation.
- Keep the README focused on purpose, setup and usage. Put detailed operational constraints
  in the relevant documents; do not add conversational notes or internal release commentary.
- Implement the agreed contract. For new product decisions or incompatibilities, ask one
  question at a time, with a recommendation and trade-offs.
- Investigate code-answerable questions rather than asking the user.
- Agree on scope and verifiable criteria before implementing changes.
- Do not expand scope to Docker, host, cluster or backend HA management.

## Local references

- `../agent-lib-go/pkg/agent/agent.go`: agent interfaces and models.
- `../agent-lib-go/pkg/stdagent/`: polling, health, metrics and secret resolution.
- `../agent-proxmox-fae/internal/vmagent/`: reference Go agent architecture.
- `../agent-test/`: additional reference; compare it with the current protocol.
- `../core/pkg/domain/service_type.go` and `service_lifecycle.go`: properties and lifecycle.
- `../core/pkg/domain/service.go` and `job.go`: services, jobs and runtime data.
- `../core/docs/SERVICE_TYPE.md`: documentation to check against implementation.

Read any AGENTS.md in repositories being inspected. Current code takes precedence over
outdated examples or documentation.

## Invariants

- One S3 service is one bucket on a shared backend, with service-scoped credentials.
- The provider deploys RustFS and the gateway. The agent manages buckets, credentials and quotas through APIs.
- RustFS 1.0.4 is the tested development target, not a production qualification.
- Check the selected release: current online documentation may describe different behavior.
- Never put secrets in Git, logs or unprotected runtime data. Use the existing core mechanisms.
- Do not claim complete S3 compatibility, absolute quota enforcement, durability or failover without evidence.
- Do not build a generic agent framework or Docker runtime. Extract shared code only for a concrete second use.

The implementation uses Apache-2.0, agent-lib-go, Fulcrum utilities, AWS SDK Go v2 and
a dedicated administrative client. Backend images are pinned by digest. Force delete
is intentional. HTTP is limited to isolated fixtures. Customer secrets are ephemeral
inputs, never stored on the agent's disk or returned in runtime data.

## Checks

Run `go test ./...`, `go test -race ./...`, `go vet ./...` and the acceptance commands
in `docs/VERIFICATION.md`. E2E requires an unchanged core checkout at `tests/core-revision`.
Do not run concurrent E2E suites on one fixture: the tests stop and restart their dedicated
RustFS instance. Never print environment files, raw sensitive logs or `docker compose config`
without `--quiet`. Keep `.local`, `.pi` and secrets out of Git. Report verification evidence
before declaring a delivery complete.
