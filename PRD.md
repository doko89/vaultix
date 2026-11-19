# Product Requirements Document (PRD)
## Vaultix — Centralized Infrastructure Backup Platform

**Status:** Draft  
**Product type:** Self-hosted backup management platform  
**Primary implementation language:** Go  
**Companion docs:** [`docs/TECH_SPEC.md`](docs/TECH_SPEC.md) (approved dependency set & implementation layout), [`docs/DESIGN.md`](docs/DESIGN.md) (design system & UI mapping, primary color red)

---

## 1. Product Overview

Vaultix is a centralized infrastructure backup platform for managing and executing backups across multiple virtual machines (VMs). A central Manager provides the web interface, API, scheduling, policy management, job orchestration, monitoring, and restore coordination. A lightweight Vaultix Agent runs on each target VM and performs backup and restore operations.

Vaultix supports host filesystem paths, Docker workloads, persistent Docker volumes, and databases including PostgreSQL, MySQL/MariaDB, and Microsoft SQL Server. Backup data is compressed, optionally encrypted, and uploaded directly from the Agent to an S3-compatible object storage service.

## 2. Problem Statement

Administrators often manage backups independently across VMs and application stacks. This creates operational overhead and makes it difficult to:

- Apply consistent backup policies across multiple VMs.
- Select specific directories, Docker resources, and databases for backup.
- Monitor backup health from one place.
- Manage retention and storage usage.
- Restore individual files, databases, or complete application stacks.
- Verify that backups are complete, valid, and recoverable.

Vaultix addresses these problems through centralized policy and job management with execution delegated to per-VM agents.

## 3. Product Goals

1. Provide a single web interface to manage backup operations across multiple VMs.
2. Allow administrators to register, monitor, and manage Vaultix Agents.
3. Support configurable backup targets for host files, Docker resources, and supported databases.
4. Automate backups through schedules and reusable policies.
5. Store backup artifacts in S3-compatible object storage.
6. Support reliable restore workflows at file, database, volume, and application-stack levels.
7. Provide backup history, execution status, integrity information, and operational alerts.
8. Keep backup data transfers between Agents and object storage, without routing backup payloads through the Manager.

## 4. Non-Goals for Initial Release

The initial release will not aim to provide:

- Full VM image or hypervisor-level snapshots.
- Continuous data protection with near-zero recovery point objectives.
- Automatic application-aware consistency for every third-party application.
- Kubernetes cluster backup.
- Cross-region replication and multi-site disaster recovery orchestration.
- Automatic discovery and backup of every database without administrator configuration.
- A multi-tenant public SaaS billing platform.

These may be considered for later releases.

## 5. Users and Roles

### 5.1 Administrator

- Register and revoke Agents.
- Configure object storage.
- Create, edit, enable, and disable backup policies.
- Configure schedules and retention.
- Start, cancel, and inspect backup and restore jobs.
- Manage credentials and access permissions.
- View audit logs and system health.

### 5.2 Operator

- View Agents, policies, backup history, and storage usage.
- Start permitted backup or restore jobs.
- Review job failures and notifications.
- Cannot change global security or storage configuration unless granted permission.

### 5.3 Read-only User

- View dashboards, Agents, policies, and job history.
- Cannot create, modify, or execute operations.

Role-based access control (RBAC) is required. The exact permission matrix is configurable by role.

Users may authenticate with local credentials or via OpenID Connect (OIDC) Single Sign-On. OIDC-authenticated users receive roles through configurable group/claim-to-role mapping, with Just-In-Time (JIT) provisioning on first login as the default.

## 6. System Architecture

### 6.1 Components

**Vaultix Manager**
- Web UI.
- REST API.
- Agent registry and enrollment.
- Policy management.
- Scheduler.
- Job orchestration and state management.
- Metadata database.
- Storage configuration.
- Notifications and audit logging.
- Restore coordination.

**Vaultix Agent**
- Runs as a service on each target VM.
- Authenticates with the Manager.
- Sends heartbeat and system information.
- Discovers Docker resources when Docker integration is enabled.
- Receives and validates assigned jobs.
- Executes filesystem, volume, and database backup operations.
- Compresses backup data and encrypts it when encryption is enabled for the policy.
- Uploads artifacts directly to object storage.
- Reports progress, results, and errors.
- Executes authorized restore operations.

**Object Storage**
- Stores backup chunks, manifests, metadata artifacts, and other backup objects.
- Supports S3-compatible APIs.
- Is accessed directly by Agents using short-lived, scoped presigned URLs or equivalent restricted credentials.

**Metadata Database**
- Stores users, roles, Agents, policies, schedules, jobs, backup snapshots, manifests metadata, storage configurations, and audit events.
- PostgreSQL is the recommended initial implementation.

### 6.2 Control Plane and Data Plane

The Manager is the **control plane**. It determines what should be backed up, when jobs run, which Agent executes them, and where artifacts are stored.

The Agent is the **data plane**. It discovers resources, reads data, creates backup artifacts, compresses them, encrypts them when enabled, transfers them to object storage, and performs restore operations.

Backup payloads must not pass through the Manager.

### 6.3 Communication Model

Use an Agent-initiated connection model to support VMs behind NAT or firewalls.

- Agent enrollment and API communication use HTTPS.
- Job delivery may use polling initially, with gRPC streaming as an optional later enhancement.
- Every Agent has a unique identity and credential.
- TLS is mandatory.
- Mutual TLS is recommended for production deployments.
- The Manager must validate Agent identity and job authorization.

## 7. Functional Requirements

### 7.1 Authentication and User Management

- Users can sign in to the Manager UI with local credentials or via OpenID Connect (OIDC) Single Sign-On.
- Administrators can create, disable, and manage local users.
- The system supports role-based access control for both local and OIDC-authenticated users.
- Sensitive actions require authorization checks.
- User sessions and authentication credentials must be protected.
- Security-sensitive actions, including local and OIDC login attempts, are recorded in audit logs.
- At least one local administrator (break-glass) account must remain usable when OIDC is enabled, unless an administrator explicitly disables local login with confirmation.

#### 7.1.1 OpenID Connect (OIDC) Support

The Manager acts as an OIDC Relying Party for interactive user login to the Web UI and API session issuance. Agent authentication is unchanged and must not use the interactive OIDC flow.

Provider configuration:
- Support OIDC Discovery via `/.well-known/openid-configuration`, with manual endpoint override for issuer, authorization, token, JWKS, userinfo, and end-session endpoints.
- The first release must support at least one OIDC provider; supporting multiple concurrent providers is recommended if it does not delay the release.
- Administrators can enable, disable, and test a provider configuration (discovery + authenticated test) without locking themselves out.
- Provider client secrets are stored encrypted at rest and are never returned by read APIs or written to logs.

Login flow:
- Use Authorization Code Flow with PKCE for UI login.
- Use `state` and `nonce` parameters and validate them on callback to prevent CSRF and replay attacks.
- Request configurable scopes; default to `openid profile email`, with optional provider-specific group/role scopes.
- Validate the ID Token signature, issuer (`iss`), audience (`client_id`), expiry (`exp`), issued-at (`iat`), and `nonce`.
- Refresh provider signing keys from the JWKS URI with caching and refresh to support key rotation.
- On callback errors (user denied access, expired code, invalid state), show an actionable error and record an audit event.

Identity, provisioning, and role mapping:
- Identify each OIDC user by the stable pair of provider (issuer) and subject (`sub`).
- Support Just-In-Time (JIT) provisioning on first login (default) and an optional mode that only allows pre-provisioned users whose identity is already linked.
- Map provider group/role claims (configurable claim names, e.g. `groups` or `roles`) to Vaultix roles (Administrator, Operator, Read-only User). Support a configurable default role; users with no mapped role must be denied access with an audit event.
- Refresh role mapping from claims on each login; removing a user from all mapped groups must remove elevated access on next login.
- Allow administrators to view, link, unlink, and disable the link between a local user record and an OIDC identity.

Sessions and logout:
- Issue a Manager session after successful OIDC login; Manager session lifetime and idle timeout apply equally to local and OIDC sessions.
- Terminate the local Manager session on logout at minimum; perform OIDC RP-Initiated Logout via `end_session_endpoint` when the provider publishes one.

### 7.2 Agent Management

The Manager must allow administrators to:

- Create or issue an Agent enrollment token.
- Register an Agent to the Manager.
- View Agent hostname, IP address, operating system, architecture, and Agent version.
- View last heartbeat and online/offline status.
- View reported disk capacity and available space when available.
- View Agent capabilities and discovered Docker resources.
- Assign and inspect backup policies for an Agent.
- Revoke or disable an Agent.
- Identify outdated Agent versions.

Agent requirements:

- Run as a background service on supported Linux VMs.
- Start automatically after VM reboot.
- Maintain periodic heartbeat.
- Reconnect after network interruption.
- Persist enough local job state to recover safely after restart.
- Reject unauthorized or malformed jobs.
- Report execution progress and structured errors.

### 7.3 Resource Discovery

The Agent must report supported resources to the Manager.

**Host resources**
- Configured filesystem paths.
- Filesystem and mount information where accessible.
- Basic host information.

**Docker resources**
- Running and stopped containers.
- Container ID, name, image, and state.
- Mounted paths and volume associations.
- Docker named volumes.
- Docker Compose project metadata where available.
- Container labels and relevant metadata.

Discovery is informational. Resources must not be backed up automatically unless included in an explicit policy.

Docker integration must be optional and configurable. Access to the Docker Engine socket is highly privileged and must be restricted to the trusted Agent process.

### 7.4 Backup Policy Management

Administrators can create policies and assign them to one or more Agents.

A policy must support:

- Policy name and description.
- Target Agent or Agent group.
- Backup target type.
- Selected resources.
- Backup method and method-specific options.
- Schedule and timezone.
- Retention rules.
- Compression settings.
- Encryption settings: enabled or disabled (default disabled), plus key reference when enabled.
- Destination object storage and bucket/prefix.
- Enabled or disabled state.

Supported target types:

1. Host filesystem.
2. Docker container-related files and configuration.
3. Docker named volumes and bind mounts.
4. PostgreSQL database.
5. MySQL/MariaDB database.
6. Microsoft SQL Server database.
7. Redis persistence data, subject to supported RDB/AOF procedures.
8. Application stack definition and associated selected resources.

Policies must be versioned or otherwise preserve the exact configuration used by each executed job. Changes to a policy must not silently alter the recorded configuration of completed backups.

### 7.5 Filesystem Backup

- Administrators can specify multiple absolute paths.
- Include and exclude patterns are supported.
- The Agent reads only paths authorized by the policy.
- The system records file metadata needed for restore, such as path, size, modification time, and permissions where supported.
- Symbolic link handling must be configurable and documented.
- The system must handle files changing during backup and report consistency limitations.
- Backup can be full initially; incremental backup is a later capability.
- Restore can target the original or an alternate directory, subject to permissions.

### 7.6 Docker Backup

The platform must distinguish between container metadata, container writable layers, mounted data, and database contents.

Supported capabilities:

- Discover containers and volumes.
- Select Docker resources in a backup policy.
- Back up selected bind-mounted paths.
- Back up selected named volumes.
- Capture container configuration metadata needed to recreate a workload.
- Capture Docker Compose configuration when the Agent can access it.
- Capture image references and image digests where available.
- Restore selected volume data and configuration artifacts.

The platform must not treat a running container's writable layer as a substitute for backing up persistent application data. Persistent data should be backed up from its mounted volume or bind mount.

Container image export is optional and should not be required for the initial release. Prefer recording the image reference and digest.

### 7.7 Database Backup

Database backup must use database-aware backup procedures. Copying a live database data directory as ordinary files is not an acceptable default backup method.

#### PostgreSQL
- Support logical backup using `pg_dump`.
- Support database or cluster scope as configured.
- Physical backup using `pg_basebackup` may be added as an advanced mode.
- Record PostgreSQL version and backup method.
- Support secure credential profiles.
- Document restore requirements and compatibility constraints.

#### MySQL / MariaDB
- Support logical backup using `mysqldump` or compatible tooling.
- Physical backup using an appropriate tool such as Percona XtraBackup may be added as an advanced mode.
- Record database server version and backup method.
- Support secure credential profiles.
- Document restore requirements and compatibility constraints.

#### Microsoft SQL Server
- Support native SQL Server backup operations, including full backup.
- Differential and transaction log backup may be supported when the database recovery model and configuration permit them.
- Record SQL Server version, database name, and backup type.
- Support secure credential profiles.
- Document restore requirements and compatibility constraints.

#### Redis
- Support configured RDB and/or AOF persistence backup procedures.
- Validate that the selected persistence artifact is in a recoverable state.
- Document consistency and recovery behavior.

Database credentials must never be stored in plaintext in policy records, job payloads, or logs. Secrets must be encrypted at rest and exposed only to the authorized Agent for the minimum required duration.

### 7.8 Application Stack Backup

An Application Stack represents a related set of resources on one VM, such as:

- Application container.
- Database container.
- Cache container.
- Reverse proxy container.
- Docker Compose configuration.
- Required environment configuration.
- Persistent volumes or bind mounts.

Administrators can group these resources into one logical backup policy or recovery plan.

The system must:
- Record stack membership and dependencies.
- Record configuration and image references.
- Back up persistent data using the appropriate filesystem or database-aware method.
- Support restore planning with an explicit restore order.
- Avoid claiming application-consistent recovery unless the required quiescing, database backup, and coordination steps have completed successfully.

Sensitive environment files must be encrypted and access-controlled. Secrets should be excluded by default unless explicitly selected.

### 7.9 Scheduling

- Support one-time and recurring jobs.
- Initial recurring schedules: hourly intervals, daily, and weekly.
- Store and display the configured timezone.
- Prevent unintended overlapping jobs for the same protected resource.
- Support configurable concurrency limits.
- Provide manual job execution.
- Provide pause and resume controls for schedules.
- Record the schedule definition used for each job.

### 7.10 Backup Job Management

Each job must have a unique identifier and track:

- Agent.
- Policy and policy version.
- Target resources.
- Start and end timestamps.
- Current state.
- Progress where measurable.
- Bytes read, compressed, and uploaded where available.
- Compression ratio where measurable.
- Encryption status (enabled or disabled) and key reference when enabled.
- Object storage destination.
- Snapshot and manifest identifiers.
- Error details and retry history.

Required job states:

- Queued.
- Assigned.
- Running.
- Uploading.
- Verifying.
- Succeeded.
- Failed.
- Cancelled.

The system must support safe retry behavior. Retrying a job must not create an invalid or falsely complete snapshot.

### 7.11 Compression and Encryption

- Use Zstandard (zstd) as the default compression format.
- Compression level must be configurable.
- Backup payload encryption is optional and configurable per policy; it is disabled by default.
- When encryption is enabled, encrypt backup data before upload using authenticated encryption, such as AES-256-GCM, with secure nonce handling.
- Encryption keys must be managed separately from backup objects.
- Key rotation and recovery procedures must be documented.
- The Manager must not log encryption keys or plaintext secrets.
- Each snapshot manifest must record whether the backup is encrypted and, when enabled, the key reference needed for restore. Unencrypted snapshots must be clearly labeled as unencrypted in the catalog.

### 7.12 Object Storage Management

- Support S3-compatible object storage.
- Allow administrators to configure endpoint, region, bucket, and prefix.
- Support connection testing.
- Support per-policy destination selection.
- Use short-lived presigned URLs or narrowly scoped temporary credentials for Agent uploads and downloads.
- Support multipart upload for large objects.
- Support retry and resume where the storage provider permits it.
- Store object metadata and checksums.
- Verify uploaded object integrity before marking a snapshot successful.
- Do not expose long-lived storage credentials to Agents.

### 7.13 Backup Catalog and Restore

The Manager must provide a catalog of completed snapshots.

Users can:
- Browse snapshots by Agent, policy, target, and date.
- View snapshot details and manifest metadata.
- Restore an individual file or directory where supported.
- Restore a database backup.
- Restore Docker volume data.
- Restore selected application stack resources.
- Choose an alternate restore destination where supported.
- View restore job status and errors.

Restore operations must require explicit authorization and confirmation. The system must warn before overwriting existing data.

A snapshot must not be presented as restorable until its manifest and required objects have passed the configured integrity checks.

### 7.14 Retention Management

- Support retention by age and/or number of snapshots.
- Retention must not delete the only known valid recovery point unless explicitly configured and confirmed.
- Deletion must account for shared or deduplicated objects if deduplication is implemented later.
- Retention actions must be auditable.
- Support storage lifecycle policies where compatible with the provider.

### 7.15 Dashboard and Monitoring

The dashboard must show:

- Total registered Agents.
- Online and offline Agents.
- Backup jobs by status.
- Recent successful and failed backups.
- Latest backup time per Agent or policy.
- Storage consumption where available.
- Backup duration and data volume trends.
- Policies with no recent successful backup.

The UI must provide filtering and search for Agents, policies, jobs, and snapshots.

### 7.16 Notifications and Audit Logs

- Notify configured recipients about failed jobs and relevant Agent state changes.
- Notification channels may include email and webhook.
- Record security-sensitive and administrative actions.
- Audit entries must include actor, action, target, timestamp, and outcome.
- Do not include secrets or backup file contents in logs.

## 8. Web UI Requirements

> Visual/interaction reference: [`docs/DESIGN.md`](docs/DESIGN.md) (color tokens, layout, page map). This section defines behavior; DESIGN.md defines appearance.

The Manager UI must provide the following sections:

| Section | Main capabilities |
|---|---|
| Dashboard | System overview and backup health |
| Agents | Register, inspect, disable, and monitor Agents |
| Resource Discovery | Browse host and Docker resources |
| Backup Policies | Create, edit, assign, and disable policies |
| Schedules | Manage recurring backup schedules |
| Jobs | Monitor, inspect, retry, and cancel jobs |
| Backup Catalog | Browse snapshots and manifests |
| Restore | Create and monitor restore jobs |
| Storage | Configure and test object storage |
| Credentials | Manage protected database and service credentials |
| Users & Roles | Manage access and permissions, including OIDC identity links and group-to-role mapping |
| Notifications | Configure alert destinations |
| Audit Logs | Review administrative and security events |
| Settings | Global configuration, OIDC provider configuration, and system health |

The UI must clearly distinguish:
- Host filesystem paths.
- Docker container metadata.
- Docker persistent volumes and bind mounts.
- Database backup targets.
- Application stack groupings.

The login page must offer each enabled OIDC provider as a Single Sign-On button alongside local login (when local login is enabled), and must display actionable errors for failed OIDC callbacks without exposing tokens or secrets.

## 9. Data Model — Initial Entities

The initial metadata schema should include:

- `users`
- `roles`
- `user_roles`
- `oidc_providers`
- `user_external_identities`
- `agents`
- `agent_credentials`
- `agent_heartbeats`
- `discovered_resources`
- `storage_destinations`
- `secret_profiles`
- `backup_policies`
- `backup_policy_versions`
- `backup_policy_targets`
- `schedules`
- `backup_jobs`
- `restore_jobs`
- `snapshots`
- `snapshot_manifests`
- `snapshot_objects`
- `retention_rules`
- `application_stacks`
- `application_stack_resources`
- `notifications`
- `audit_logs`

Exact columns and relationships will be defined during technical design.

## 10. Non-Functional Requirements

### 10.1 Reliability
- Agent must recover from temporary Manager or storage connectivity failures.
- Incomplete uploads must not be marked as successful snapshots.
- Job state transitions must be persisted.
- Backup metadata and manifests must be durable.
- Restore operations must validate required artifacts before execution.

### 10.2 Security
- TLS for all network communication.
- Unique Agent identity and revocable credentials.
- RBAC for Manager operations.
- Encryption at rest for secrets; backup payload encryption is optional per policy (default disabled).
- No plaintext credentials in logs.
- Least-privilege access to files, databases, Docker, and object storage.
- Audit logging for privileged actions.
- Restore operations protected against unauthorized execution.
- OIDC: strict issuer/audience/expiry/nonce validation, PKCE + `state` on every login, encrypted client secrets, JWKS refresh for key rotation, and no tokens or claims with PII beyond the minimum in logs.

### 10.3 Performance and Scalability
- Backup payloads transfer directly from Agent to object storage.
- Manager handles metadata and job coordination rather than bulk data transfer.
- Support configurable per-Agent and global concurrency.
- Support large files through multipart uploads and chunked processing.
- Avoid loading entire backup datasets into memory.
- Design the Manager to support increasing Agent counts without requiring a microservices architecture initially.

### 10.4 Compatibility
- Initial Agent target: supported Linux distributions.
- Docker integration uses the Docker Engine API.
- Object storage uses S3-compatible APIs.
- Database tooling and supported server versions must be documented.
- Windows Agent and SQL Server deployment compatibility require a separately defined support matrix.

### 10.5 Observability
- Structured logs.
- Job-level error reporting.
- Agent heartbeat monitoring.
- Metrics for job duration, throughput, failures, and storage usage.
- Health endpoints for Manager components.

## 11. Recommended Technology Stack

> Implementation reference: [`docs/TECH_SPEC.md`](docs/TECH_SPEC.md) (approved libraries, repo layout, bootstrap commands).

| Component | Technology |
|---|---|
| Manager backend | Go |
| Agent | Go |
| Web frontend | React + TypeScript |
| UI components | Tailwind CSS + shadcn/ui |
| API | REST |
| User SSO | OIDC Relying Party (Authorization Code + PKCE; e.g. `go-oidc` + `golang.org/x/oauth2`) |
| Agent communication | HTTPS initially; gRPC streaming as an option |
| Metadata database | PostgreSQL |
| Database access | pgx or sqlc |
| Docker integration | Docker Engine Go SDK |
| Compression | Zstandard |
| Encryption | AES-256-GCM (optional per policy, default off) |
| Object storage | S3-compatible API |
| Scheduling | Go-based scheduler with persisted schedules |
| Metrics | Prometheus |
| Deployment | Docker Compose initially |

A modular monolith is recommended for the initial Manager implementation. The Agent should be a separate executable and deployable service.

## 12. MVP Scope

The MVP must deliver a complete and testable backup-to-restore workflow.

### Included
1. Manager authentication (local users + basic OIDC SSO with single provider, JIT provisioning, and group-to-role mapping) and basic roles.
2. Agent enrollment, heartbeat, and status.
3. Host filesystem discovery and backup selection.
4. Docker container and volume discovery.
5. Backup of selected filesystem paths and Docker persistent volumes.
6. PostgreSQL logical backup.
7. MySQL/MariaDB logical backup.
8. S3-compatible object storage integration.
9. Zstandard compression and optional encryption of backup artifacts (default off).
10. Manual and daily scheduled backup jobs.
11. Job monitoring and backup history.
12. Snapshot catalog and manifest.
13. File and directory restore.
14. Database restore workflow.
15. Basic retention policy.
16. Audit log for critical operations.

### Excluded from MVP
- Advanced incremental backup and global deduplication.
- Physical PostgreSQL and MySQL backup modes.
- Advanced SQL Server differential and transaction log workflows.
- Complete application-stack automated recovery.
- Container image export.
- Kubernetes.
- Cross-region replication.
- Multi-tenant billing.
- Advanced RBAC customization (beyond basic OIDC group-to-role mapping).
- Advanced SSO: multiple concurrent OIDC providers (if deferred), SAML, SCIM provisioning, and IdP-initiated login.

## 13. Acceptance Criteria

The MVP is acceptable when:

1. An administrator can enroll multiple Linux VMs from the Manager UI.
2. The Manager displays Agent status and last heartbeat.
3. An Agent can discover Docker containers and volumes when Docker integration is enabled.
4. An administrator can select host paths or Docker persistent volumes for backup.
5. An administrator can configure PostgreSQL and MySQL/MariaDB logical backup targets.
6. An administrator can create and run a backup policy manually.
7. A daily schedule can trigger a backup without manual intervention.
8. Backup data is compressed before upload, and encrypted before upload when encryption is enabled for the policy.
9. Backup payloads upload directly from Agent to configured object storage.
10. The Manager records job state and snapshot metadata.
11. A completed snapshot is verified before being marked successful.
12. An administrator can restore a selected file or directory.
13. An administrator can execute a database restore workflow with explicit confirmation.
14. Failed jobs display actionable error information.
15. Secrets are not exposed in plaintext through the UI, API responses, job payloads, or logs.
16. A backup is never reported as successful when required objects or manifest verification are incomplete.
17. A user can sign in via the configured OIDC provider; first login provisions the user and assigns the mapped role, and subsequent logins refresh the role from claims.
18. A user with no mapped OIDC role is denied access with an audit event, and OIDC client secrets and tokens never appear in logs or API responses.

## 14. Key Risks and Mitigations

| Risk | Mitigation |
|---|---|
| Live database files are inconsistent when copied | Use database-native backup procedures |
| Docker socket grants excessive host privileges | Restrict socket access to the trusted Agent and minimize operations |
| Agent loses connection during upload | Retry and resume supported multipart uploads |
| Backup object exists but metadata is incomplete | Use manifest-based completion and verification |
| Restore overwrites production data | Require explicit confirmation and support alternate destinations |
| Secrets leak through job configuration | Use encrypted secret profiles and short-lived delivery |
| Concurrent jobs affect database or VM performance | Configure concurrency limits and resource controls |
| Compression or encryption increases CPU usage | Make compression level and concurrency configurable |
| Retention removes needed recovery points | Validate retention rules and preserve configured recovery points |
| OIDC misconfiguration locks out administrators | Keep a break-glass local admin, validate/test provider config before activation, and audit SSO logins |
| Identity provider outage blocks logins | Keep local break-glass login, short Manager sessions with clear errors, and documented IdP recovery steps |

## 15. Product Success Metrics

- Percentage of scheduled backup jobs completed successfully.
- Percentage of registered Agents reporting healthy heartbeats.
- Time from job scheduling to execution.
- Backup throughput and duration.
- Restore success rate.
- Time required to restore a selected file or database.
- Number of policies without a recent successful backup.
- Storage consumption and compression ratio.
- Number of backup integrity verification failures.

## 16. Open Technical Decisions

The following decisions must be resolved during technical design:

1. Whether Agent job delivery starts with polling or persistent gRPC streaming.
2. Exact Linux distributions and database versions supported in the first release.
3. Whether the first release includes Windows Agent support for SQL Server.
4. Backup chunking and manifest format.
5. Encryption key ownership, recovery, and rotation model.
6. Exact consistency strategy for files that change during backup.
7. Database credential delivery and secret-management implementation.
8. Whether restore execution is always Agent-based or can use a dedicated restore worker.
9. Retention behavior when snapshots share objects.
10. Whether application-stack restore is included in the first post-MVP release.
11. Whether the first release supports multiple concurrent OIDC providers or a single provider.
12. OIDC session policy details: Manager session lifetime, idle timeout, and whether to force re-authentication for sensitive actions.
13. Whether SCIM (or another automated deprovisioning mechanism) is needed post-MVP beyond claim refresh on login.

---

**End of document.**
