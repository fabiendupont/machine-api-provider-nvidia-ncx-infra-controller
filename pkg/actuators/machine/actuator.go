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
	"fmt"
	"net/http"

	"github.com/google/uuid"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"

	v1beta1 "github.com/fabiendupont/machine-api-provider-nvidia-carbide/pkg/apis/nvidiacarbideprovider/v1beta1"
	"github.com/fabiendupont/machine-api-provider-nvidia-carbide/pkg/providerid"
	bmm "github.com/nvidia/bare-metal-manager-rest/sdk/standard"
)

// NvidiaCarbideClientInterface defines the methods needed from NVIDIA Carbide REST client
type NvidiaCarbideClientInterface interface {
	CreateInstance(ctx context.Context, org string, req bmm.InstanceCreateRequest) (*bmm.Instance, *http.Response, error)
	GetInstance(ctx context.Context, org string, instanceId string) (*bmm.Instance, *http.Response, error)
	DeleteInstance(ctx context.Context, org string, instanceId string) (*http.Response, error)
}

// carbideClient wraps the SDK APIClient and injects auth context
type carbideClient struct {
	client *bmm.APIClient
	token  string
}

func (c *carbideClient) authCtx(ctx context.Context) context.Context {
	return context.WithValue(ctx, bmm.ContextAccessToken, c.token)
}

func (c *carbideClient) CreateInstance(
	ctx context.Context, org string, req bmm.InstanceCreateRequest,
) (*bmm.Instance, *http.Response, error) {
	return c.client.InstanceAPI.CreateInstance(c.authCtx(ctx), org).InstanceCreateRequest(req).Execute()
}

func (c *carbideClient) GetInstance(
	ctx context.Context, org, instanceId string,
) (*bmm.Instance, *http.Response, error) {
	return c.client.InstanceAPI.GetInstance(c.authCtx(ctx), org, instanceId).Execute()
}

func (c *carbideClient) DeleteInstance(ctx context.Context, org, instanceId string) (*http.Response, error) {
	return c.client.InstanceAPI.DeleteInstance(c.authCtx(ctx), org, instanceId).Execute()
}

// Actuator implements the OpenShift Machine actuator interface
type Actuator struct {
	client        client.Client
	eventRecorder record.EventRecorder
	// For testing
	nvidiaCarbideClient NvidiaCarbideClientInterface
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
	nvidiaCarbideClient NvidiaCarbideClientInterface, orgName string,
) *Actuator {
	return &Actuator{
		client:              k8sClient,
		eventRecorder:       eventRecorder,
		nvidiaCarbideClient: nvidiaCarbideClient,
		orgName:             orgName,
	}
}

// buildInstanceRequest constructs the API request body from a provider spec.
func buildInstanceRequest(
	name string,
	providerSpec *v1beta1.NvidiaCarbideMachineProviderSpec,
) bmm.InstanceCreateRequest {
	interfaces := []bmm.InterfaceCreateRequest{
		{
			SubnetId:   &providerSpec.SubnetID,
			IsPhysical: ptr(false),
		},
	}

	for _, additionalSubnet := range providerSpec.AdditionalSubnetIDs {
		subnetID := additionalSubnet.SubnetID
		interfaces = append(interfaces, bmm.InterfaceCreateRequest{
			SubnetId:   &subnetID,
			IsPhysical: ptr(additionalSubnet.IsPhysical),
		})
	}

	req := bmm.InstanceCreateRequest{
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
		req.UserData = *bmm.NewNullableString(&userData)
	}
	if len(providerSpec.SSHKeyGroupIDs) > 0 {
		req.SshKeyGroupIds = providerSpec.SSHKeyGroupIDs
	}
	if len(providerSpec.Labels) > 0 {
		req.Labels = providerSpec.Labels
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

	// Get NVIDIA Carbide client and orgName
	nvidiaCarbideClient, orgName, err := a.getNvidiaCarbideClient(ctx, providerSpec)
	if err != nil {
		return fmt.Errorf("failed to create NVIDIA Carbide client: %w", err)
	}

	// Build instance request
	instanceReq := buildInstanceRequest(machineObj.GetName(), providerSpec)

	// Create instance
	instance, httpResp, err := nvidiaCarbideClient.CreateInstance(ctx, orgName, instanceReq)
	if err != nil {
		if a.eventRecorder != nil {
			a.eventRecorder.Eventf(machineObj, corev1.EventTypeWarning, "FailedCreate", "Failed to create instance: %v", err)
		}
		return fmt.Errorf("failed to create instance: %w", err)
	}

	if instance == nil {
		if a.eventRecorder != nil {
			a.eventRecorder.Eventf(machineObj, corev1.EventTypeWarning, "FailedCreate", "Create instance returned no data")
		}
		return fmt.Errorf("create instance returned no data, status code: %d", httpResp.StatusCode)
	}

	// Build provider status
	providerStatus := &v1beta1.NvidiaCarbideMachineProviderStatus{
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

	if err := a.setProviderStatus(machineObj, providerStatus); err != nil {
		return fmt.Errorf("failed to update provider status: %w", err)
	}

	// Set provider ID using the local providerid package
	instanceUUID, err := uuid.Parse(*instance.Id)
	if err != nil {
		return fmt.Errorf("failed to parse instance ID from response: %w", err)
	}
	pid := providerid.NewProviderID(orgName, providerSpec.TenantID, providerSpec.SiteID, instanceUUID)
	if err := a.setProviderID(machineObj, pid.String()); err != nil {
		return fmt.Errorf("failed to set provider ID: %w", err)
	}

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

	// Get NVIDIA Carbide client and orgName
	nvidiaCarbideClient, orgName, err := a.getNvidiaCarbideClient(ctx, providerSpec)
	if err != nil {
		return fmt.Errorf("failed to create NVIDIA Carbide client: %w", err)
	}

	// Get current instance status
	instance, httpResp, err := nvidiaCarbideClient.GetInstance(ctx, orgName, *providerStatus.InstanceID)
	if err != nil {
		return fmt.Errorf("failed to get instance: %w", err)
	}

	if instance == nil {
		return fmt.Errorf("get instance returned no data, status code: %d", httpResp.StatusCode)
	}

	// Update provider status
	if instance.Status != nil {
		status := string(*instance.Status)
		providerStatus.InstanceState = &status
	}
	if instance.MachineId.Get() != nil {
		providerStatus.MachineID = instance.MachineId.Get()
	}

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

	if err := a.setProviderStatus(machineObj, providerStatus); err != nil {
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

	// Get NVIDIA Carbide client and orgName
	nvidiaCarbideClient, orgName, err := a.getNvidiaCarbideClient(ctx, providerSpec)
	if err != nil {
		return false, fmt.Errorf("failed to create NVIDIA Carbide client: %w", err)
	}

	// Check if instance exists
	instance, _, err := nvidiaCarbideClient.GetInstance(ctx, orgName, *providerStatus.InstanceID)
	if err != nil {
		return false, nil
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

	// Get NVIDIA Carbide client and orgName
	nvidiaCarbideClient, orgName, err := a.getNvidiaCarbideClient(ctx, providerSpec)
	if err != nil {
		return fmt.Errorf("failed to create NVIDIA Carbide client: %w", err)
	}

	// Delete instance
	httpResp, err := nvidiaCarbideClient.DeleteInstance(ctx, orgName, *providerStatus.InstanceID)
	if err != nil {
		if a.eventRecorder != nil {
			a.eventRecorder.Eventf(machineObj, corev1.EventTypeWarning, "FailedDelete", "Failed to delete instance: %v", err)
		}
		return fmt.Errorf("failed to delete instance: %w", err)
	}

	// Check response
	if httpResp.StatusCode != 204 && httpResp.StatusCode != 404 {
		if a.eventRecorder != nil {
			a.eventRecorder.Eventf(machineObj, corev1.EventTypeWarning, "FailedDelete",
				"Delete instance returned unexpected status: %d", httpResp.StatusCode)
		}
		return fmt.Errorf("delete instance returned unexpected status: %d", httpResp.StatusCode)
	}

	if a.eventRecorder != nil {
		a.eventRecorder.Eventf(machineObj, corev1.EventTypeNormal, "Deleted",
			"Deleted instance %s", *providerStatus.InstanceID)
	}
	return nil
}

// Helper functions

func (a *Actuator) getProviderSpec(machine client.Object) (*v1beta1.NvidiaCarbideMachineProviderSpec, error) {
	// Cast to unstructured to access nested fields
	unstructuredMachine, ok := machine.(*unstructured.Unstructured)
	if !ok {
		return nil, fmt.Errorf("machine is not unstructured")
	}

	// Extract providerSpec.value from spec
	providerSpecValue, found, err := unstructured.NestedFieldCopy(
		unstructuredMachine.Object,
		"spec", "providerSpec", "value",
	)
	if err != nil || !found {
		return nil, fmt.Errorf("providerSpec.value not found: %w", err)
	}

	// Marshal and unmarshal to get typed struct
	providerSpecBytes, err := json.Marshal(providerSpecValue)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal providerSpec: %w", err)
	}

	providerSpec := &v1beta1.NvidiaCarbideMachineProviderSpec{}
	if err := json.Unmarshal(providerSpecBytes, providerSpec); err != nil {
		return nil, fmt.Errorf("failed to unmarshal providerSpec: %w", err)
	}

	return providerSpec, nil
}

func (a *Actuator) getProviderStatus(machine client.Object) (*v1beta1.NvidiaCarbideMachineProviderStatus, error) {
	// Cast to unstructured to access nested fields
	unstructuredMachine, ok := machine.(*unstructured.Unstructured)
	if !ok {
		return nil, fmt.Errorf("machine is not unstructured")
	}

	// Extract providerStatus from status
	providerStatusValue, found, err := unstructured.NestedFieldCopy(
		unstructuredMachine.Object,
		"status", "providerStatus",
	)
	if err != nil {
		return nil, fmt.Errorf("failed to get providerStatus: %w", err)
	}

	// If not found, return empty status (this is OK for new machines)
	if !found {
		return &v1beta1.NvidiaCarbideMachineProviderStatus{}, nil
	}

	// Marshal and unmarshal to get typed struct
	providerStatusBytes, err := json.Marshal(providerStatusValue)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal providerStatus: %w", err)
	}

	providerStatus := &v1beta1.NvidiaCarbideMachineProviderStatus{}
	if err := json.Unmarshal(providerStatusBytes, providerStatus); err != nil {
		return nil, fmt.Errorf("failed to unmarshal providerStatus: %w", err)
	}

	return providerStatus, nil
}

func (a *Actuator) setProviderStatus(machine client.Object, status *v1beta1.NvidiaCarbideMachineProviderStatus) error {
	// Cast to unstructured to access nested fields
	unstructuredMachine, ok := machine.(*unstructured.Unstructured)
	if !ok {
		return fmt.Errorf("machine is not unstructured")
	}

	// Convert status to map
	statusBytes, err := json.Marshal(status)
	if err != nil {
		return fmt.Errorf("failed to marshal status: %w", err)
	}

	var statusMap map[string]interface{}
	if err := json.Unmarshal(statusBytes, &statusMap); err != nil {
		return fmt.Errorf("failed to unmarshal status to map: %w", err)
	}

	// Set providerStatus in status
	if err := unstructured.SetNestedField(
		unstructuredMachine.Object,
		statusMap,
		"status", "providerStatus",
	); err != nil {
		return fmt.Errorf("failed to set providerStatus: %w", err)
	}

	// Update the machine status
	if err := a.client.Status().Update(context.Background(), unstructuredMachine); err != nil {
		return fmt.Errorf("failed to update machine status: %w", err)
	}

	return nil
}

func (a *Actuator) setProviderID(machine client.Object, providerID string) error {
	// Cast to unstructured to access nested fields
	unstructuredMachine, ok := machine.(*unstructured.Unstructured)
	if !ok {
		return fmt.Errorf("machine is not unstructured")
	}

	// Set spec.providerID
	if err := unstructured.SetNestedField(
		unstructuredMachine.Object,
		providerID,
		"spec", "providerID",
	); err != nil {
		return fmt.Errorf("failed to set providerID: %w", err)
	}

	// Update the machine
	if err := a.client.Update(context.Background(), unstructuredMachine); err != nil {
		return fmt.Errorf("failed to update machine: %w", err)
	}

	return nil
}

func (a *Actuator) getNvidiaCarbideClient(
	ctx context.Context, providerSpec *v1beta1.NvidiaCarbideMachineProviderSpec,
) (NvidiaCarbideClientInterface, string, error) {
	// Use injected client for testing
	if a.nvidiaCarbideClient != nil {
		return a.nvidiaCarbideClient, a.orgName, nil
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

	// Create NVIDIA Carbide API client
	sdkCfg := bmm.NewConfiguration()
	sdkCfg.Servers = bmm.ServerConfigurations{
		{URL: string(endpoint)},
	}

	return &carbideClient{
		client: bmm.NewAPIClient(sdkCfg),
		token:  string(token),
	}, string(orgName), nil
}

// ptr is a helper function to get a pointer to a value
func ptr[T any](v T) *T {
	return &v
}
