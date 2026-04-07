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

package machine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"
	machinev1beta1 "github.com/openshift/api/machine/v1beta1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"

	v1beta1 "github.com/fabiendupont/machine-api-provider-nvidia-ncx-infra-controller/pkg/apis/nicoprovider/v1beta1"
	nicometrics "github.com/fabiendupont/machine-api-provider-nvidia-ncx-infra-controller/pkg/metrics"
	"github.com/fabiendupont/machine-api-provider-nvidia-ncx-infra-controller/pkg/providerid"
	nico "github.com/NVIDIA/ncx-infra-controller-rest/sdk/standard"
)

// NicoClientInterface defines the methods needed from NICo REST client
type NicoClientInterface interface {
	CreateInstance(
		ctx context.Context, org string, req nico.InstanceCreateRequest,
	) (*nico.Instance, *http.Response, error)
	GetInstance(
		ctx context.Context, org string, instanceId string,
	) (*nico.Instance, *http.Response, error)
	DeleteInstance(
		ctx context.Context, org string, instanceId string,
		deleteReq *nico.InstanceDeleteRequest,
	) (*http.Response, error)
	UpdateInstance(
		ctx context.Context, org string, instanceId string,
		req nico.InstanceUpdateRequest,
	) (*nico.Instance, *http.Response, error)
	GetMachine(
		ctx context.Context, org string, machineId string,
	) (*nico.Machine, *http.Response, error)
	GetCurrentTenant(
		ctx context.Context, org string,
	) (*nico.Tenant, *http.Response, error)
	GetInstanceStatusHistory(
		ctx context.Context, org string, instanceId string,
	) ([]nico.StatusDetail, *http.Response, error)
}

// nicoClient wraps the SDK APIClient and injects auth context
type nicoClient struct {
	client *nico.APIClient
	token  string
}

func (c *nicoClient) authCtx(ctx context.Context) context.Context {
	return context.WithValue(ctx, nico.ContextAccessToken, c.token)
}

func (c *nicoClient) CreateInstance(
	ctx context.Context, org string, req nico.InstanceCreateRequest,
) (*nico.Instance, *http.Response, error) {
	return c.client.InstanceAPI.CreateInstance(c.authCtx(ctx), org).InstanceCreateRequest(req).Execute()
}

func (c *nicoClient) GetInstance(
	ctx context.Context, org, instanceId string,
) (*nico.Instance, *http.Response, error) {
	return c.client.InstanceAPI.GetInstance(c.authCtx(ctx), org, instanceId).Execute()
}

func (c *nicoClient) DeleteInstance(
	ctx context.Context, org, instanceId string,
	deleteReq *nico.InstanceDeleteRequest,
) (*http.Response, error) {
	r := c.client.InstanceAPI.DeleteInstance(c.authCtx(ctx), org, instanceId)
	if deleteReq != nil {
		r = r.InstanceDeleteRequest(*deleteReq)
	}
	return r.Execute()
}

func (c *nicoClient) UpdateInstance(
	ctx context.Context, org, instanceId string,
	req nico.InstanceUpdateRequest,
) (*nico.Instance, *http.Response, error) {
	return c.client.InstanceAPI.UpdateInstance(
		c.authCtx(ctx), org, instanceId,
	).InstanceUpdateRequest(req).Execute()
}

func (c *nicoClient) GetMachine(
	ctx context.Context, org, machineId string,
) (*nico.Machine, *http.Response, error) {
	return c.client.MachineAPI.GetMachine(c.authCtx(ctx), org, machineId).Execute()
}

func (c *nicoClient) GetCurrentTenant(
	ctx context.Context, org string,
) (*nico.Tenant, *http.Response, error) {
	return c.client.TenantAPI.GetCurrentTenant(c.authCtx(ctx), org).Execute()
}

func (c *nicoClient) GetInstanceStatusHistory(
	ctx context.Context, org, instanceId string,
) ([]nico.StatusDetail, *http.Response, error) {
	return c.client.InstanceAPI.GetInstanceStatusHistory(c.authCtx(ctx), org, instanceId).Execute()
}

const statusCodeUnknown = "unknown"

// APIErrorKind classifies NICo API errors for retry decisions.
type APIErrorKind int

const (
	// ErrorTransient indicates a retryable error (network, 429, 5xx).
	ErrorTransient APIErrorKind = iota
	// ErrorTerminal indicates a non-retryable error (400 bad request).
	ErrorTerminal
)

// ClassifiedError wraps an error with a classification for retry logic.
type ClassifiedError struct {
	Kind    APIErrorKind
	wrapped error
}

func (e *ClassifiedError) Error() string { return e.wrapped.Error() }
func (e *ClassifiedError) Unwrap() error { return e.wrapped }

// classifyHTTPError wraps an error with transient/terminal classification
// based on the HTTP status code.
func classifyHTTPError(httpResp *http.Response, err error) error {
	if httpResp == nil {
		return &ClassifiedError{Kind: ErrorTransient, wrapped: err}
	}
	switch {
	case httpResp.StatusCode == 400:
		return &ClassifiedError{Kind: ErrorTerminal, wrapped: err}
	default:
		return &ClassifiedError{Kind: ErrorTransient, wrapped: err}
	}
}

// Actuator implements the OpenShift Machine actuator interface
type Actuator struct {
	client        client.Client
	eventRecorder record.EventRecorder
	// For testing
	nicoAPIClient NicoClientInterface
	orgName             string
}

// NewActuator creates a new machine actuator
func NewActuator(k8sClient client.Client, eventRecorder record.EventRecorder) *Actuator {
	return &Actuator{
		client:        k8sClient,
		eventRecorder: eventRecorder,
	}
}

// NewActuatorWithClient creates a new machine actuator with injected client (for testing)
func NewActuatorWithClient(
	k8sClient client.Client, eventRecorder record.EventRecorder,
	nicoAPIClient NicoClientInterface, orgName string,
) *Actuator {
	return &Actuator{
		client:              k8sClient,
		eventRecorder:       eventRecorder,
		nicoAPIClient: nicoAPIClient,
		orgName:             orgName,
	}
}

// validateProviderSpec validates the provider spec fields before making API calls.
func validateProviderSpec(spec *v1beta1.NicoMachineProviderSpec) error {
	if spec.InstanceTypeID != "" && spec.MachineID != "" {
		return fmt.Errorf("specify either instanceTypeId or machineId, not both")
	}
	if spec.InstanceTypeID == "" && spec.MachineID == "" {
		return fmt.Errorf("either instanceTypeId or machineId is required")
	}
	if spec.SiteID == "" {
		return fmt.Errorf("siteId is required")
	}
	if spec.TenantID == "" {
		return fmt.Errorf("tenantId is required")
	}
	if spec.VpcID == "" {
		return fmt.Errorf("vpcId is required")
	}
	if spec.SubnetID == "" {
		return fmt.Errorf("subnetId is required")
	}
	if len(spec.AdditionalSubnetIDs) > 10 {
		return fmt.Errorf("too many additional subnets (max 10, got %d)",
			len(spec.AdditionalSubnetIDs))
	}
	return nil
}

// buildInstanceRequest constructs the API request body from a provider spec.
func buildInstanceRequest(
	name string,
	providerSpec *v1beta1.NicoMachineProviderSpec,
) nico.InstanceCreateRequest {
	interfaces := []nico.InterfaceCreateRequest{
		{
			SubnetId:   &providerSpec.SubnetID,
			IsPhysical: ptr(false),
		},
	}

	for _, additionalSubnet := range providerSpec.AdditionalSubnetIDs {
		subnetID := additionalSubnet.SubnetID
		interfaces = append(interfaces, nico.InterfaceCreateRequest{
			SubnetId:   &subnetID,
			IsPhysical: ptr(additionalSubnet.IsPhysical),
		})
	}

	req := nico.InstanceCreateRequest{
		Name:             name,
		TenantId:         providerSpec.TenantID,
		VpcId:            providerSpec.VpcID,
		Interfaces:       interfaces,
		PhoneHomeEnabled: ptr(true),
	}

	if providerSpec.InstanceTypeID != "" {
		req.InstanceTypeId = &providerSpec.InstanceTypeID
	}
	if providerSpec.MachineID != "" {
		req.MachineId = &providerSpec.MachineID
	}
	if providerSpec.AllowUnhealthyMachine {
		req.AllowUnhealthyMachine = ptr(true)
	}
	if providerSpec.UserData != "" {
		userData := providerSpec.UserData
		req.UserData = *nico.NewNullableString(&userData)
	}
	// The API requires either ipxeScript or operatingSystemId.
	if providerSpec.OperatingSystemID != "" {
		req.OperatingSystemId = *nico.NewNullableString(&providerSpec.OperatingSystemID)
	} else {
		ipxeScript := "#!ipxe\necho Booting via NICo"
		req.IpxeScript = *nico.NewNullableString(&ipxeScript)
	}
	if len(providerSpec.SSHKeyGroupIDs) > 0 {
		req.SshKeyGroupIds = providerSpec.SSHKeyGroupIDs
	}
	if len(providerSpec.Labels) > 0 {
		req.Labels = providerSpec.Labels
	}
	if providerSpec.NetworkSecurityGroupID != "" {
		req.NetworkSecurityGroupId = *nico.NewNullableString(&providerSpec.NetworkSecurityGroupID)
	}
	if providerSpec.Description != "" {
		desc := providerSpec.Description
		req.Description = *nico.NewNullableString(&desc)
	}
	if providerSpec.AlwaysBootWithCustomIpxe {
		req.AlwaysBootWithCustomIpxe = ptr(true)
	}
	if len(providerSpec.InfiniBandInterfaces) > 0 {
		ibInterfaces := make([]nico.InfiniBandInterfaceCreateRequest, 0, len(providerSpec.InfiniBandInterfaces))
		for _, ib := range providerSpec.InfiniBandInterfaces {
			ibReq := nico.InfiniBandInterfaceCreateRequest{}
			if ib.PartitionID != "" {
				ibReq.PartitionId = &ib.PartitionID
			}
			if ib.Device != "" {
				ibReq.Device = &ib.Device
			}
			if ib.IsPhysical {
				ibReq.IsPhysical = ptr(true)
			}
			if ib.DeviceInstance != nil {
				ibReq.DeviceInstance = ib.DeviceInstance
			}
			ibInterfaces = append(ibInterfaces, ibReq)
		}
		req.InfinibandInterfaces = ibInterfaces
	}
	if len(providerSpec.NVLinkInterfaces) > 0 {
		nvlInterfaces := make([]nico.NVLinkInterfaceCreateRequest, 0, len(providerSpec.NVLinkInterfaces))
		for _, nvl := range providerSpec.NVLinkInterfaces {
			nvlReq := nico.NVLinkInterfaceCreateRequest{}
			if nvl.NVLinkLogicalPartitionID != "" {
				nvlReq.NvLinklogicalPartitionId = &nvl.NVLinkLogicalPartitionID
			}
			if nvl.DeviceInstance != nil {
				nvlReq.DeviceInstance = nvl.DeviceInstance
			}
			nvlInterfaces = append(nvlInterfaces, nvlReq)
		}
		req.NvLinkInterfaces = nvlInterfaces
	}

	return req
}

// Create provisions a new instance
func (a *Actuator) Create(ctx context.Context, machine runtime.Object) error {
	machineObj, ok := machine.(client.Object)
	if !ok {
		return fmt.Errorf("machine is not a client.Object")
	}

	// Parse provider spec
	providerSpec, err := a.getProviderSpec(machineObj)
	if err != nil {
		return fmt.Errorf("failed to get provider spec: %w", err)
	}

	// Validate provider spec
	if err := validateProviderSpec(providerSpec); err != nil {
		if a.eventRecorder != nil {
			a.eventRecorder.Eventf(machineObj, corev1.EventTypeWarning, "InvalidSpec", "Invalid provider spec: %v", err)
		}
		return fmt.Errorf("invalid provider spec: %w", err)
	}

	// Get NICo client and orgName
	nicoAPIClient, orgName, err := a.getNicoClient(ctx, providerSpec)
	if err != nil {
		return fmt.Errorf("failed to create NICo client: %w", err)
	}

	// Validate tenant capabilities for targeted provisioning
	if providerSpec.MachineID != "" {
		tenant, _, tenantErr := nicoAPIClient.GetCurrentTenant(ctx, orgName)
		if tenantErr == nil && tenant != nil && tenant.Capabilities != nil {
			if tenant.Capabilities.TargetedInstanceCreation != nil && !*tenant.Capabilities.TargetedInstanceCreation {
				return fmt.Errorf("tenant does not have targeted instance creation enabled; cannot use machineId")
			}
		}
	}

	// Build instance request
	instanceReq := buildInstanceRequest(machineObj.GetName(), providerSpec)

	// Create instance
	createStart := time.Now()
	instance, httpResp, err := nicoAPIClient.CreateInstance(
		ctx, orgName, instanceReq,
	)
	nicometrics.APILatency.WithLabelValues("CreateInstance").Observe(time.Since(createStart).Seconds())
	if err != nil {
		return a.handleCreateError(machineObj, httpResp, err)
	}
	if instance == nil {
		if a.eventRecorder != nil {
			a.eventRecorder.Eventf(machineObj,
				corev1.EventTypeWarning, "FailedCreate",
				"Create instance returned no data")
		}
		return fmt.Errorf(
			"create instance returned no data, status code: %d",
			httpResp.StatusCode,
		)
	}

	// Build provider status
	providerStatus := &v1beta1.NicoMachineProviderStatus{
		InstanceID: instance.Id,
	}

	if instance.MachineId.Get() != nil {
		providerStatus.MachineID = instance.MachineId.Get()
	}
	if instance.Status != nil {
		status := string(*instance.Status)
		providerStatus.InstanceState = &status
	}

	// Extract addresses
	for _, iface := range instance.Interfaces {
		for _, ipAddr := range iface.IpAddresses {
			providerStatus.Addresses = append(providerStatus.Addresses, v1beta1.MachineAddress{
				Type:    "InternalIP",
				Address: ipAddr,
			})
		}
	}

	meta.SetStatusCondition(&providerStatus.Conditions, metav1.Condition{
		Type:    "InstanceProvisioned",
		Status:  metav1.ConditionTrue,
		Reason:  "InstanceCreated",
		Message: fmt.Sprintf("Instance %s created successfully", *instance.Id),
	})

	if err := a.setProviderStatus(ctx, machineObj, providerStatus); err != nil {
		return fmt.Errorf("failed to update provider status: %w", err)
	}

	// Set provider ID using the local providerid package
	instanceUUID, err := uuid.Parse(*instance.Id)
	if err != nil {
		return fmt.Errorf("failed to parse instance ID from response: %w", err)
	}
	pid := providerid.NewProviderID(orgName, providerSpec.TenantID, providerSpec.SiteID, instanceUUID)
	if err := a.setProviderID(ctx, machineObj, pid.String()); err != nil {
		return fmt.Errorf("failed to set provider ID: %w", err)
	}

	// Deploy DPU extension services if specified
	if len(providerSpec.DpuExtensionServices) > 0 {
		if err := a.deployDpuExtensionServices(
			ctx, nicoAPIClient, orgName,
			machineObj, *instance.Id, providerSpec,
		); err != nil {
			return err
		}
	}

	nicometrics.MachinesManaged.Inc()
	if a.eventRecorder != nil {
		a.eventRecorder.Eventf(machineObj, corev1.EventTypeNormal, "Created", "Created instance %s", *instance.Id)
	}
	return nil
}

// Update updates an existing instance
func (a *Actuator) Update(ctx context.Context, machine runtime.Object) error {
	machineObj, ok := machine.(client.Object)
	if !ok {
		return fmt.Errorf("machine is not a client.Object")
	}

	// Parse provider spec
	providerSpec, err := a.getProviderSpec(machineObj)
	if err != nil {
		return fmt.Errorf("failed to get provider spec: %w", err)
	}

	// Get provider status
	providerStatus, err := a.getProviderStatus(machineObj)
	if err != nil {
		return fmt.Errorf("failed to get provider status: %w", err)
	}

	if providerStatus.InstanceID == nil {
		return fmt.Errorf("instance ID not set in provider status")
	}

	// Get NICo client and orgName
	nicoAPIClient, orgName, err := a.getNicoClient(ctx, providerSpec)
	if err != nil {
		return fmt.Errorf("failed to create NICo client: %w", err)
	}

	// Get current instance status
	getStart := time.Now()
	instance, httpResp, err := nicoAPIClient.GetInstance(ctx, orgName, *providerStatus.InstanceID)
	nicometrics.APILatency.WithLabelValues("GetInstance").Observe(time.Since(getStart).Seconds())
	if err != nil {
		return fmt.Errorf("failed to get instance: %w", err)
	}

	if instance == nil {
		return fmt.Errorf("get instance returned no data, status code: %d", httpResp.StatusCode)
	}

	// Update provider status with state machine tracking
	if instance.Status != nil {
		status := string(*instance.Status)
		providerStatus.InstanceState = &status
		setInstanceStateConditions(providerStatus, *instance.Status)

		// Record provision duration when instance reaches Ready
		if *instance.Status == nico.INSTANCESTATUS_READY {
			for _, c := range providerStatus.Conditions {
				if c.Type == "InstanceProvisioned" && !c.LastTransitionTime.IsZero() {
					nicometrics.InstanceProvisionDuration.WithLabelValues("").Observe(
						time.Since(c.LastTransitionTime.Time).Seconds())
					break
				}
			}
		}
	}
	if instance.MachineId.Get() != nil {
		providerStatus.MachineID = instance.MachineId.Get()
	}

	// Check machine health if a physical machine is assigned
	a.updateMachineHealth(ctx, nicoAPIClient, orgName, instance, providerStatus)

	// Record status history as events when instance is stuck or in error
	a.checkStatusHistory(ctx, nicoAPIClient, orgName, machineObj, instance)

	// Check provisioning timeout
	a.checkProvisioningTimeout(machineObj, instance, providerStatus)

	// Update addresses
	providerStatus.Addresses = []v1beta1.MachineAddress{}
	for _, iface := range instance.Interfaces {
		for _, ipAddr := range iface.IpAddresses {
			providerStatus.Addresses = append(providerStatus.Addresses, v1beta1.MachineAddress{
				Type:    "InternalIP",
				Address: ipAddr,
			})
		}
	}

	if err := a.setProviderStatus(ctx, machineObj, providerStatus); err != nil {
		return fmt.Errorf("failed to update provider status: %w", err)
	}

	return nil
}

// Exists checks if instance exists
func (a *Actuator) Exists(ctx context.Context, machine runtime.Object) (bool, error) {
	machineObj, ok := machine.(client.Object)
	if !ok {
		return false, fmt.Errorf("machine is not a client.Object")
	}

	// Get provider status
	providerStatus, err := a.getProviderStatus(machineObj)
	if err != nil {
		return false, fmt.Errorf("failed to get provider status: %w", err)
	}

	if providerStatus.InstanceID == nil {
		return false, nil
	}

	// Parse provider spec
	providerSpec, err := a.getProviderSpec(machineObj)
	if err != nil {
		return false, fmt.Errorf("failed to get provider spec: %w", err)
	}

	// Get NICo client and orgName
	nicoAPIClient, orgName, err := a.getNicoClient(ctx, providerSpec)
	if err != nil {
		return false, fmt.Errorf("failed to create NICo client: %w", err)
	}

	// Check if instance exists
	instance, httpResp, err := nicoAPIClient.GetInstance(ctx, orgName, *providerStatus.InstanceID)
	if err != nil {
		if httpResp != nil && httpResp.StatusCode == http.StatusNotFound {
			return false, nil
		}
		return false, fmt.Errorf("failed to check instance existence: %w", err)
	}

	// Instance exists if we get a non-nil instance
	return instance != nil, nil
}

// Delete deprovisions the instance
func (a *Actuator) Delete(ctx context.Context, machine runtime.Object) error {
	machineObj, ok := machine.(client.Object)
	if !ok {
		return fmt.Errorf("machine is not a client.Object")
	}

	// Parse provider spec
	providerSpec, err := a.getProviderSpec(machineObj)
	if err != nil {
		return fmt.Errorf("failed to get provider spec: %w", err)
	}

	// Get provider status
	providerStatus, err := a.getProviderStatus(machineObj)
	if err != nil {
		return fmt.Errorf("failed to get provider status: %w", err)
	}

	if providerStatus.InstanceID == nil {
		// Nothing to delete
		return nil
	}

	// Get NICo client and orgName
	nicoAPIClient, orgName, err := a.getNicoClient(ctx, providerSpec)
	if err != nil {
		return fmt.Errorf("failed to create NICo client: %w", err)
	}

	// Check if this deletion is triggered by MachineHealthCheck remediation
	var deleteReq *nico.InstanceDeleteRequest
	if isMHCRemediation(machineObj) {
		deleteReq = &nico.InstanceDeleteRequest{
			MachineHealthIssue: &nico.MachineHealthIssue{
				Category: ptr("MachineHealthCheck"),
				Summary:  ptr("Node marked unhealthy by OpenShift MachineHealthCheck"),
			},
		}
		if a.eventRecorder != nil {
			a.eventRecorder.Eventf(machineObj, corev1.EventTypeNormal, "MHCRemediation",
				"Reporting machine health issue to NICo for break-fix workflow")
		}
	}

	// Delete instance
	deleteStart := time.Now()
	httpResp, err := nicoAPIClient.DeleteInstance(ctx, orgName, *providerStatus.InstanceID, deleteReq)
	nicometrics.APILatency.WithLabelValues("DeleteInstance").Observe(time.Since(deleteStart).Seconds())
	if err != nil {
		statusCode := statusCodeUnknown
		if httpResp != nil {
			statusCode = strconv.Itoa(httpResp.StatusCode)
		}
		// Treat 404 as success (instance already deleted)
		if httpResp != nil && httpResp.StatusCode == 404 {
			if a.eventRecorder != nil {
				a.eventRecorder.Eventf(machineObj, corev1.EventTypeNormal, "Deleted",
					"Instance %s already deleted (404)", *providerStatus.InstanceID)
			}
		} else {
			nicometrics.APICallErrors.WithLabelValues("DeleteInstance", statusCode).Inc()
			if a.eventRecorder != nil {
				a.eventRecorder.Eventf(machineObj, corev1.EventTypeWarning, "FailedDelete", "Failed to delete instance: %v", err)
			}
			return fmt.Errorf("failed to delete instance: %w", err)
		}
	} else if httpResp.StatusCode != 200 && httpResp.StatusCode != 202 &&
		httpResp.StatusCode != 204 && httpResp.StatusCode != 404 {
		// Accept 200 (OK), 202 (Accepted/async), 204 (No Content), 404 (already gone)
		if a.eventRecorder != nil {
			a.eventRecorder.Eventf(machineObj, corev1.EventTypeWarning, "FailedDelete",
				"Delete instance returned unexpected status: %d", httpResp.StatusCode)
		}
		return fmt.Errorf("delete instance returned unexpected status: %d", httpResp.StatusCode)
	}

	nicometrics.MachinesManaged.Dec()
	if a.eventRecorder != nil {
		a.eventRecorder.Eventf(machineObj, corev1.EventTypeNormal, "Deleted",
			"Deleted instance %s", *providerStatus.InstanceID)
	}
	return nil
}

// Helper functions

func (a *Actuator) getProviderSpec(machine client.Object) (*v1beta1.NicoMachineProviderSpec, error) {
	var providerSpecBytes []byte

	switch m := machine.(type) {
	case *machinev1beta1.Machine:
		if m.Spec.ProviderSpec.Value == nil {
			return nil, fmt.Errorf("providerSpec.value is nil")
		}
		providerSpecBytes = m.Spec.ProviderSpec.Value.Raw
	case *unstructured.Unstructured:
		providerSpecValue, found, err := unstructured.NestedFieldCopy(m.Object, "spec", "providerSpec", "value")
		if err != nil || !found {
			return nil, fmt.Errorf("providerSpec.value not found: %w", err)
		}
		providerSpecBytes, err = json.Marshal(providerSpecValue)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal providerSpec: %w", err)
		}
	default:
		return nil, fmt.Errorf("unsupported machine type: %T", machine)
	}

	providerSpec := &v1beta1.NicoMachineProviderSpec{}
	if err := json.Unmarshal(providerSpecBytes, providerSpec); err != nil {
		return nil, fmt.Errorf("failed to unmarshal providerSpec: %w", err)
	}

	return providerSpec, nil
}

func (a *Actuator) getProviderStatus(machine client.Object) (*v1beta1.NicoMachineProviderStatus, error) {
	var providerStatusBytes []byte

	switch m := machine.(type) {
	case *machinev1beta1.Machine:
		if m.Status.ProviderStatus == nil {
			return &v1beta1.NicoMachineProviderStatus{}, nil
		}
		providerStatusBytes = m.Status.ProviderStatus.Raw
	case *unstructured.Unstructured:
		providerStatusValue, found, err := unstructured.NestedFieldCopy(m.Object, "status", "providerStatus")
		if err != nil {
			return nil, fmt.Errorf("failed to get providerStatus: %w", err)
		}
		if !found {
			return &v1beta1.NicoMachineProviderStatus{}, nil
		}
		providerStatusBytes, err = json.Marshal(providerStatusValue)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal providerStatus: %w", err)
		}
	default:
		return nil, fmt.Errorf("unsupported machine type: %T", machine)
	}

	providerStatus := &v1beta1.NicoMachineProviderStatus{}
	if err := json.Unmarshal(providerStatusBytes, providerStatus); err != nil {
		return nil, fmt.Errorf("failed to unmarshal providerStatus: %w", err)
	}

	return providerStatus, nil
}

func (a *Actuator) setProviderStatus(
	ctx context.Context, machine client.Object,
	status *v1beta1.NicoMachineProviderStatus,
) error {
	statusBytes, err := json.Marshal(status)
	if err != nil {
		return fmt.Errorf("failed to marshal status: %w", err)
	}

	switch m := machine.(type) {
	case *machinev1beta1.Machine:
		m.Status.ProviderStatus = &runtime.RawExtension{Raw: statusBytes}
		if err := a.client.Status().Update(ctx, m); err != nil {
			return fmt.Errorf("failed to update machine status: %w", err)
		}
	case *unstructured.Unstructured:
		var statusMap map[string]interface{}
		if err := json.Unmarshal(statusBytes, &statusMap); err != nil {
			return fmt.Errorf("failed to unmarshal status to map: %w", err)
		}
		if err := unstructured.SetNestedField(m.Object, statusMap, "status", "providerStatus"); err != nil {
			return fmt.Errorf("failed to set providerStatus: %w", err)
		}
		if err := a.client.Status().Update(ctx, m); err != nil {
			return fmt.Errorf("failed to update machine status: %w", err)
		}
	default:
		return fmt.Errorf("unsupported machine type: %T", machine)
	}

	return nil
}

func (a *Actuator) setProviderID(ctx context.Context, machine client.Object, providerID string) error {
	switch m := machine.(type) {
	case *machinev1beta1.Machine:
		m.Spec.ProviderID = &providerID
		if err := a.client.Update(ctx, m); err != nil {
			return fmt.Errorf("failed to update machine: %w", err)
		}
	case *unstructured.Unstructured:
		if err := unstructured.SetNestedField(m.Object, providerID, "spec", "providerID"); err != nil {
			return fmt.Errorf("failed to set providerID: %w", err)
		}
		if err := a.client.Update(ctx, m); err != nil {
			return fmt.Errorf("failed to update machine: %w", err)
		}
	default:
		return fmt.Errorf("unsupported machine type: %T", machine)
	}

	return nil
}

func (a *Actuator) getNicoClient(
	ctx context.Context, providerSpec *v1beta1.NicoMachineProviderSpec,
) (NicoClientInterface, string, error) {
	// Use injected client for testing
	if a.nicoAPIClient != nil {
		return a.nicoAPIClient, a.orgName, nil
	}

	// Fetch credentials secret
	secret := &corev1.Secret{}
	secretKey := client.ObjectKey{
		Name:      providerSpec.CredentialsSecret.Name,
		Namespace: providerSpec.CredentialsSecret.Namespace,
	}

	if err := a.client.Get(ctx, secretKey, secret); err != nil {
		return nil, "", fmt.Errorf("failed to get credentials secret: %w", err)
	}

	// Validate secret contains required fields
	endpoint, ok := secret.Data["endpoint"]
	if !ok {
		return nil, "", fmt.Errorf("secret %s is missing 'endpoint' field", secretKey.Name)
	}
	orgName, ok := secret.Data["orgName"]
	if !ok {
		return nil, "", fmt.Errorf("secret %s is missing 'orgName' field", secretKey.Name)
	}
	token, ok := secret.Data["token"]
	if !ok {
		return nil, "", fmt.Errorf("secret %s is missing 'token' field", secretKey.Name)
	}

	// Create NICo API client
	sdkCfg := nico.NewConfiguration()
	sdkCfg.Servers = nico.ServerConfigurations{
		{URL: string(endpoint)},
	}

	return &nicoClient{
		client: nico.NewAPIClient(sdkCfg),
		token:  string(token),
	}, string(orgName), nil
}

// ptr is a helper function to get a pointer to a value
func ptr[T any](v T) *T {
	return &v
}

// setInstanceStateConditions maps NICo InstanceStatus values to provider
// status conditions. The SDK defines: Pending, Provisioning, Configuring,
// Ready, Updating, Rebooting, Terminating, Error.
func setInstanceStateConditions(
	providerStatus *v1beta1.NicoMachineProviderStatus,
	status nico.InstanceStatus,
) {
	switch status {
	case nico.INSTANCESTATUS_PENDING:
		setCondition(providerStatus, "InstanceAllocating", metav1.ConditionTrue,
			"Pending", "Instance creation request sent, awaiting allocation")
		setCondition(providerStatus, "InstanceReady", metav1.ConditionFalse,
			"Pending", "Instance is pending allocation")

	case nico.INSTANCESTATUS_PROVISIONING:
		setCondition(providerStatus, "InstanceAllocating", metav1.ConditionFalse,
			"Allocated", "Instance has been allocated")
		setCondition(providerStatus, "InstanceProvisioning", metav1.ConditionTrue,
			"Provisioning", "NICo is provisioning the machine")
		setCondition(providerStatus, "InstanceReady", metav1.ConditionFalse,
			"Provisioning", "Instance is being provisioned")

	case nico.INSTANCESTATUS_CONFIGURING:
		setCondition(providerStatus, "InstanceProvisioning", metav1.ConditionFalse,
			"Provisioned", "Machine has been provisioned")
		setCondition(providerStatus, "InstanceBootstrapping", metav1.ConditionTrue,
			"Configuring", "OS installed, configuration in progress")
		setCondition(providerStatus, "InstanceReady", metav1.ConditionFalse,
			"Configuring", "Instance is being configured")

	case nico.INSTANCESTATUS_READY:
		setCondition(providerStatus, "InstanceBootstrapping", metav1.ConditionFalse,
			"Complete", "Bootstrap completed")
		setCondition(providerStatus, "InstanceReady", metav1.ConditionTrue,
			"InstanceRunning", "Instance is fully operational")

	case nico.INSTANCESTATUS_UPDATING:
		setCondition(providerStatus, "InstanceReady", metav1.ConditionFalse,
			"Updating", "Instance is being updated")

	case nico.INSTANCESTATUS_REBOOTING:
		setCondition(providerStatus, "InstanceReady", metav1.ConditionFalse,
			"Rebooting", "Instance is rebooting")

	case nico.INSTANCESTATUS_TERMINATING:
		setCondition(providerStatus, "InstanceTerminating", metav1.ConditionTrue,
			"Terminating", "Instance deletion in progress")
		setCondition(providerStatus, "InstanceReady", metav1.ConditionFalse,
			"Terminating", "Instance is terminating")

	case nico.INSTANCESTATUS_ERROR:
		setCondition(providerStatus, "InstanceError", metav1.ConditionTrue,
			"Error", "Instance is in a terminal error state")
		setCondition(providerStatus, "InstanceReady", metav1.ConditionFalse,
			"Error", "Instance encountered an error")

	default:
		setCondition(providerStatus, "InstanceReady", metav1.ConditionFalse,
			"Unknown", fmt.Sprintf("Instance is in unknown state: %s", string(status)))
	}
}

func setCondition(
	providerStatus *v1beta1.NicoMachineProviderStatus,
	condType string, status metav1.ConditionStatus, reason, message string,
) {
	meta.SetStatusCondition(&providerStatus.Conditions, metav1.Condition{
		Type:    condType,
		Status:  status,
		Reason:  reason,
		Message: message,
	})
}

// handleCreateError processes API errors from CreateInstance, recording
// metrics and events and returning a classified error.
func (a *Actuator) handleCreateError(
	machineObj client.Object, httpResp *http.Response, err error,
) error {
	statusCode := statusCodeUnknown
	if httpResp != nil {
		statusCode = strconv.Itoa(httpResp.StatusCode)
	}
	nicometrics.APICallErrors.WithLabelValues(
		"CreateInstance", statusCode,
	).Inc()

	errKind := "transient"
	classified := classifyHTTPError(httpResp, err)
	var ce *ClassifiedError
	if errors.As(classified, &ce) && ce.Kind == ErrorTerminal {
		errKind = "terminal"
	}

	if a.eventRecorder != nil {
		a.eventRecorder.Eventf(machineObj,
			corev1.EventTypeWarning, "FailedCreate",
			"NICo API error (%s): %v", errKind, err)
	}

	var wrappedErr error
	if httpResp != nil && httpResp.Body != nil {
		respBody, _ := io.ReadAll(httpResp.Body)
		type apiError struct {
			Message string `json:"message"`
			Code    string `json:"code"`
		}
		var parsedErr apiError
		if json.Unmarshal(respBody, &parsedErr) == nil &&
			parsedErr.Message != "" {
			wrappedErr = fmt.Errorf(
				"failed to create instance (status %d): %s: %w",
				httpResp.StatusCode, parsedErr.Message, err,
			)
		} else {
			wrappedErr = fmt.Errorf(
				"failed to create instance (status %d): %w",
				httpResp.StatusCode, err,
			)
		}
	} else {
		wrappedErr = fmt.Errorf("failed to create instance: %w", err)
	}
	return classifyHTTPError(httpResp, wrappedErr)
}

// deployDpuExtensionServices deploys DPU extension services on a newly
// created instance via the UpdateInstance API.
func (a *Actuator) deployDpuExtensionServices(
	ctx context.Context,
	nicoAPIClient NicoClientInterface,
	orgName string,
	machineObj client.Object,
	instanceID string,
	providerSpec *v1beta1.NicoMachineProviderSpec,
) error {
	dpuDeployments := make(
		[]nico.DpuExtensionServiceDeploymentRequest,
		0, len(providerSpec.DpuExtensionServices),
	)
	for _, svc := range providerSpec.DpuExtensionServices {
		dpuReq := nico.DpuExtensionServiceDeploymentRequest{
			DpuExtensionServiceId: &svc.ServiceID,
		}
		if svc.Version != "" {
			dpuReq.Version = &svc.Version
		}
		dpuDeployments = append(dpuDeployments, dpuReq)
	}
	updateReq := nico.InstanceUpdateRequest{
		DpuExtensionServiceDeployments: dpuDeployments,
	}
	_, updateResp, updateErr := nicoAPIClient.UpdateInstance(
		ctx, orgName, instanceID, updateReq,
	)
	if updateErr != nil {
		statusCode := statusCodeUnknown
		if updateResp != nil {
			statusCode = strconv.Itoa(updateResp.StatusCode)
		}
		nicometrics.APICallErrors.WithLabelValues(
			"UpdateInstance", statusCode,
		).Inc()
		if a.eventRecorder != nil {
			a.eventRecorder.Eventf(machineObj,
				corev1.EventTypeWarning, "DPUDeployFailed",
				"Failed to deploy DPU extension services: %v",
				updateErr)
		}
		return fmt.Errorf(
			"failed to deploy DPU extension services: %w", updateErr,
		)
	}
	if a.eventRecorder != nil {
		a.eventRecorder.Eventf(machineObj,
			corev1.EventTypeNormal, "DPUDeployed",
			"Deployed %d DPU extension service(s)",
			len(providerSpec.DpuExtensionServices))
	}
	return nil
}

// provisioningStuckThreshold is the duration after which a Provisioning instance
// is considered stuck and status history is emitted as Warning events.
const provisioningStuckThreshold = 5 * time.Minute

// ProvisioningTimeout is the maximum duration to wait for an instance to reach
// Ready state before setting FailureReason. Exported for testing.
var ProvisioningTimeout = 30 * time.Minute

// checkStatusHistory fetches the instance status history and records transitions
// as Warning events when the instance is in Error state or has been Provisioning
// for more than 5 minutes. This aids debugging without direct API access.
func (a *Actuator) checkStatusHistory(
	ctx context.Context,
	nicoAPIClient NicoClientInterface,
	orgName string,
	machineObj client.Object,
	instance *nico.Instance,
) {
	if instance.Status == nil || a.eventRecorder == nil {
		return
	}

	shouldFetch := false
	switch *instance.Status {
	case nico.INSTANCESTATUS_ERROR:
		shouldFetch = true
	case nico.INSTANCESTATUS_PROVISIONING:
		// Check if Provisioning for more than 5 minutes using status history
		shouldFetch = true
	default:
		return
	}

	if !shouldFetch {
		return
	}

	history, httpResp, err := nicoAPIClient.GetInstanceStatusHistory(ctx, orgName, *instance.Id)
	if err != nil || httpResp == nil || httpResp.StatusCode != http.StatusOK || len(history) == 0 {
		return
	}

	// For Provisioning state, check if it's been stuck for more than the threshold
	if *instance.Status == nico.INSTANCESTATUS_PROVISIONING {
		stuck := false
		for _, entry := range history {
			if entry.Status != nil && *entry.Status == string(nico.INSTANCESTATUS_PROVISIONING) && entry.Created != nil {
				if time.Since(*entry.Created) > provisioningStuckThreshold {
					stuck = true
				}
				break
			}
		}
		if !stuck {
			return
		}
		a.eventRecorder.Eventf(machineObj, corev1.EventTypeWarning, "ProvisioningStuck",
			"Instance has been in Provisioning state for more than %s", provisioningStuckThreshold)
	}

	// Record status transitions as Warning events for debugging
	for _, entry := range history {
		status := "unknown"
		if entry.Status != nil {
			status = *entry.Status
		}
		msg := ""
		if entry.Message != nil {
			msg = *entry.Message
		}
		ts := ""
		if entry.Created != nil {
			ts = entry.Created.Format(time.RFC3339)
		}
		if msg != "" {
			a.eventRecorder.Eventf(machineObj, corev1.EventTypeWarning, "StatusHistory",
				"[%s] %s: %s", ts, status, msg)
		} else {
			a.eventRecorder.Eventf(machineObj, corev1.EventTypeWarning, "StatusHistory",
				"[%s] %s", ts, status)
		}
	}
}

// checkProvisioningTimeout sets FailureReason on the Machine if the instance
// has been stuck in a non-Ready state beyond ProvisioningTimeout.
func (a *Actuator) checkProvisioningTimeout(
	machineObj client.Object,
	instance *nico.Instance,
	providerStatus *v1beta1.NicoMachineProviderStatus,
) {
	if instance.Status == nil {
		return
	}
	// Only check timeout for non-terminal, non-ready states
	switch *instance.Status {
	case nico.INSTANCESTATUS_PENDING, nico.INSTANCESTATUS_PROVISIONING, nico.INSTANCESTATUS_CONFIGURING:
		// continue
	default:
		return
	}

	// Find the InstanceProvisioned condition set during Create
	for _, c := range providerStatus.Conditions {
		if c.Type == "InstanceProvisioned" && !c.LastTransitionTime.IsZero() {
			if time.Since(c.LastTransitionTime.Time) > ProvisioningTimeout {
				setCondition(providerStatus, "InstanceReady", metav1.ConditionFalse,
					"ProvisioningTimeout",
					fmt.Sprintf("Instance has not reached Ready state after %s", ProvisioningTimeout))
				// Set failure on the Machine object if it's a typed Machine
				if m, ok := machineObj.(*machinev1beta1.Machine); ok {
					reason := "ProvisioningTimeout"
					msg := fmt.Sprintf("Instance stuck in %s state for more than %s", string(*instance.Status), ProvisioningTimeout)
					m.Status.ErrorReason = (*machinev1beta1.MachineStatusError)(&reason)
					m.Status.ErrorMessage = &msg
				}
				if a.eventRecorder != nil {
					a.eventRecorder.Eventf(machineObj, corev1.EventTypeWarning, "ProvisioningTimeout",
						"Instance has not reached Ready state after %s, setting FailureReason", ProvisioningTimeout)
				}
			}
			break
		}
	}
}

// isMHCRemediation detects if the Machine deletion was triggered by
// OpenShift MachineHealthCheck remediation. MHC sets the annotation
// "machine.openshift.io/unhealthy" on the Machine before requesting deletion.
func isMHCRemediation(machine client.Object) bool {
	annotations := machine.GetAnnotations()
	if annotations == nil {
		return false
	}
	_, hasUnhealthy := annotations["machine.openshift.io/unhealthy"]
	return hasUnhealthy
}

// updateMachineHealth queries the NICo Machine API for health data and
// sets the MachineHealthy condition. Health failures are non-fatal — they
// are logged but do not block the Update.
func (a *Actuator) updateMachineHealth(
	ctx context.Context,
	nicoAPIClient NicoClientInterface,
	orgName string,
	instance *nico.Instance,
	providerStatus *v1beta1.NicoMachineProviderStatus,
) {
	machineID, ok := instance.GetMachineIdOk()
	if !ok || machineID == nil || *machineID == "" {
		return
	}

	machine, httpResp, err := nicoAPIClient.GetMachine(ctx, orgName, *machineID)
	if err != nil || httpResp == nil || httpResp.StatusCode != http.StatusOK || machine == nil {
		return
	}

	if machine.Health == nil {
		return
	}

	if len(machine.Health.Alerts) == 0 {
		setCondition(providerStatus, "MachineHealthy", metav1.ConditionTrue,
			"Healthy", "Machine has no health alerts")
		providerStatus.HealthLabels = map[string]string{
			"nico.io/healthy": "true",
		}
	} else {
		alertMsg := fmt.Sprintf("Machine has %d health alert(s)", len(machine.Health.Alerts))
		setCondition(providerStatus, "MachineHealthy", metav1.ConditionFalse,
			"Unhealthy", alertMsg)
		providerStatus.HealthLabels = map[string]string{
			"nico.io/healthy":            "false",
			"nico.io/health-alert-count": fmt.Sprintf("%d", len(machine.Health.Alerts)),
		}
	}
}
