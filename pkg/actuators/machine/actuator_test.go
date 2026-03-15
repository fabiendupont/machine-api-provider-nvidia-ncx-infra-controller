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
	"testing"

	"github.com/google/uuid"
	machinev1beta1 "github.com/openshift/api/machine/v1beta1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	v1beta1 "github.com/fabiendupont/machine-api-provider-nvidia-carbide/pkg/apis/nvidiacarbideprovider/v1beta1"
	"github.com/fabiendupont/machine-api-provider-nvidia-carbide/pkg/providerid"
	bmm "github.com/nvidia/bare-metal-manager-rest/sdk/standard"
)

// mockCarbideClient implements NvidiaCarbideClientInterface for testing
type mockCarbideClient struct {
	createInstance func(ctx context.Context, org string, req bmm.InstanceCreateRequest) (*bmm.Instance, *http.Response, error)
	getInstance    func(ctx context.Context, org string, instanceId string) (*bmm.Instance, *http.Response, error)
	deleteInstance func(ctx context.Context, org string, instanceId string) (*http.Response, error)
	getMachine     func(ctx context.Context, org string, machineId string) (*bmm.Machine, *http.Response, error)
}

func (m *mockCarbideClient) CreateInstance(ctx context.Context, org string, req bmm.InstanceCreateRequest) (*bmm.Instance, *http.Response, error) {
	return m.createInstance(ctx, org, req)
}

func (m *mockCarbideClient) GetInstance(ctx context.Context, org string, instanceId string) (*bmm.Instance, *http.Response, error) {
	return m.getInstance(ctx, org, instanceId)
}

func (m *mockCarbideClient) DeleteInstance(ctx context.Context, org string, instanceId string) (*http.Response, error) {
	return m.deleteInstance(ctx, org, instanceId)
}

func (m *mockCarbideClient) GetMachine(ctx context.Context, org string, machineId string) (*bmm.Machine, *http.Response, error) {
	if m.getMachine != nil {
		return m.getMachine(ctx, org, machineId)
	}
	return nil, &http.Response{StatusCode: 404}, fmt.Errorf("not found")
}

func (m *mockCarbideClient) GetCurrentTenant(ctx context.Context, org string) (*bmm.Tenant, *http.Response, error) {
	return nil, &http.Response{StatusCode: 200}, nil
}

func (m *mockCarbideClient) GetInstanceStatusHistory(ctx context.Context, org string, instanceId string) ([]bmm.StatusDetail, *http.Response, error) {
	return nil, &http.Response{StatusCode: 200}, nil
}

func newTestActuator(mock *mockCarbideClient) (*Actuator, *record.FakeRecorder) {
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	_ = machinev1beta1.AddToScheme(scheme)

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		Build()

	recorder := record.NewFakeRecorder(10)
	actuator := NewActuatorWithClient(fakeClient, recorder, mock, "test-org")
	return actuator, recorder
}

func newTestActuatorWithMachine(mock *mockCarbideClient, machine *machinev1beta1.Machine) (*Actuator, *record.FakeRecorder) {
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	_ = machinev1beta1.AddToScheme(scheme)

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(machine).
		WithStatusSubresource(machine).
		Build()

	recorder := record.NewFakeRecorder(10)
	actuator := NewActuatorWithClient(fakeClient, recorder, mock, "test-org")
	return actuator, recorder
}

func testInstance(id string) *bmm.Instance {
	status := bmm.INSTANCESTATUS_PROVISIONING
	machineId := bmm.NewNullableString(ptr("machine-123"))
	return &bmm.Instance{
		Id:        &id,
		Status:    &status,
		MachineId: *machineId,
		Interfaces: []bmm.Interface{
			{
				IpAddresses: []string{"10.0.0.1"},
			},
		},
	}
}

func validProviderSpec() v1beta1.NvidiaCarbideMachineProviderSpec {
	return v1beta1.NvidiaCarbideMachineProviderSpec{
		SiteID:         "550e8400-e29b-41d4-a716-446655440000",
		TenantID:       "660e8400-e29b-41d4-a716-446655440001",
		InstanceTypeID: "990e8400-e29b-41d4-a716-446655440004",
		VpcID:          "770e8400-e29b-41d4-a716-446655440002",
		SubnetID:       "880e8400-e29b-41d4-a716-446655440003",
		CredentialsSecret: v1beta1.CredentialsSecretReference{
			Name:      "nvidia-carbide-creds",
			Namespace: "default",
		},
	}
}

func createTypedTestMachine(providerSpec v1beta1.NvidiaCarbideMachineProviderSpec) *machinev1beta1.Machine {
	specBytes, _ := json.Marshal(providerSpec)
	return &machinev1beta1.Machine{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-machine",
			Namespace: "default",
		},
		Spec: machinev1beta1.MachineSpec{
			ProviderSpec: machinev1beta1.ProviderSpec{
				Value: &runtime.RawExtension{Raw: specBytes},
			},
		},
	}
}

func createTestMachine(providerSpec v1beta1.NvidiaCarbideMachineProviderSpec) *unstructured.Unstructured {
	machine := &unstructured.Unstructured{}
	machine.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "machine.openshift.io",
		Version: "v1beta1",
		Kind:    "Machine",
	})
	machine.SetName("test-machine")
	machine.SetNamespace("default")

	providerSpecMap, _ := runtime.DefaultUnstructuredConverter.ToUnstructured(&providerSpec)
	_ = unstructured.SetNestedField(machine.Object, providerSpecMap, "spec", "providerSpec", "value")

	return machine
}

func createTestMachineWithStatus(
	providerSpec v1beta1.NvidiaCarbideMachineProviderSpec,
	providerStatus v1beta1.NvidiaCarbideMachineProviderStatus,
) *unstructured.Unstructured {
	machine := createTestMachine(providerSpec)

	statusMap, _ := runtime.DefaultUnstructuredConverter.ToUnstructured(&providerStatus)
	_ = unstructured.SetNestedField(machine.Object, statusMap, "status", "providerStatus")

	return machine
}

func TestCreate_Success(t *testing.T) {
	instanceID := uuid.New().String()
	mock := &mockCarbideClient{
		createInstance: func(ctx context.Context, org string, req bmm.InstanceCreateRequest) (*bmm.Instance, *http.Response, error) {
			return testInstance(instanceID), &http.Response{StatusCode: 201}, nil
		},
	}

	machine := createTypedTestMachine(validProviderSpec())
	actuator, _ := newTestActuatorWithMachine(mock, machine)

	err := actuator.Create(context.Background(), machine)
	if err != nil {
		t.Fatalf("Create() unexpected error: %v", err)
	}
}

func TestCreate_APIError(t *testing.T) {
	mock := &mockCarbideClient{
		createInstance: func(ctx context.Context, org string, req bmm.InstanceCreateRequest) (*bmm.Instance, *http.Response, error) {
			return nil, nil, fmt.Errorf("connection refused")
		},
	}

	actuator, _ := newTestActuator(mock)
	machine := createTestMachine(validProviderSpec())

	err := actuator.Create(context.Background(), machine)
	if err == nil {
		t.Fatal("Create() expected error, got nil")
	}
}

func TestCreate_InvalidSpec(t *testing.T) {
	createCalled := false
	mock := &mockCarbideClient{
		createInstance: func(ctx context.Context, org string, req bmm.InstanceCreateRequest) (*bmm.Instance, *http.Response, error) {
			createCalled = true
			return nil, nil, nil
		},
	}

	actuator, _ := newTestActuator(mock)

	// Both instanceTypeId and machineId set
	spec := validProviderSpec()
	spec.MachineID = "some-machine-id"
	machine := createTestMachine(spec)

	err := actuator.Create(context.Background(), machine)
	if err == nil {
		t.Fatal("Create() expected validation error, got nil")
	}
	if createCalled {
		t.Error("Create() should not have called the API with invalid spec")
	}
}

func TestCreate_MissingRequiredFields(t *testing.T) {
	mock := &mockCarbideClient{}
	actuator, _ := newTestActuator(mock)

	// Neither instanceTypeId nor machineId
	spec := validProviderSpec()
	spec.InstanceTypeID = ""
	machine := createTestMachine(spec)

	err := actuator.Create(context.Background(), machine)
	if err == nil {
		t.Fatal("Create() expected validation error for missing instanceTypeId/machineId")
	}
}

func TestExists_TransientError(t *testing.T) {
	mock := &mockCarbideClient{
		getInstance: func(ctx context.Context, org string, instanceId string) (*bmm.Instance, *http.Response, error) {
			return nil, nil, fmt.Errorf("connection timeout")
		},
	}

	actuator, _ := newTestActuator(mock)
	instanceID := "test-instance-id"
	machine := createTestMachineWithStatus(validProviderSpec(), v1beta1.NvidiaCarbideMachineProviderStatus{
		InstanceID: &instanceID,
	})

	exists, err := actuator.Exists(context.Background(), machine)
	if err == nil {
		t.Fatal("Exists() expected error on transient failure, got nil")
	}
	if exists {
		t.Error("Exists() should return false on error")
	}
}

func TestExists_NotFound(t *testing.T) {
	mock := &mockCarbideClient{
		getInstance: func(ctx context.Context, org string, instanceId string) (*bmm.Instance, *http.Response, error) {
			return nil, &http.Response{StatusCode: 404}, fmt.Errorf("not found")
		},
	}

	actuator, _ := newTestActuator(mock)
	instanceID := "test-instance-id"
	machine := createTestMachineWithStatus(validProviderSpec(), v1beta1.NvidiaCarbideMachineProviderStatus{
		InstanceID: &instanceID,
	})

	exists, err := actuator.Exists(context.Background(), machine)
	if err != nil {
		t.Fatalf("Exists() unexpected error for 404: %v", err)
	}
	if exists {
		t.Error("Exists() should return false for 404")
	}
}

func TestExists_InstanceExists(t *testing.T) {
	mock := &mockCarbideClient{
		getInstance: func(ctx context.Context, org string, instanceId string) (*bmm.Instance, *http.Response, error) {
			return testInstance(instanceId), &http.Response{StatusCode: 200}, nil
		},
	}

	actuator, _ := newTestActuator(mock)
	instanceID := "test-instance-id"
	machine := createTestMachineWithStatus(validProviderSpec(), v1beta1.NvidiaCarbideMachineProviderStatus{
		InstanceID: &instanceID,
	})

	exists, err := actuator.Exists(context.Background(), machine)
	if err != nil {
		t.Fatalf("Exists() unexpected error: %v", err)
	}
	if !exists {
		t.Error("Exists() should return true when instance exists")
	}
}

func TestExists_NoInstanceID(t *testing.T) {
	mock := &mockCarbideClient{}
	actuator, _ := newTestActuator(mock)
	machine := createTestMachine(validProviderSpec())

	exists, err := actuator.Exists(context.Background(), machine)
	if err != nil {
		t.Fatalf("Exists() unexpected error: %v", err)
	}
	if exists {
		t.Error("Exists() should return false when no instance ID is set")
	}
}

func TestDelete_AlreadyDeleted(t *testing.T) {
	mock := &mockCarbideClient{
		deleteInstance: func(ctx context.Context, org string, instanceId string) (*http.Response, error) {
			return &http.Response{StatusCode: 404}, fmt.Errorf("not found")
		},
	}

	actuator, _ := newTestActuator(mock)
	instanceID := "test-instance-id"
	machine := createTestMachineWithStatus(validProviderSpec(), v1beta1.NvidiaCarbideMachineProviderStatus{
		InstanceID: &instanceID,
	})

	err := actuator.Delete(context.Background(), machine)
	if err != nil {
		t.Fatalf("Delete() unexpected error for already-deleted instance: %v", err)
	}
}

func TestDelete_Success(t *testing.T) {
	mock := &mockCarbideClient{
		deleteInstance: func(ctx context.Context, org string, instanceId string) (*http.Response, error) {
			return &http.Response{StatusCode: 200}, nil
		},
	}

	actuator, _ := newTestActuator(mock)
	instanceID := "test-instance-id"
	machine := createTestMachineWithStatus(validProviderSpec(), v1beta1.NvidiaCarbideMachineProviderStatus{
		InstanceID: &instanceID,
	})

	err := actuator.Delete(context.Background(), machine)
	if err != nil {
		t.Fatalf("Delete() unexpected error: %v", err)
	}
}

func TestDelete_NoInstanceID(t *testing.T) {
	mock := &mockCarbideClient{}
	actuator, _ := newTestActuator(mock)
	machine := createTestMachine(validProviderSpec())

	err := actuator.Delete(context.Background(), machine)
	if err != nil {
		t.Fatalf("Delete() unexpected error when no instance ID: %v", err)
	}
}

func TestValidateProviderSpec(t *testing.T) {
	tests := []struct {
		name    string
		spec    v1beta1.NvidiaCarbideMachineProviderSpec
		wantErr bool
	}{
		{
			name:    "valid with instanceTypeId",
			spec:    validProviderSpec(),
			wantErr: false,
		},
		{
			name: "valid with machineId",
			spec: func() v1beta1.NvidiaCarbideMachineProviderSpec {
				s := validProviderSpec()
				s.InstanceTypeID = ""
				s.MachineID = "machine-id"
				return s
			}(),
			wantErr: false,
		},
		{
			name: "both instanceTypeId and machineId",
			spec: func() v1beta1.NvidiaCarbideMachineProviderSpec {
				s := validProviderSpec()
				s.MachineID = "machine-id"
				return s
			}(),
			wantErr: true,
		},
		{
			name: "neither instanceTypeId nor machineId",
			spec: func() v1beta1.NvidiaCarbideMachineProviderSpec {
				s := validProviderSpec()
				s.InstanceTypeID = ""
				return s
			}(),
			wantErr: true,
		},
		{
			name: "missing siteId",
			spec: func() v1beta1.NvidiaCarbideMachineProviderSpec {
				s := validProviderSpec()
				s.SiteID = ""
				return s
			}(),
			wantErr: true,
		},
		{
			name: "missing tenantId",
			spec: func() v1beta1.NvidiaCarbideMachineProviderSpec {
				s := validProviderSpec()
				s.TenantID = ""
				return s
			}(),
			wantErr: true,
		},
		{
			name: "missing vpcId",
			spec: func() v1beta1.NvidiaCarbideMachineProviderSpec {
				s := validProviderSpec()
				s.VpcID = ""
				return s
			}(),
			wantErr: true,
		},
		{
			name: "missing subnetId",
			spec: func() v1beta1.NvidiaCarbideMachineProviderSpec {
				s := validProviderSpec()
				s.SubnetID = ""
				return s
			}(),
			wantErr: true,
		},
		{
			name: "too many additional subnets",
			spec: func() v1beta1.NvidiaCarbideMachineProviderSpec {
				s := validProviderSpec()
				s.AdditionalSubnetIDs = make([]v1beta1.AdditionalSubnet, 11)
				return s
			}(),
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateProviderSpec(&tt.spec)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateProviderSpec() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestUpdate_StateTracking(t *testing.T) {
	instanceID := uuid.New().String()

	tests := []struct {
		name           string
		status         bmm.InstanceStatus
		expectReady    bool
		expectCondType string
	}{
		{"Pending", bmm.INSTANCESTATUS_PENDING, false, "InstanceAllocating"},
		{"Provisioning", bmm.INSTANCESTATUS_PROVISIONING, false, "InstanceProvisioning"},
		{"Configuring", bmm.INSTANCESTATUS_CONFIGURING, false, "InstanceBootstrapping"},
		{"Ready", bmm.INSTANCESTATUS_READY, true, "InstanceReady"},
		{"Terminating", bmm.INSTANCESTATUS_TERMINATING, false, "InstanceTerminating"},
		{"Error", bmm.INSTANCESTATUS_ERROR, false, "InstanceError"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mock := &mockCarbideClient{
				getInstance: func(ctx context.Context, org string, id string) (*bmm.Instance, *http.Response, error) {
					inst := testInstance(instanceID)
					inst.Status = tt.status.Ptr()
					return inst, &http.Response{StatusCode: 200}, nil
				},
			}

			providerStatus := v1beta1.NvidiaCarbideMachineProviderStatus{
				InstanceID: &instanceID,
			}
			machine := createTypedTestMachineWithStatus(validProviderSpec(), providerStatus)
			actuator, _ := newTestActuatorWithMachine(mock, machine)

			err := actuator.Update(context.Background(), machine)
			if err != nil {
				t.Fatalf("Update() unexpected error: %v", err)
			}
		})
	}
}

func TestUpdate_HealthIntegration(t *testing.T) {
	instanceID := uuid.New().String()
	machineID := "machine-123"

	mock := &mockCarbideClient{
		getInstance: func(ctx context.Context, org string, id string) (*bmm.Instance, *http.Response, error) {
			inst := testInstance(instanceID)
			return inst, &http.Response{StatusCode: 200}, nil
		},
		getMachine: func(ctx context.Context, org string, mid string) (*bmm.Machine, *http.Response, error) {
			return &bmm.Machine{
				Id: &machineID,
				Health: &bmm.MachineHealth{
					Alerts: []bmm.MachineHealthProbeAlert{
						{},
					},
				},
			}, &http.Response{StatusCode: 200}, nil
		},
	}

	providerStatus := v1beta1.NvidiaCarbideMachineProviderStatus{
		InstanceID: &instanceID,
	}
	machine := createTypedTestMachineWithStatus(validProviderSpec(), providerStatus)
	actuator, _ := newTestActuatorWithMachine(mock, machine)

	err := actuator.Update(context.Background(), machine)
	if err != nil {
		t.Fatalf("Update() unexpected error: %v", err)
	}
}

func TestUpdate_HealthyMachine(t *testing.T) {
	instanceID := uuid.New().String()
	machineID := "machine-123"

	mock := &mockCarbideClient{
		getInstance: func(ctx context.Context, org string, id string) (*bmm.Instance, *http.Response, error) {
			inst := testInstance(instanceID)
			return inst, &http.Response{StatusCode: 200}, nil
		},
		getMachine: func(ctx context.Context, org string, mid string) (*bmm.Machine, *http.Response, error) {
			return &bmm.Machine{
				Id: &machineID,
				Health: &bmm.MachineHealth{
					Alerts: []bmm.MachineHealthProbeAlert{},
				},
			}, &http.Response{StatusCode: 200}, nil
		},
	}

	providerStatus := v1beta1.NvidiaCarbideMachineProviderStatus{
		InstanceID: &instanceID,
	}
	machine := createTypedTestMachineWithStatus(validProviderSpec(), providerStatus)
	actuator, _ := newTestActuatorWithMachine(mock, machine)

	err := actuator.Update(context.Background(), machine)
	if err != nil {
		t.Fatalf("Update() unexpected error: %v", err)
	}
}

func createTypedTestMachineWithStatus(
	providerSpec v1beta1.NvidiaCarbideMachineProviderSpec,
	providerStatus v1beta1.NvidiaCarbideMachineProviderStatus,
) *machinev1beta1.Machine {
	specBytes, _ := json.Marshal(providerSpec)
	statusBytes, _ := json.Marshal(providerStatus)
	return &machinev1beta1.Machine{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-machine",
			Namespace: "default",
		},
		Spec: machinev1beta1.MachineSpec{
			ProviderSpec: machinev1beta1.ProviderSpec{
				Value: &runtime.RawExtension{Raw: specBytes},
			},
		},
		Status: machinev1beta1.MachineStatus{
			ProviderStatus: &runtime.RawExtension{Raw: statusBytes},
		},
	}
}

func TestProviderIDParsing(t *testing.T) {
	pid := providerid.NewProviderID("test-org", "test-tenant", "test-site", uuid.New())

	parsed, err := providerid.ParseProviderID(pid.String())
	if err != nil {
		t.Fatalf("Failed to parse provider ID: %v", err)
	}

	if parsed.OrgName != "test-org" {
		t.Errorf("Expected orgName=test-org, got %s", parsed.OrgName)
	}
	if parsed.TenantName != "test-tenant" {
		t.Errorf("Expected tenantName=test-tenant, got %s", parsed.TenantName)
	}
	if parsed.SiteName != "test-site" {
		t.Errorf("Expected siteName=test-site, got %s", parsed.SiteName)
	}
}
