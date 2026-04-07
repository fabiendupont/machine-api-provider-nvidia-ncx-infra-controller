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
go test ./test/integration/ -v
# E2E tests (require live NICo API)
NVIDIA_CARBIDE_API_ENDPOINT=https://... go test ./test/e2e/ -v
```

## Key files

- `pkg/apis/nicoprovider/v1beta1/types.go` — provider
  spec and status types
- `pkg/actuators/machine/actuator.go` — Create/Update/Exists/Delete
  (889 LOC, main logic)
- `pkg/controllers/machine/controller.go` — reconciler loop
- `pkg/providerid/providerid.go` — provider ID parsing
- `pkg/metrics/metrics.go` — Prometheus metrics (partially wired)
- `pkg/apis/nicoprovider/v1beta1/webhook.go` — admission
  validation
- `test/` — unit, integration, e2e tests

## SDK

Uses `github.com/NVIDIA/ncx-infra-controller-rest v1.2.0`.
**Known issue:** SDK sub-module lacks tagged releases. go.mod
uses a local `replace` directive:
```
replace github.com/NVIDIA/ncx-infra-controller-rest/sdk/standard => ../../NVIDIA/ncx-infra-controller-rest/sdk/standard
```
This requires a local checkout of the NICo REST repo until
upstream tags `sdk/standard/v0.1.0`.

## Current status

v0.1.0, alpha. Basic happy-path test coverage. MHC integration
added recently. Actuator reads machine health alerts and sets
`MachineHealthy` condition.

---

## Work to do

The following changes align this MAPI provider with the NCP
reference architecture vision. Key reference documents:

- NEP-0007: `~/Code/github.com/NVIDIA/ncx-infra-controller-rest/docs/enhancements/0007-fault-management-provider.md`
- NEP-0001: `~/Code/github.com/NVIDIA/ncx-infra-controller-rest/docs/enhancements/0001-extensible-architecture.md`

### ~~1. Unify provider ID scheme~~ (DONE)

Provider ID scheme changed from `nvidia-carbide://` to `nico://`.
`ParseProviderID()` accepts both prefixes on read.
Finalizer changed to `machine.openshift.io/nico` with legacy
finalizer removal on delete.

### 2. Replace JSONB health parsing with structured fault API

**Current:** The actuator's `Update()` method calls
`GetMachine()`, parses `machine.Health` (JSONB) for alerts, and
sets a `MachineHealthy` condition.

**Target:** Replace with a call to the structured health events
API:
`GET /v2/org/{org}/carbide/health/events?machine_id={id}&state=open`

Changes in `pkg/actuators/machine/actuator.go`:
1. Add a method `getHealthEvents(ctx, machineID)` that calls
   the health events endpoint
2. In `Update()`, replace the `GetMachine()` health parsing with
   `getHealthEvents()`
3. Map fault_event fields to the `MachineHealthy` condition:
   - Any open critical fault → `MachineHealthy=False` with
     reason from `classification` and message from `message`
   - Open warning faults → `MachineHealthy=True` with warning
     event recorded
   - No faults → `MachineHealthy=True`
4. Add a `NicoFaultRemediation` condition when fault state is
   `remediating`:
   ```go
   Condition{
       Type:    "NicoFaultRemediation",
       Status:  corev1.ConditionTrue,
       Reason:  "RemediationInProgress",
       Message: "Automated GPU reset in progress",
   }
   ```

Fall back to current JSONB parsing if the health events endpoint
is not available. Check `/v2/org/{org}/carbide/capabilities` for
`fault-management` feature.

### 3. Close the MHC remediation loop

**Current:** When MHC sets the `machine.openshift.io/unhealthy`
annotation, the actuator detects it in `Delete()` and logs a
message. One-way flow.

**Target:** When the actuator detects the unhealthy annotation:

1. POST to `POST /v2/org/{org}/carbide/health/events/ingest`:
   ```json
   {
     "source": "k8s-mhc",
     "severity": "critical",
     "component": "node",
     "classification": "mhc-remediation-triggered",
     "message": "MachineHealthCheck triggered remediation for machine {name}",
     "machine_id": "{machineID from provider ID}",
     "detected_at": "{timestamp}",
     "metadata": {
       "machine_name": "{machine.Name}",
       "namespace": "{machine.Namespace}",
       "mhc_annotation": "machine.openshift.io/unhealthy"
     }
   }
   ```
2. This creates a fault_event in NICo, which triggers
   NEP-0007's remediation workflow
3. NICo's remediation may resolve the issue before the MAPI
   controller deletes the machine — in that case, the fault
   resolves and MHC should clear the annotation

This closes the feedback loop: K8s health checks push back to
NICo's health ingestion endpoint, and NICo's remediation workflow
handles the full lifecycle.

Guard behind capability check. If `fault-management` is not
available, continue with current behavior (delete the machine).

### 4. Pre-flight fault check before instance creation

**Current:** The actuator creates instances without checking
hardware health.

**Target:** In `Create()`, before calling `CreateInstance()`:

1. If the spec uses `machineId` (targeted allocation), query
   `GET /health/events?machine_id={id}&state=open&severity=critical`
2. If open critical faults exist:
   - Record a warning event explaining the fault
   - Set `MachineHealthy=False` condition with fault details
   - Return `RequeueAfter: 2 * time.Minute` (give remediation
     time to complete)
   - Do NOT call `CreateInstance()`
3. After 3 requeue cycles with persistent faults, set
   `FailureReason` and `FailureMessage` to surface the issue

### ~~5. Error classification and retry logic~~ (DONE)

Errors classified as transient (429, 5xx, network) or terminal
(400). Controller skips requeue for terminal errors. Provisioning
timeout (30min default) sets ErrorReason on Machine. Events
include error classification label.

### ~~6. Wire up Prometheus metrics~~ (DONE)

Metrics renamed to `nico_mapi_*`. Provision duration histogram
wired on Ready transition. API latency histogram added for
CreateInstance, GetInstance, DeleteInstance.

**Deferred** (pending health events API):
- `nico_mapi_machines_unhealthy` gauge
- `nico_mapi_health_events_ingested_total` counter

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

Added tests for:
- Error classification (400 terminal, 429/500/503 transient)
- Provisioning timeout enforcement
- Provider ID parsing (both schemes, 3 and 4 segments, invalid)
- Delete error scenarios (500, 202 accepted, nil instance)

**Deferred** (pending health events API):
- Health event query, MHC remediation loop, fault-blocked creation

## Design constraints

- All new health features must be guarded behind capability
  checks (`fault-management` feature)
- Graceful degradation: fall back to current JSONB parsing if
  health events API unavailable
- Follow OpenShift Machine API conventions for conditions and
  events
- Provider ID and finalizer changes must handle upgrade from
  old format (accept both on read, write new)
- Maintain the actuator interface contract
  (Create/Update/Exists/Delete)
- All changes must pass `go build ./...` and `go test ./...`
