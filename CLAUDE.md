# Machine API Provider NVIDIA NCX Infrastructure Controller

OpenShift Machine API (MAPI) infrastructure provider for NICo
(NVIDIA NCX Infrastructure Controller). Manages instance
lifecycle for OpenShift worker nodes provisioned on NICo
bare-metal infrastructure.

## Build and test

```bash
go build ./...
go test ./... -v
# Integration tests (require envtest)
KUBEBUILDER_ASSETS=$(~/go/bin/setup-envtest use --print path) \
  go test ./test/integration/ -v
# E2E tests (require live NICo API)
NVIDIA_CARBIDE_API_ENDPOINT=https://... go test ./test/e2e/ -v
```

## Key files

- `pkg/apis/nicoprovider/v1beta1/types.go` — provider
  spec and status types
- `pkg/actuators/machine/actuator.go` — Create/Update/Exists/Delete
  (main logic, ~1500 LOC)
- `pkg/controllers/machine/controller.go` — reconciler loop
- `pkg/providerid/providerid.go` — provider ID parsing
- `pkg/metrics/metrics.go` — Prometheus metrics
- `pkg/apis/nicoprovider/v1beta1/webhook.go` — admission
  validation
- `test/` — unit, integration, e2e tests

## SDK

Uses `github.com/NVIDIA/infra-controller/rest-api/sdk/standard`.
go.mod uses a local `replace` directive:
```
replace github.com/NVIDIA/infra-controller/rest-api/sdk/standard => ../../NVIDIA/infra-controller/rest-api/sdk/standard
```
This requires a local checkout of the infra-controller repo.

## Current status

v0.3.0, alpha. Health management via HealthReport API.
Health features use HealthReport API with JSONB fallback when
API is unavailable.

---

## Completed work

### ~~1. Unify provider ID scheme~~ (DONE)

Provider ID scheme changed from `nvidia-carbide://` to `nico://`.
`ParseProviderID()` accepts both prefixes on read.
Finalizer changed to `machine.openshift.io/nico` with legacy
finalizer removal on delete.

### ~~2. Replace JSONB health parsing with structured health API~~ (DONE)

`updateMachineHealth()` calls `GetAllMachineHealthReport(org,
machineId)` via the HealthReport API and aggregates alerts from
all report entries, classifying by `Classifications`:
- Critical → `MachineHealthy=False` with reason from
  classification and message from alert
- Warning → `MachineHealthy=True` with `HealthyWithWarnings`
- No alerts → `MachineHealthy=True`
- Remediating classification → `NicoFaultRemediation=True`

Falls back to `GetMachine().Health.Alerts` JSONB parsing when
HealthReport API is unavailable. No capability gating — always
tries HealthReport API first.

### ~~3. Close the MHC remediation loop~~ (DONE)

On MHC-triggered deletion (annotation
`machine.openshift.io/unhealthy`), if a physical machine ID is
known, calls `CreateOrUpdateMachineHealthReport` with:
- source=`k8s-mhc`, mode=`replace`
- Alert with id=`mhc-remediation-triggered`,
  classification=`critical`
- Message includes machine name

Also sets `MachineHealthIssue` on `InstanceDeleteRequest` as
belt-and-suspenders fallback. Report failure is non-fatal.

### ~~4. Pre-flight fault check before instance creation~~ (DONE)

In `Create()`, for targeted allocations (`machineId`), checks
critical faults via `GetAllMachineHealthReport` (with JSONB
fallback).
- Critical faults → block creation, record
  `FaultBlockedCreation` event, return error (controller
  requeues)
- Warning-only → allow creation
- Skipped when `AllowUnhealthyMachine` is set
- After `MaxFaultBlockedAttempts` (default 3) consecutive
  blocks, sets `FailureReason=PreFlightHealthCheckFailed`

### ~~5. Error classification and retry logic~~ (DONE)

Errors classified as transient (429, 5xx, network) or terminal
(400). Controller skips requeue for terminal errors. Provisioning
timeout (30min default) sets ErrorReason on Machine. Events
include error classification label.

### ~~6. Wire up Prometheus metrics~~ (DONE)

Metrics renamed to `nico_mapi_*`. All metrics wired:
- `nico_mapi_instance_provision_seconds` — provision duration
  histogram on Ready transition
- `nico_mapi_api_latency_seconds` — API call latency by method
- `nico_mapi_api_errors_total` — API errors by method and
  status code
- `nico_mapi_machines_managed` — gauge of managed machines
- `nico_mapi_machines_unhealthy` — gauge tracking machines with
  `MachineHealthy=False` (increments/decrements on transitions)
- `nico_mapi_health_events_ingested_total` — counter of
  successful `IngestFaultEvent` calls

### ~~7. Rename Carbide references~~ (DONE)

All naming renamed from Carbide to NICo:
- Types: `NicoMachineProviderSpec`, `NicoMachineProviderStatus`,
  `NicoClientInterface`
- Package: `pkg/apis/nicoprovider/`
- API group: `nicoprovider.infrastructure.cluster.x-k8s.io`
- Finalizer: `machine.openshift.io/nico`
- Metrics: `nico_mapi_*`
- Health labels: `nico.io/healthy`
- All manifests, README, OLM bundle updated
- Backward compat: `ParseProviderID()` accepts `nvidia-carbide://`,
  `LegacyMachineFinalizer` removed on delete

### ~~8. Improve test coverage~~ (DONE)

Unit tests cover:
- Error classification (400 terminal, 429/500/503 transient)
- Provisioning timeout enforcement
- Provider ID parsing (both schemes, 3 and 4 segments, invalid)
- Delete error scenarios (500, 202 accepted, nil instance)
- Alert classification (critical, warning, unclassified, mixed)
- HealthReport API path (critical, warning, remediating,
  no alerts)
- JSONB fallback path (critical, warning, remediation)
- MHC remediation with `CreateOrUpdateMachineHealthReport`
  and enriched details
- Pre-flight health check (blocks creation, allows healthy,
  skipped for instanceTypeId, skipped for AllowUnhealthy,
  uses HealthReport API, warning-only allows creation,
  FailureReason after max attempts)
- Health report cleanup on unhealthy→healthy transition
  (deletes k8s-mhc source, not deleted when staying healthy)
- Pre-flight machine validation (blocks on failure,
  skips on 403, allows on pass)
- Machine status history emitted when provisioning stuck
  with machineId assigned

### ~~9. Health report cleanup on recovery~~ (DONE)

When MHC remediation creates a `k8s-mhc` health report via
`CreateOrUpdateMachineHealthReport`, it persists on the machine.
On unhealthy→healthy transition in `updateMachineHealthFromReports`,
calls `DeleteMachineHealthReport(org, machineId, "k8s-mhc")`.
Non-fatal — ignores errors/404. Only in the HealthReport API
path (not JSONB fallback).

### ~~10. Pre-flight machine validation~~ (DONE)

For targeted provisioning (`machineId`), queries
`GetAllMachineValidationRuns` for the most recent validation run.
Blocks creation if the latest run state != "completed" or has
failures (completed < total). Degrades gracefully on 403/404
(skips silently — API requires PROVIDER_ADMIN). No spec field
needed — zero-config alongside health checks.

### ~~11. Machine status history on stuck provisioning~~ (DONE)

When an instance is stuck in Provisioning >5min and has a
physical machineId assigned, fetches `GetMachineStatusHistory`
and emits `MachineStatusHistory` Warning events. Surfaces
physical-layer issues (firmware updates, PXE failures, BMC
resets) that instance status history doesn't show. Non-fatal.

### ~~12. TenantAccount-based capability check~~ (DONE)

Targeted instance creation capability check migrated from the
deprecated `TenantCapabilities.TargetedInstanceCreation` on
`GetCurrentTenant` to the `TenantAccountAPI`. Queries
`GetAllTenantAccount`, finds Ready accounts, checks
`SiteCapabilities` for the target site. Empty `SiteIds`
means default (applies to all sites). Falls back to
`GetCurrentTenant` if TenantAccount API is unavailable.

## Design constraints

- Health features try HealthReport API first, fall back to
  JSONB parsing if API is unavailable
- Follow OpenShift Machine API conventions for conditions and
  events
- Provider ID and finalizer changes handle upgrade from old
  format (accept both on read, write new)
- Maintain the actuator interface contract
  (Create/Update/Exists/Delete)
- All changes must pass `go build ./...` and `go test ./...`
