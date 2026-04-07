# Machine API Provider for NICo

OpenShift Machine API actuator for provisioning bare-metal machines on NICo (NVIDIA NCX Infrastructure Controller) platform.

## Overview

This provider implements the OpenShift Machine API actuator interface for NICo, enabling:

- **Declarative machine provisioning** via `Machine` CRDs
- **Automated scaling** via `MachineSet` controllers
- **Integration with OpenShift cluster lifecycle** and machine management

The provider translates OpenShift Machine API requests into NICo REST API calls (via the NCX Infra Controller REST SDK), managing the full lifecycle of bare-metal instances.

## Architecture

```
+-----------------------------------------------------+
|         OpenShift Machine API Operator              |
|  +---------------+       +------------------+       |
|  |  Machine CRD  |-------|  MachineSet CRD  |       |
|  +-------+-------+       +--------+---------+       |
|          |                        |                 |
+----------+------------------------+-----------------+
           |                        |
           v                        v
+-----------------------------------------------------+
|   Machine API Provider for NICo (this repo)         |
|  +----------------------------------------------+   |
|  |  Machine Reconciler                          |   |
|  |  +----------------------------------------+  |   |
|  |  |  Actuator                              |  |   |
|  |  |  - Create/Update/Delete/Exists         |  |   |
|  |  |  - NicoMachineProviderSpec parser      |  |   |
|  |  +----------+-----------------------------+  |   |
|  +-------------+--------------------------------+   |
+----------------+------------------------------------+
                 |
                 v
+-----------------------------------------------------+
|         NICo REST API Client                        |
|   (github.com/NVIDIA/ncx-infra-controller-rest)    |
+-----------------------------------------------------+
                 |
                 v
+-----------------------------------------------------+
|            NICo Platform                            |
|       (Bare-Metal Infrastructure Management)        |
+-----------------------------------------------------+
```

## Dependencies

- **[github.com/NVIDIA/ncx-infra-controller-rest/sdk/standard](https://github.com/NVIDIA/ncx-infra-controller-rest)** - Auto-generated REST API client (SDK)
- **[github.com/openshift/api](https://github.com/openshift/api)** - OpenShift Machine API types
- **OpenShift 4.14+** or compatible Machine API implementation

### SDK Dependency Note

The NCX Infra Controller REST SDK (`github.com/NVIDIA/ncx-infra-controller-rest/sdk/standard`) does not
yet have tagged releases for its `sdk/standard` sub-module. Until upstream tags the module,
`go.mod` uses a local `replace` directive pointing to a sibling checkout:

```
replace github.com/NVIDIA/ncx-infra-controller-rest/sdk/standard => ../../NVIDIA/ncx-infra-controller-rest/sdk/standard
```

**Why this is necessary:** Go modules require each sub-module (`sdk/standard/` lives
under the parent `ncx-infra-controller-rest` repository) to have its own `go.mod` with
a tagged version. The upstream repository has the `go.mod` on the `chore/sdk-go-module`
branch, which must be cherry-picked into your local checkout until it is merged to main.

**Tracking:** Monitor the upstream repository for a tagged release of the
`sdk/standard` sub-module (e.g., `sdk/standard/v0.1.0`). When available, remove the
`replace` directive and update the `require` entry to the actual version.

## Prerequisites

1. OpenShift cluster (4.14+) or Kubernetes with Machine API CRDs installed
2. NICo API credentials (endpoint, orgName, token)
3. Access to NICo platform with configured Sites, VPCs, and Subnets

## Installation

### Option A: OLM (OpenShift)

Apply the File Based Catalog, then install from OperatorHub:

```bash
kubectl apply -f - <<EOF
apiVersion: operators.coreos.com/v1alpha1
kind: CatalogSource
metadata:
  name: nico-catalog
  namespace: openshift-marketplace
spec:
  sourceType: grpc
  image: ghcr.io/fabiendupont/machine-api-provider-nico-catalog:v0.1.0
  displayName: NICo Machine API Provider
EOF
```

The operator appears in OperatorHub as **Machine API Provider NICo**.

### Option B: Manual (kubectl)

```bash
# Build and push Docker image
make docker-build docker-push IMG=your-registry/machine-api-provider-nico:latest

# Deploy RBAC and controller
make deploy
```

### Create Credentials Secret

```bash
kubectl create secret generic nico-credentials \
  --namespace openshift-machine-api \
  --from-literal=endpoint="https://api.nico.nvidia.com" \
  --from-literal=orgName="your-org-name" \
  --from-literal=token="your-api-token"
```

## Usage

### Create a Machine

```yaml
apiVersion: machine.openshift.io/v1beta1
kind: Machine
metadata:
  name: worker-nico-1
  namespace: openshift-machine-api
  labels:
    machine.openshift.io/cluster-api-cluster: my-cluster
spec:
  providerSpec:
    value:
      apiVersion: nicoprovider.infrastructure.cluster.x-k8s.io/v1beta1
      kind: NicoMachineProviderSpec

      # NICo Site and Tenant
      siteId: "550e8400-e29b-41d4-a716-446655440000"
      tenantId: "660e8400-e29b-41d4-a716-446655440001"

      # Network Configuration
      vpcId: "770e8400-e29b-41d4-a716-446655440002"
      subnetId: "880e8400-e29b-41d4-a716-446655440003"

      # Instance Type (choose one approach)
      instanceTypeId: "990e8400-e29b-41d4-a716-446655440004"  # Generic instance type
      # OR
      # machineId: "aa0e8400-e29b-41d4-a716-446655440005"     # Specific machine

      # Optional: SSH Key Groups
      sshKeyGroupIds:
        - "bb0e8400-e29b-41d4-a716-446655440006"

      # Optional: Labels
      labels:
        environment: production
        role: worker

      # Optional: Cloud-init user data
      userData: |
        #cloud-config
        users:
          - name: core
            ssh_authorized_keys:
              - ssh-rsa AAAAB3NzaC1yc2E...

      # Credentials Secret
      credentialsSecret:
        name: nico-credentials
        namespace: openshift-machine-api
```

### Create a MachineSet for Auto-Scaling

```yaml
apiVersion: machine.openshift.io/v1beta1
kind: MachineSet
metadata:
  name: worker-nico-us-west
  namespace: openshift-machine-api
spec:
  replicas: 3
  selector:
    matchLabels:
      machine.openshift.io/cluster-api-machineset: worker-nico-us-west
  template:
    metadata:
      labels:
        machine.openshift.io/cluster-api-machineset: worker-nico-us-west
    spec:
      providerSpec:
        value:
          apiVersion: nicoprovider.infrastructure.cluster.x-k8s.io/v1beta1
          kind: NicoMachineProviderSpec
          siteId: "550e8400-e29b-41d4-a716-446655440000"
          tenantId: "660e8400-e29b-41d4-a716-446655440001"
          vpcId: "770e8400-e29b-41d4-a716-446655440002"
          subnetId: "880e8400-e29b-41d4-a716-446655440003"
          instanceTypeId: "990e8400-e29b-41d4-a716-446655440004"
          credentialsSecret:
            name: nico-credentials
            namespace: openshift-machine-api
```

### Multi-NIC Configuration

```yaml
spec:
  providerSpec:
    value:
      # ... other fields ...
      subnetId: "primary-subnet-uuid"
      additionalSubnetIds:
        - subnetId: "secondary-subnet-uuid"
          isPhysical: false
        - subnetId: "storage-subnet-uuid"
          isPhysical: true
```

## Provider Spec Reference

### NicoMachineProviderSpec

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `siteId` | string | Yes | NICo Site UUID |
| `tenantId` | string | Yes | NICo Tenant ID |
| `vpcId` | string | Yes | VPC UUID for networking |
| `subnetId` | string | Yes | Primary subnet UUID |
| `instanceTypeId` | string | * | Instance type UUID (mutually exclusive with `machineId`) |
| `machineId` | string | * | Specific machine UUID for targeted provisioning |
| `allowUnhealthyMachine` | bool | No | Allow provisioning on unhealthy machines (requires capability) |
| `additionalSubnetIds` | []AdditionalSubnet | No | Additional network interfaces |
| `userData` | string | No | Cloud-init user data |
| `sshKeyGroupIds` | []string | No | SSH key group UUIDs |
| `labels` | map[string]string | No | Labels to apply to instance |
| `description` | string | No | Description for the NICo instance |
| `operatingSystemId` | string | No | NICo operating system UUID to install |
| `networkSecurityGroupId` | string | No | Network security group UUID to attach |
| `alwaysBootWithCustomIpxe` | bool | No | Always run iPXE script on reboot |
| `infiniBandInterfaces` | []InfiniBandInterfaceSpec | No | InfiniBand partition attachments |
| `nvLinkInterfaces` | []NVLinkInterfaceSpec | No | NVLink logical partition attachments |
| `dpuExtensionServices` | []DpuExtensionServiceSpec | No | DPU Extension Services to deploy after creation |
| `credentialsSecret` | CredentialsSecretReference | Yes | Secret containing API credentials |

\* Must specify exactly one of `instanceTypeId` or `machineId`

### NicoMachineProviderStatus

| Field | Type | Description |
|-------|------|-------------|
| `instanceId` | string | NICo instance UUID |
| `machineId` | string | Physical machine ID |
| `instanceState` | string | Instance lifecycle state (Pending, Provisioning, Configuring, Ready, etc.) |
| `addresses` | []MachineAddress | IP addresses assigned to the machine |
| `conditions` | []Condition | Instance lifecycle conditions (InstanceReady, MachineHealthy, etc.) |
| `healthLabels` | map[string]string | Health labels matching the CCM (nico.io/healthy) |

## Development

### Building

```bash
make build          # Build binary
make test           # Run tests
make docker-build   # Build Docker image
make run            # Run locally (requires kubeconfig)
```

### Release Artifacts

```bash
# OLM bundle image
make bundle-build bundle-push

# FBC catalog image
make catalog-build catalog-push
```

### Project Structure

```
machine-api-provider-nico/
├── cmd/manager/          # Controller manager entry point
├── pkg/
│   ├── apis/             # NicoMachineProviderSpec types
│   ├── actuators/        # Machine actuator implementation
│   ├── providerid/       # Provider ID parsing and formatting
│   └── controllers/      # Machine and MachineSet reconcilers
├── config/               # Deployment manifests
│   ├── rbac/             # RBAC permissions
│   ├── manager/          # Controller deployment
│   └── samples/          # Example Machine CRs
├── bundle/               # OLM bundle (CSV)
├── catalog/              # File Based Catalog for OLM
└── Dockerfile            # Container build
```

## Troubleshooting

### Machine stuck in "Provisioning" state

Check the controller logs:

```bash
kubectl logs -n openshift-machine-api \
  -l app=machine-api-provider-nico \
  --tail=100 -f
```

Common issues:
- Invalid credentials in secret
- Incorrect site/tenant/VPC/subnet UUIDs
- Network connectivity to NICo API
- Instance type not available in site

### Instance created but not joining cluster

1. Verify user data is correctly formatted
2. Check instance has network connectivity
3. Verify SSH keys are configured
4. Check OpenShift ignition/bootstrap process

### Permission errors

Ensure the service account has proper RBAC:

```bash
kubectl auth can-i get machines.machine.openshift.io \
  --as=system:serviceaccount:openshift-machine-api:machine-api-provider-nico
```

## License

Apache 2.0

## Related Projects

- [cloud-provider-nvidia-ncx-infra-controller](../cloud-provider-nvidia-ncx-infra-controller) - Cloud Controller Manager for NICo
- [ncx-infra-controller-rest](https://github.com/NVIDIA/ncx-infra-controller-rest) - NICo REST API and SDK

## Contributing

Contributions are welcome! Please submit issues and pull requests to the GitHub repository.
