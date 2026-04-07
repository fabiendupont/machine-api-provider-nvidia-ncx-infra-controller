/*
Copyright 2026 Fabien Dupont.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package v1beta1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// NicoMachineProviderSpec defines the desired state for OpenShift Machine API
type NicoMachineProviderSpec struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// SiteID is the NICo Site UUID
	// +required
	SiteID string `json:"siteId"`

	// TenantID is the NICo tenant ID
	// +required
	TenantID string `json:"tenantId"`

	// InstanceTypeID specifies the NICo instance type UUID
	// Mutually exclusive with MachineID
	// +optional
	InstanceTypeID string `json:"instanceTypeId,omitempty"`

	// MachineID specifies a specific machine UUID for targeted provisioning
	// Mutually exclusive with InstanceTypeID
	// +optional
	MachineID string `json:"machineId,omitempty"`

	// AllowUnhealthyMachine allows provisioning on an unhealthy machine
	// +optional
	AllowUnhealthyMachine bool `json:"allowUnhealthyMachine,omitempty"`

	// VpcID is the VPC UUID
	// +required
	VpcID string `json:"vpcId"`

	// SubnetID is the primary subnet UUID
	// +required
	SubnetID string `json:"subnetId"`

	// AdditionalSubnetIDs for multi-NIC configurations
	// +optional
	AdditionalSubnetIDs []AdditionalSubnet `json:"additionalSubnetIds,omitempty"`

	// OperatingSystemID is the NICo operating system to install.
	// If empty, a minimal iPXE script is used as fallback.
	// +optional
	OperatingSystemID string `json:"operatingSystemId,omitempty"`

	// InfiniBandInterfaces specifies InfiniBand partition attachments for HPC networking.
	// +optional
	InfiniBandInterfaces []InfiniBandInterfaceSpec `json:"infiniBandInterfaces,omitempty"`

	// NVLinkInterfaces specifies NVLink logical partition attachments for GPU communication.
	// +optional
	NVLinkInterfaces []NVLinkInterfaceSpec `json:"nvLinkInterfaces,omitempty"`

	// NetworkSecurityGroupID attaches a network security group to the instance.
	// +optional
	NetworkSecurityGroupID string `json:"networkSecurityGroupId,omitempty"`

	// UserData contains the cloud-init user data
	// +optional
	UserData string `json:"userData,omitempty"`

	// SSHKeyGroupIDs contains SSH key group IDs
	// +optional
	SSHKeyGroupIDs []string `json:"sshKeyGroupIds,omitempty"`

	// Labels to apply to the NICo instance
	// +optional
	Labels map[string]string `json:"labels,omitempty"`

	// Description for the NICo instance
	// +optional
	Description string `json:"description,omitempty"`

	// AlwaysBootWithCustomIpxe when true, the iPXE script will always run on reboot.
	// Requires the OS to be of iPXE type.
	// +optional
	AlwaysBootWithCustomIpxe bool `json:"alwaysBootWithCustomIpxe,omitempty"`

	// DpuExtensionServices specifies DPU Extension Services to deploy after instance creation.
	// These are deployed via an UpdateInstance call with DpuExtensionServiceDeployments.
	// +optional
	DpuExtensionServices []DpuExtensionServiceSpec `json:"dpuExtensionServices,omitempty"`

	// CredentialsSecret references a secret containing NICo API credentials
	// The secret must contain: endpoint, orgName, token
	// +required
	CredentialsSecret CredentialsSecretReference `json:"credentialsSecret"`
}

// AdditionalSubnet defines an additional network interface
type AdditionalSubnet struct {
	// SubnetID is the subnet UUID for this interface
	// +required
	SubnetID string `json:"subnetId"`

	// IsPhysical indicates if this is a physical interface
	// +optional
	IsPhysical bool `json:"isPhysical,omitempty"`
}

// CredentialsSecretReference contains information to locate the secret
type CredentialsSecretReference struct {
	// Name of the secret
	// +required
	Name string `json:"name"`

	// Namespace of the secret
	// +required
	Namespace string `json:"namespace"`
}

// NicoMachineProviderStatus defines the observed state for OpenShift Machine API
type NicoMachineProviderStatus struct {
	metav1.TypeMeta `json:",inline"`

	// InstanceID is the NICo instance ID
	// +optional
	InstanceID *string `json:"instanceId,omitempty"`

	// MachineID is the physical machine ID
	// +optional
	MachineID *string `json:"machineId,omitempty"`

	// InstanceState represents the current state of the instance
	// +optional
	InstanceState *string `json:"instanceState,omitempty"`

	// Addresses contains the IP addresses assigned to the machine
	// +optional
	Addresses []MachineAddress `json:"addresses,omitempty"`

	// Conditions represent the current state
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// HealthLabels contains health-related labels matching the CCM.
	// Keys: nico.io/healthy, nico.io/health-alert-count
	// +optional
	HealthLabels map[string]string `json:"healthLabels,omitempty"`
}

// MachineAddress contains information for a machine's network address
type MachineAddress struct {
	// Type of the address (e.g., InternalIP, ExternalIP)
	// +required
	Type string `json:"type"`

	// Address is the IP address
	// +required
	Address string `json:"address"`
}

// InfiniBandInterfaceSpec defines an InfiniBand interface attachment
type InfiniBandInterfaceSpec struct {
	// PartitionID is the InfiniBand partition UUID
	// +optional
	PartitionID string `json:"partitionId,omitempty"`

	// Device is the device name
	// +optional
	Device string `json:"device,omitempty"`

	// IsPhysical indicates if this is a physical interface
	// +optional
	IsPhysical bool `json:"isPhysical,omitempty"`

	// DeviceInstance is the device index
	// +optional
	DeviceInstance *int32 `json:"deviceInstance,omitempty"`
}

// DpuExtensionServiceSpec defines a DPU Extension Service to deploy on the instance
type DpuExtensionServiceSpec struct {
	// ServiceID is the UUID of the DPU Extension Service to deploy
	// +required
	ServiceID string `json:"serviceId"`

	// Version is the version of the DPU Extension Service to deploy
	// +optional
	Version string `json:"version,omitempty"`
}

// NVLinkInterfaceSpec defines an NVLink interface attachment
type NVLinkInterfaceSpec struct {
	// NVLinkLogicalPartitionID is the NVLink logical partition UUID
	// +optional
	NVLinkLogicalPartitionID string `json:"nvLinkLogicalPartitionId,omitempty"`

	// DeviceInstance is the device index
	// +optional
	DeviceInstance *int32 `json:"deviceInstance,omitempty"`
}
