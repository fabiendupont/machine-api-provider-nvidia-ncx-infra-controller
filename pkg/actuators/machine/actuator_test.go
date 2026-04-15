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
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	machinev1beta1 "github.com/openshift/api/machine/v1beta1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	nico "github.com/NVIDIA/ncx-infra-controller-rest/sdk/standard"
	v1beta1 "github.com/fabiendupont/machine-api-provider-nvidia-ncx-infra-controller/pkg/apis/nicoprovider/v1beta1"
	"github.com/fabiendupont/machine-api-provider-nvidia-ncx-infra-controller/pkg/providerid"
)

const (
	testInstanceID      = "test-instance-id"
	testMachineID       = "machine-123"
	testTargetMachineID = "target-machine-id"
)

// mockNicoClient implements NicoClientInterface for testing
type mockNicoClient struct {
	createInstance func(
		ctx context.Context, org string, req nico.InstanceCreateRequest,
	) (*nico.Instance, *http.Response, error)
	getInstance func(
		ctx context.Context, org string, instanceId string,
	) (*nico.Instance, *http.Response, error)
	deleteInstance func(
		ctx context.Context, org string, instanceId string,
		deleteReq *nico.InstanceDeleteRequest,
	) (*http.Response, error)
	getMachine func(
		ctx context.Context, org string, machineId string,
	) (*nico.Machine, *http.Response, error)
	updateInstance func(
		ctx context.Context, org string, instanceId string,
		req nico.InstanceUpdateRequest,
	) (*nico.Instance, *http.Response, error)
	getInstanceStatusHistory func(
		ctx context.Context, org string, instanceId string,
	) ([]nico.StatusDetail, *http.Response, error)
	listFaultEvents func(
		ctx context.Context, org string, machineId string, state string,
	) ([]nico.FaultEvent, *http.Response, error)
	ingestFaultEvent func(
		ctx context.Context, org string, req nico.FaultIngestionRequest,
	) (*nico.FaultEvent, *http.Response, error)
	getCurrentTenant func(
		ctx context.Context, org string,
	) (*nico.Tenant, *http.Response, error)
}

func (m *mockNicoClient) CreateInstance(
	ctx context.Context, org string, req nico.InstanceCreateRequest,
) (*nico.Instance, *http.Response, error) {
	return m.createInstance(ctx, org, req)
}

func (m *mockNicoClient) GetInstance(
	ctx context.Context, org string, instanceId string,
) (*nico.Instance, *http.Response, error) {
	return m.getInstance(ctx, org, instanceId)
}

func (m *mockNicoClient) DeleteInstance(
	ctx context.Context, org string, instanceId string,
	deleteReq *nico.InstanceDeleteRequest,
) (*http.Response, error) {
	return m.deleteInstance(ctx, org, instanceId, deleteReq)
}

func (m *mockNicoClient) GetMachine(
	ctx context.Context, org string, machineId string,
) (*nico.Machine, *http.Response, error) {
	if m.getMachine != nil {
		return m.getMachine(ctx, org, machineId)
	}
	return nil, &http.Response{StatusCode: 404}, fmt.Errorf("not found")
}

func (m *mockNicoClient) GetCurrentTenant(
	ctx context.Context, org string,
) (*nico.Tenant, *http.Response, error) {
	if m.getCurrentTenant != nil {
		return m.getCurrentTenant(ctx, org)
	}
	return nil, &http.Response{StatusCode: 200}, nil
}

func (m *mockNicoClient) GetInstanceStatusHistory(
	ctx context.Context, org string, instanceId string,
) ([]nico.StatusDetail, *http.Response, error) {
	if m.getInstanceStatusHistory != nil {
		return m.getInstanceStatusHistory(ctx, org, instanceId)
	}
	return nil, &http.Response{StatusCode: 200}, nil
}

func (m *mockNicoClient) UpdateInstance(
	ctx context.Context, org string, instanceId string,
	req nico.InstanceUpdateRequest,
) (*nico.Instance, *http.Response, error) {
	if m.updateInstance != nil {
		return m.updateInstance(ctx, org, instanceId, req)
	}
	return nil, &http.Response{StatusCode: 200}, nil
}

func (m *mockNicoClient) ListFaultEvents(
	ctx context.Context, org string, machineId string, state string,
) ([]nico.FaultEvent, *http.Response, error) {
	if m.listFaultEvents != nil {
		return m.listFaultEvents(ctx, org, machineId, state)
	}
	// Default: API unavailable (triggers JSONB fallback)
	return nil, &http.Response{StatusCode: 404}, fmt.Errorf("not found")
}

func (m *mockNicoClient) IngestFaultEvent(
	ctx context.Context, org string, req nico.FaultIngestionRequest,
) (*nico.FaultEvent, *http.Response, error) {
	if m.ingestFaultEvent != nil {
		return m.ingestFaultEvent(ctx, org, req)
	}
	return nil, &http.Response{StatusCode: 404}, fmt.Errorf("not found")
}

func newTestActuator(mock *mockNicoClient) *Actuator {
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	_ = machinev1beta1.AddToScheme(scheme)

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		Build()

	recorder := record.NewFakeRecorder(10)
	return NewActuatorWithClient(fakeClient, recorder, mock, "test-org")
}

func newTestActuatorWithMachine(
	mock *mockNicoClient, machine *machinev1beta1.Machine,
) (*Actuator, *record.FakeRecorder) {
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

func testInstance(id string) *nico.Instance {
	status := nico.INSTANCESTATUS_PROVISIONING
	machineId := nico.NewNullableString(ptr(testMachineID))
	return &nico.Instance{
		Id:        &id,
		Status:    &status,
		MachineId: *machineId,
		Interfaces: []nico.Interface{
			{
				IpAddresses: []string{"10.0.0.1"},
			},
		},
	}
}

func validProviderSpec() v1beta1.NicoMachineProviderSpec {
	return v1beta1.NicoMachineProviderSpec{
		SiteID:         "550e8400-e29b-41d4-a716-446655440000",
		TenantID:       "660e8400-e29b-41d4-a716-446655440001",
		InstanceTypeID: "990e8400-e29b-41d4-a716-446655440004",
		VpcID:          "770e8400-e29b-41d4-a716-446655440002",
		SubnetID:       "880e8400-e29b-41d4-a716-446655440003",
		CredentialsSecret: v1beta1.CredentialsSecretReference{
			Name:      "nico-creds",
			Namespace: "default",
		},
	}
}

func createTypedTestMachine(providerSpec v1beta1.NicoMachineProviderSpec) *machinev1beta1.Machine {
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

func createTestMachine(providerSpec v1beta1.NicoMachineProviderSpec) *unstructured.Unstructured {
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
	providerSpec v1beta1.NicoMachineProviderSpec,
	providerStatus v1beta1.NicoMachineProviderStatus,
) *unstructured.Unstructured {
	machine := createTestMachine(providerSpec)

	statusMap, _ := runtime.DefaultUnstructuredConverter.ToUnstructured(&providerStatus)
	_ = unstructured.SetNestedField(machine.Object, statusMap, "status", "providerStatus")

	return machine
}

func TestCreate_Success(t *testing.T) {
	instanceID := uuid.New().String()
	mock := &mockNicoClient{
		createInstance: func(
			ctx context.Context, org string, req nico.InstanceCreateRequest,
		) (*nico.Instance, *http.Response, error) {
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
	mock := &mockNicoClient{
		createInstance: func(
			ctx context.Context, org string, req nico.InstanceCreateRequest,
		) (*nico.Instance, *http.Response, error) {
			return nil, nil, fmt.Errorf("connection refused")
		},
	}

	actuator := newTestActuator(mock)
	machine := createTestMachine(validProviderSpec())

	err := actuator.Create(context.Background(), machine)
	if err == nil {
		t.Fatal("Create() expected error, got nil")
	}
}

func TestCreate_InvalidSpec(t *testing.T) {
	createCalled := false
	mock := &mockNicoClient{
		createInstance: func(
			ctx context.Context, org string, req nico.InstanceCreateRequest,
		) (*nico.Instance, *http.Response, error) {
			createCalled = true
			return nil, nil, nil
		},
	}

	actuator := newTestActuator(mock)

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
	mock := &mockNicoClient{}
	actuator := newTestActuator(mock)

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
	mock := &mockNicoClient{
		getInstance: func(ctx context.Context, org string, instanceId string) (*nico.Instance, *http.Response, error) {
			return nil, nil, fmt.Errorf("connection timeout")
		},
	}

	actuator := newTestActuator(mock)
	instanceID := testInstanceID
	machine := createTestMachineWithStatus(validProviderSpec(), v1beta1.NicoMachineProviderStatus{
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
	mock := &mockNicoClient{
		getInstance: func(ctx context.Context, org string, instanceId string) (*nico.Instance, *http.Response, error) {
			return nil, &http.Response{StatusCode: 404}, fmt.Errorf("not found")
		},
	}

	actuator := newTestActuator(mock)
	instanceID := testInstanceID
	machine := createTestMachineWithStatus(validProviderSpec(), v1beta1.NicoMachineProviderStatus{
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
	mock := &mockNicoClient{
		getInstance: func(ctx context.Context, org string, instanceId string) (*nico.Instance, *http.Response, error) {
			return testInstance(instanceId), &http.Response{StatusCode: 200}, nil
		},
	}

	actuator := newTestActuator(mock)
	instanceID := testInstanceID
	machine := createTestMachineWithStatus(validProviderSpec(), v1beta1.NicoMachineProviderStatus{
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
	mock := &mockNicoClient{}
	actuator := newTestActuator(mock)
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
	mock := &mockNicoClient{
		deleteInstance: func(
			ctx context.Context, org string, instanceId string,
			deleteReq *nico.InstanceDeleteRequest,
		) (*http.Response, error) {
			return &http.Response{StatusCode: 404}, fmt.Errorf("not found")
		},
	}

	actuator := newTestActuator(mock)
	instanceID := testInstanceID
	machine := createTestMachineWithStatus(validProviderSpec(), v1beta1.NicoMachineProviderStatus{
		InstanceID: &instanceID,
	})

	err := actuator.Delete(context.Background(), machine)
	if err != nil {
		t.Fatalf("Delete() unexpected error for already-deleted instance: %v", err)
	}
}

func TestDelete_Success(t *testing.T) {
	mock := &mockNicoClient{
		deleteInstance: func(
			ctx context.Context, org string, instanceId string,
			deleteReq *nico.InstanceDeleteRequest,
		) (*http.Response, error) {
			return &http.Response{StatusCode: 200}, nil
		},
	}

	actuator := newTestActuator(mock)
	instanceID := testInstanceID
	machine := createTestMachineWithStatus(validProviderSpec(), v1beta1.NicoMachineProviderStatus{
		InstanceID: &instanceID,
	})

	err := actuator.Delete(context.Background(), machine)
	if err != nil {
		t.Fatalf("Delete() unexpected error: %v", err)
	}
}

func TestDelete_NoInstanceID(t *testing.T) {
	mock := &mockNicoClient{}
	actuator := newTestActuator(mock)
	machine := createTestMachine(validProviderSpec())

	err := actuator.Delete(context.Background(), machine)
	if err != nil {
		t.Fatalf("Delete() unexpected error when no instance ID: %v", err)
	}
}

func TestValidateProviderSpec(t *testing.T) {
	tests := []struct {
		name    string
		spec    v1beta1.NicoMachineProviderSpec
		wantErr bool
	}{
		{
			name:    "valid with instanceTypeId",
			spec:    validProviderSpec(),
			wantErr: false,
		},
		{
			name: "valid with machineId",
			spec: func() v1beta1.NicoMachineProviderSpec {
				s := validProviderSpec()
				s.InstanceTypeID = ""
				s.MachineID = "machine-id"
				return s
			}(),
			wantErr: false,
		},
		{
			name: "both instanceTypeId and machineId",
			spec: func() v1beta1.NicoMachineProviderSpec {
				s := validProviderSpec()
				s.MachineID = "machine-id"
				return s
			}(),
			wantErr: true,
		},
		{
			name: "neither instanceTypeId nor machineId",
			spec: func() v1beta1.NicoMachineProviderSpec {
				s := validProviderSpec()
				s.InstanceTypeID = ""
				return s
			}(),
			wantErr: true,
		},
		{
			name: "missing siteId",
			spec: func() v1beta1.NicoMachineProviderSpec {
				s := validProviderSpec()
				s.SiteID = ""
				return s
			}(),
			wantErr: true,
		},
		{
			name: "missing tenantId",
			spec: func() v1beta1.NicoMachineProviderSpec {
				s := validProviderSpec()
				s.TenantID = ""
				return s
			}(),
			wantErr: true,
		},
		{
			name: "missing vpcId",
			spec: func() v1beta1.NicoMachineProviderSpec {
				s := validProviderSpec()
				s.VpcID = ""
				return s
			}(),
			wantErr: true,
		},
		{
			name: "missing subnetId",
			spec: func() v1beta1.NicoMachineProviderSpec {
				s := validProviderSpec()
				s.SubnetID = ""
				return s
			}(),
			wantErr: true,
		},
		{
			name: "too many additional subnets",
			spec: func() v1beta1.NicoMachineProviderSpec {
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
		status         nico.InstanceStatus
		expectReady    bool
		expectCondType string
	}{
		{"Pending", nico.INSTANCESTATUS_PENDING, false, "InstanceAllocating"},
		{"Provisioning", nico.INSTANCESTATUS_PROVISIONING, false, "InstanceProvisioning"},
		{"Configuring", nico.INSTANCESTATUS_CONFIGURING, false, "InstanceBootstrapping"},
		{"Ready", nico.INSTANCESTATUS_READY, true, "InstanceReady"},
		{"Terminating", nico.INSTANCESTATUS_TERMINATING, false, "InstanceTerminating"},
		{"Error", nico.INSTANCESTATUS_ERROR, false, "InstanceError"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mock := &mockNicoClient{
				getInstance: func(ctx context.Context, org string, id string) (*nico.Instance, *http.Response, error) {
					inst := testInstance(instanceID)
					inst.Status = tt.status.Ptr()
					return inst, &http.Response{StatusCode: 200}, nil
				},
			}

			providerStatus := v1beta1.NicoMachineProviderStatus{
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
	machineID := testMachineID

	mock := &mockNicoClient{
		getInstance: func(ctx context.Context, org string, id string) (*nico.Instance, *http.Response, error) {
			inst := testInstance(instanceID)
			return inst, &http.Response{StatusCode: 200}, nil
		},
		getMachine: func(ctx context.Context, org string, mid string) (*nico.Machine, *http.Response, error) {
			return &nico.Machine{
				Id: &machineID,
				Health: &nico.MachineHealth{
					Alerts: []nico.MachineHealthProbeAlert{
						{},
					},
				},
			}, &http.Response{StatusCode: 200}, nil
		},
	}

	providerStatus := v1beta1.NicoMachineProviderStatus{
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
	machineID := testMachineID

	mock := &mockNicoClient{
		getInstance: func(ctx context.Context, org string, id string) (*nico.Instance, *http.Response, error) {
			inst := testInstance(instanceID)
			return inst, &http.Response{StatusCode: 200}, nil
		},
		getMachine: func(ctx context.Context, org string, mid string) (*nico.Machine, *http.Response, error) {
			return &nico.Machine{
				Id: &machineID,
				Health: &nico.MachineHealth{
					Alerts: []nico.MachineHealthProbeAlert{},
				},
			}, &http.Response{StatusCode: 200}, nil
		},
	}

	providerStatus := v1beta1.NicoMachineProviderStatus{
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
	providerSpec v1beta1.NicoMachineProviderSpec,
	providerStatus v1beta1.NicoMachineProviderStatus,
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

func TestDelete_MHCRemediation(t *testing.T) {
	var capturedDeleteReq *nico.InstanceDeleteRequest
	mock := &mockNicoClient{
		deleteInstance: func(
			ctx context.Context, org string, instanceId string,
			deleteReq *nico.InstanceDeleteRequest,
		) (*http.Response, error) {
			capturedDeleteReq = deleteReq
			return &http.Response{StatusCode: 200}, nil
		},
	}

	actuator := newTestActuator(mock)
	instanceID := testInstanceID
	machine := createTestMachineWithStatus(validProviderSpec(), v1beta1.NicoMachineProviderStatus{
		InstanceID: &instanceID,
	})
	// Set the MHC remediation annotation
	machine.SetAnnotations(map[string]string{
		"machine.openshift.io/unhealthy": "",
	})

	err := actuator.Delete(context.Background(), machine)
	if err != nil {
		t.Fatalf("Delete() unexpected error: %v", err)
	}
	if capturedDeleteReq == nil {
		t.Fatal("Delete() should have passed InstanceDeleteRequest for MHC remediation")
	}
	if capturedDeleteReq.MachineHealthIssue == nil {
		t.Fatal("Delete() should have set MachineHealthIssue")
	}
	if *capturedDeleteReq.MachineHealthIssue.Category != "MachineHealthCheck" {
		t.Errorf("Expected category MachineHealthCheck, got %s", *capturedDeleteReq.MachineHealthIssue.Category)
	}
}

func TestDelete_NoMHCRemediation(t *testing.T) {
	var capturedDeleteReq *nico.InstanceDeleteRequest
	mock := &mockNicoClient{
		deleteInstance: func(
			ctx context.Context, org string, instanceId string,
			deleteReq *nico.InstanceDeleteRequest,
		) (*http.Response, error) {
			capturedDeleteReq = deleteReq
			return &http.Response{StatusCode: 200}, nil
		},
	}

	actuator := newTestActuator(mock)
	instanceID := testInstanceID
	machine := createTestMachineWithStatus(validProviderSpec(), v1beta1.NicoMachineProviderStatus{
		InstanceID: &instanceID,
	})

	err := actuator.Delete(context.Background(), machine)
	if err != nil {
		t.Fatalf("Delete() unexpected error: %v", err)
	}
	if capturedDeleteReq != nil {
		t.Error("Delete() should not have passed InstanceDeleteRequest without MHC annotation")
	}
}

func TestUpdate_StatusHistoryOnError(t *testing.T) {
	instanceID := uuid.New().String()
	errorStatus := nico.INSTANCESTATUS_ERROR

	mock := &mockNicoClient{
		getInstance: func(ctx context.Context, org string, id string) (*nico.Instance, *http.Response, error) {
			inst := testInstance(instanceID)
			inst.Status = &errorStatus
			return inst, &http.Response{StatusCode: 200}, nil
		},
		getInstanceStatusHistory: func(
			ctx context.Context, org string, id string,
		) ([]nico.StatusDetail, *http.Response, error) {
			now := time.Now()
			errorMsg := "Machine allocation failed"
			errorStr := string(nico.INSTANCESTATUS_ERROR)
			provStr := string(nico.INSTANCESTATUS_PROVISIONING)
			return []nico.StatusDetail{
				{Status: &provStr, Created: &now},
				{Status: &errorStr, Message: &errorMsg, Created: &now},
			}, &http.Response{StatusCode: 200}, nil
		},
	}

	providerStatus := v1beta1.NicoMachineProviderStatus{
		InstanceID: &instanceID,
	}
	machine := createTypedTestMachineWithStatus(validProviderSpec(), providerStatus)
	actuator, recorder := newTestActuatorWithMachine(mock, machine)

	err := actuator.Update(context.Background(), machine)
	if err != nil {
		t.Fatalf("Update() unexpected error: %v", err)
	}

	// Verify Warning events were recorded
	close(recorder.Events)
	eventCount := 0
	for range recorder.Events {
		eventCount++
	}
	if eventCount == 0 {
		t.Error("Expected Warning events for status history on Error state")
	}
}

func TestCreate_WithDpuExtensionServices(t *testing.T) {
	instanceID := uuid.New().String()
	updateCalled := false
	mock := &mockNicoClient{
		createInstance: func(
			ctx context.Context, org string, req nico.InstanceCreateRequest,
		) (*nico.Instance, *http.Response, error) {
			return testInstance(instanceID), &http.Response{StatusCode: 201}, nil
		},
		updateInstance: func(
			ctx context.Context, org string, id string,
			req nico.InstanceUpdateRequest,
		) (*nico.Instance, *http.Response, error) {
			updateCalled = true
			if len(req.DpuExtensionServiceDeployments) != 1 {
				t.Errorf("Expected 1 DPU deployment, got %d", len(req.DpuExtensionServiceDeployments))
			}
			return testInstance(instanceID), &http.Response{StatusCode: 200}, nil
		},
	}

	spec := validProviderSpec()
	spec.DpuExtensionServices = []v1beta1.DpuExtensionServiceSpec{
		{ServiceID: "aa0e8400-e29b-41d4-a716-446655440010", Version: "1.0.0"},
	}
	machine := createTypedTestMachine(spec)
	actuator, _ := newTestActuatorWithMachine(mock, machine)

	err := actuator.Create(context.Background(), machine)
	if err != nil {
		t.Fatalf("Create() unexpected error: %v", err)
	}
	if !updateCalled {
		t.Error("Create() should have called UpdateInstance for DPU extension services")
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

func TestCreate_HTTPStatusErrors(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		wantKind   APIErrorKind
	}{
		{"400 terminal", 400, ErrorTerminal},
		{"429 transient", 429, ErrorTransient},
		{"500 transient", 500, ErrorTransient},
		{"503 transient", 503, ErrorTransient},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mock := &mockNicoClient{
				createInstance: func(
					ctx context.Context, org string, req nico.InstanceCreateRequest,
				) (*nico.Instance, *http.Response, error) {
					return nil, &http.Response{
						StatusCode: tt.statusCode,
						Body:       io.NopCloser(strings.NewReader(`{"message":"test error"}`)),
					}, fmt.Errorf("API error %d", tt.statusCode)
				},
			}

			machine := createTypedTestMachine(validProviderSpec())
			actuator, _ := newTestActuatorWithMachine(mock, machine)

			err := actuator.Create(context.Background(), machine)
			if err == nil {
				t.Fatal("Create() expected error, got nil")
			}

			var classified *ClassifiedError
			if !errors.As(err, &classified) {
				t.Fatalf("expected ClassifiedError, got %T: %v", err, err)
			}
			if classified.Kind != tt.wantKind {
				t.Errorf("error kind = %d, want %d", classified.Kind, tt.wantKind)
			}
		})
	}
}

func TestDelete_APIError500(t *testing.T) {
	mock := &mockNicoClient{
		deleteInstance: func(
			ctx context.Context, org string, instanceId string,
			deleteReq *nico.InstanceDeleteRequest,
		) (*http.Response, error) {
			return &http.Response{StatusCode: 500}, fmt.Errorf("internal server error")
		},
	}

	actuator := newTestActuator(mock)
	instanceID := testInstanceID
	machine := createTestMachineWithStatus(validProviderSpec(), v1beta1.NicoMachineProviderStatus{
		InstanceID: &instanceID,
	})

	err := actuator.Delete(context.Background(), machine)
	if err == nil {
		t.Fatal("Delete() expected error for 500, got nil")
	}
}

func TestDelete_HTTP202Accepted(t *testing.T) {
	mock := &mockNicoClient{
		deleteInstance: func(
			ctx context.Context, org string, instanceId string,
			deleteReq *nico.InstanceDeleteRequest,
		) (*http.Response, error) {
			return &http.Response{StatusCode: 202}, nil
		},
	}

	actuator := newTestActuator(mock)
	instanceID := testInstanceID
	machine := createTestMachineWithStatus(validProviderSpec(), v1beta1.NicoMachineProviderStatus{
		InstanceID: &instanceID,
	})

	err := actuator.Delete(context.Background(), machine)
	if err != nil {
		t.Fatalf("Delete() unexpected error for 202: %v", err)
	}
}

func TestCreate_NilInstance(t *testing.T) {
	mock := &mockNicoClient{
		createInstance: func(
			ctx context.Context, org string, req nico.InstanceCreateRequest,
		) (*nico.Instance, *http.Response, error) {
			return nil, &http.Response{StatusCode: 201}, nil
		},
	}

	machine := createTypedTestMachine(validProviderSpec())
	actuator, _ := newTestActuatorWithMachine(mock, machine)

	err := actuator.Create(context.Background(), machine)
	if err == nil {
		t.Fatal("Create() expected error for nil instance, got nil")
	}
}

func TestUpdate_ProvisioningTimeout(t *testing.T) {
	// Temporarily reduce timeout for testing
	origTimeout := ProvisioningTimeout
	ProvisioningTimeout = 1 * time.Millisecond
	defer func() { ProvisioningTimeout = origTimeout }()

	instanceID := uuid.New().String()
	mock := &mockNicoClient{
		getInstance: func(ctx context.Context, org string, id string) (*nico.Instance, *http.Response, error) {
			inst := testInstance(instanceID)
			status := nico.INSTANCESTATUS_PROVISIONING
			inst.Status = &status
			return inst, &http.Response{StatusCode: 200}, nil
		},
	}

	// Set InstanceProvisioned condition with a timestamp in the past
	pastTime := metav1.NewTime(time.Now().Add(-1 * time.Hour))
	providerStatus := v1beta1.NicoMachineProviderStatus{
		InstanceID: &instanceID,
		Conditions: []metav1.Condition{
			{
				Type:               "InstanceProvisioned",
				Status:             metav1.ConditionTrue,
				Reason:             "InstanceCreated",
				Message:            "Instance created",
				LastTransitionTime: pastTime,
			},
		},
	}
	machine := createTypedTestMachineWithStatus(validProviderSpec(), providerStatus)
	actuator, recorder := newTestActuatorWithMachine(mock, machine)

	err := actuator.Update(context.Background(), machine)
	if err != nil {
		t.Fatalf("Update() unexpected error: %v", err)
	}

	// Check for ProvisioningTimeout event
	close(recorder.Events)
	foundTimeout := false
	for event := range recorder.Events {
		if strings.Contains(event, "ProvisioningTimeout") {
			foundTimeout = true
		}
	}
	if !foundTimeout {
		t.Error("Expected ProvisioningTimeout event")
	}

	// Verify ErrorReason is set on the Machine
	if machine.Status.ErrorReason == nil {
		t.Error("Expected ErrorReason to be set")
	}
}

func TestClassifyAlerts(t *testing.T) {
	tests := []struct {
		name         string
		alerts       []nico.MachineHealthProbeAlert
		wantCritical int
		wantWarning  int
	}{
		{
			name:         "no alerts",
			alerts:       nil,
			wantCritical: 0,
			wantWarning:  0,
		},
		{
			name: "critical classification",
			alerts: []nico.MachineHealthProbeAlert{
				{Classifications: []string{severityCritical}},
			},
			wantCritical: 1,
			wantWarning:  0,
		},
		{
			name: "warning classification",
			alerts: []nico.MachineHealthProbeAlert{
				{Classifications: []string{severityWarning}},
			},
			wantCritical: 0,
			wantWarning:  1,
		},
		{
			name: "no classification defaults to critical",
			alerts: []nico.MachineHealthProbeAlert{
				{Classifications: nil},
			},
			wantCritical: 1,
			wantWarning:  0,
		},
		{
			name: "mixed classifications",
			alerts: []nico.MachineHealthProbeAlert{
				{Classifications: []string{severityCritical}},
				{Classifications: []string{severityWarning}},
				{Classifications: []string{"unknown-type"}},
			},
			wantCritical: 2,
			wantWarning:  1,
		},
		{
			name: "alert with both critical and warning is critical",
			alerts: []nico.MachineHealthProbeAlert{
				{Classifications: []string{severityWarning, severityCritical}},
			},
			wantCritical: 1,
			wantWarning:  0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			critical, warning := classifyAlerts(tt.alerts)
			if len(critical) != tt.wantCritical {
				t.Errorf("critical count = %d, want %d", len(critical), tt.wantCritical)
			}
			if len(warning) != tt.wantWarning {
				t.Errorf("warning count = %d, want %d", len(warning), tt.wantWarning)
			}
		})
	}
}

func TestUpdate_HealthClassification_Critical(t *testing.T) {
	instanceID := uuid.New().String()
	machineID := testMachineID

	mock := &mockNicoClient{
		getInstance: func(ctx context.Context, org string, id string) (*nico.Instance, *http.Response, error) {
			inst := testInstance(instanceID)
			return inst, &http.Response{StatusCode: 200}, nil
		},
		getMachine: func(ctx context.Context, org string, mid string) (*nico.Machine, *http.Response, error) {
			msg := "GPU memory ECC error"
			return &nico.Machine{
				Id: &machineID,
				Health: &nico.MachineHealth{
					Alerts: []nico.MachineHealthProbeAlert{
						{
							Message:         &msg,
							Classifications: []string{severityCritical},
						},
					},
				},
			}, &http.Response{StatusCode: 200}, nil
		},
	}

	providerStatus := v1beta1.NicoMachineProviderStatus{InstanceID: &instanceID}
	machine := createTypedTestMachineWithStatus(validProviderSpec(), providerStatus)
	actuator, _ := newTestActuatorWithMachine(mock, machine)

	err := actuator.Update(context.Background(), machine)
	if err != nil {
		t.Fatalf("Update() unexpected error: %v", err)
	}
}

func TestUpdate_HealthClassification_WarningOnly(t *testing.T) {
	instanceID := uuid.New().String()
	machineID := testMachineID

	mock := &mockNicoClient{
		getInstance: func(ctx context.Context, org string, id string) (*nico.Instance, *http.Response, error) {
			inst := testInstance(instanceID)
			return inst, &http.Response{StatusCode: 200}, nil
		},
		getMachine: func(ctx context.Context, org string, mid string) (*nico.Machine, *http.Response, error) {
			return &nico.Machine{
				Id: &machineID,
				Health: &nico.MachineHealth{
					Alerts: []nico.MachineHealthProbeAlert{
						{Classifications: []string{severityWarning}},
					},
				},
			}, &http.Response{StatusCode: 200}, nil
		},
	}

	providerStatus := v1beta1.NicoMachineProviderStatus{InstanceID: &instanceID}
	machine := createTypedTestMachineWithStatus(validProviderSpec(), providerStatus)
	actuator, _ := newTestActuatorWithMachine(mock, machine)

	err := actuator.Update(context.Background(), machine)
	if err != nil {
		t.Fatalf("Update() unexpected error: %v", err)
	}
}

func TestUpdate_NicoFaultRemediation(t *testing.T) {
	instanceID := uuid.New().String()
	machineID := testMachineID

	mock := &mockNicoClient{
		getInstance: func(ctx context.Context, org string, id string) (*nico.Instance, *http.Response, error) {
			inst := testInstance(instanceID)
			return inst, &http.Response{StatusCode: 200}, nil
		},
		getMachine: func(ctx context.Context, org string, mid string) (*nico.Machine, *http.Response, error) {
			msg := "GPU reset in progress"
			return &nico.Machine{
				Id: &machineID,
				Health: &nico.MachineHealth{
					Alerts: []nico.MachineHealthProbeAlert{
						{
							Message:         &msg,
							Classifications: []string{severityCritical, severityRemediating},
						},
					},
				},
			}, &http.Response{StatusCode: 200}, nil
		},
	}

	providerStatus := v1beta1.NicoMachineProviderStatus{InstanceID: &instanceID}
	machine := createTypedTestMachineWithStatus(validProviderSpec(), providerStatus)
	actuator, _ := newTestActuatorWithMachine(mock, machine)

	err := actuator.Update(context.Background(), machine)
	if err != nil {
		t.Fatalf("Update() unexpected error: %v", err)
	}
}

func TestDelete_MHCRemediation_EnrichedDetails(t *testing.T) {
	var capturedDeleteReq *nico.InstanceDeleteRequest
	mock := &mockNicoClient{
		deleteInstance: func(
			ctx context.Context, org string, instanceId string,
			deleteReq *nico.InstanceDeleteRequest,
		) (*http.Response, error) {
			capturedDeleteReq = deleteReq
			return &http.Response{StatusCode: 200}, nil
		},
	}

	actuator := newTestActuator(mock)
	instanceID := testInstanceID
	machine := createTestMachineWithStatus(validProviderSpec(), v1beta1.NicoMachineProviderStatus{
		InstanceID: &instanceID,
	})
	machine.SetAnnotations(map[string]string{
		"machine.openshift.io/unhealthy": "",
	})

	err := actuator.Delete(context.Background(), machine)
	if err != nil {
		t.Fatalf("Delete() unexpected error: %v", err)
	}
	if capturedDeleteReq == nil || capturedDeleteReq.MachineHealthIssue == nil {
		t.Fatal("Delete() should have set MachineHealthIssue")
	}

	// Verify enriched summary includes machine name
	summary := *capturedDeleteReq.MachineHealthIssue.Summary
	if !strings.Contains(summary, "test-machine") {
		t.Errorf("Expected summary to contain machine name, got: %s", summary)
	}

	// Verify details contain structured metadata
	details := capturedDeleteReq.MachineHealthIssue.Details.Get()
	if details == nil {
		t.Fatal("Expected Details to be set")
	}
	if !strings.Contains(*details, "machine_name") {
		t.Errorf("Expected details to contain machine_name, got: %s", *details)
	}
	if !strings.Contains(*details, "detected_at") {
		t.Errorf("Expected details to contain detected_at, got: %s", *details)
	}
}

func TestCreate_PreFlightHealthCheck_BlocksCreation(t *testing.T) {
	createCalled := false
	msg := "GPU memory ECC error"
	mock := &mockNicoClient{
		createInstance: func(
			ctx context.Context, org string, req nico.InstanceCreateRequest,
		) (*nico.Instance, *http.Response, error) {
			createCalled = true
			return testInstance(uuid.New().String()), &http.Response{StatusCode: 201}, nil
		},
		getMachine: func(ctx context.Context, org string, mid string) (*nico.Machine, *http.Response, error) {
			return &nico.Machine{
				Id: &mid,
				Health: &nico.MachineHealth{
					Alerts: []nico.MachineHealthProbeAlert{
						{
							Message:         &msg,
							Classifications: []string{severityCritical},
						},
					},
				},
			}, &http.Response{StatusCode: 200}, nil
		},
	}

	spec := validProviderSpec()
	spec.InstanceTypeID = ""
	spec.MachineID = testTargetMachineID

	machine := createTypedTestMachine(spec)
	actuator, recorder := newTestActuatorWithMachine(mock, machine)

	err := actuator.Create(context.Background(), machine)
	if err == nil {
		t.Fatal("Create() expected error when pre-flight health check fails")
	}
	if createCalled {
		t.Error("Create() should not have called CreateInstance when pre-flight health check fails")
	}
	if !strings.Contains(err.Error(), "critical health faults") {
		t.Errorf("Expected error about critical health faults, got: %v", err)
	}

	// Check for FaultBlockedCreation event
	close(recorder.Events)
	foundEvent := false
	for event := range recorder.Events {
		if strings.Contains(event, "FaultBlockedCreation") {
			foundEvent = true
		}
	}
	if !foundEvent {
		t.Error("Expected FaultBlockedCreation event")
	}
}

func TestCreate_PreFlightHealthCheck_AllowsHealthyMachine(t *testing.T) {
	instanceID := uuid.New().String()
	mock := &mockNicoClient{
		createInstance: func(
			ctx context.Context, org string, req nico.InstanceCreateRequest,
		) (*nico.Instance, *http.Response, error) {
			return testInstance(instanceID), &http.Response{StatusCode: 201}, nil
		},
		getMachine: func(ctx context.Context, org string, mid string) (*nico.Machine, *http.Response, error) {
			return &nico.Machine{
				Id: &mid,
				Health: &nico.MachineHealth{
					Alerts: []nico.MachineHealthProbeAlert{},
				},
			}, &http.Response{StatusCode: 200}, nil
		},
	}

	spec := validProviderSpec()
	spec.InstanceTypeID = ""
	spec.MachineID = testTargetMachineID

	machine := createTypedTestMachine(spec)
	actuator, _ := newTestActuatorWithMachine(mock, machine)

	err := actuator.Create(context.Background(), machine)
	if err != nil {
		t.Fatalf("Create() unexpected error: %v", err)
	}
}

func TestCreate_PreFlightHealthCheck_SkippedWithAllowUnhealthy(t *testing.T) {
	instanceID := uuid.New().String()
	getMachineCalled := false
	mock := &mockNicoClient{
		createInstance: func(
			ctx context.Context, org string, req nico.InstanceCreateRequest,
		) (*nico.Instance, *http.Response, error) {
			return testInstance(instanceID), &http.Response{StatusCode: 201}, nil
		},
		getMachine: func(ctx context.Context, org string, mid string) (*nico.Machine, *http.Response, error) {
			getMachineCalled = true
			return nil, &http.Response{StatusCode: 200}, nil
		},
	}

	spec := validProviderSpec()
	spec.InstanceTypeID = ""
	spec.MachineID = testTargetMachineID
	spec.AllowUnhealthyMachine = true

	machine := createTypedTestMachine(spec)
	actuator, _ := newTestActuatorWithMachine(mock, machine)

	err := actuator.Create(context.Background(), machine)
	if err != nil {
		t.Fatalf("Create() unexpected error: %v", err)
	}
	if getMachineCalled {
		t.Error("Create() should not have called GetMachine when AllowUnhealthyMachine is set")
	}
}

func TestCreate_PreFlightHealthCheck_SkippedForInstanceType(t *testing.T) {
	instanceID := uuid.New().String()
	getMachineCalled := false
	mock := &mockNicoClient{
		createInstance: func(
			ctx context.Context, org string, req nico.InstanceCreateRequest,
		) (*nico.Instance, *http.Response, error) {
			return testInstance(instanceID), &http.Response{StatusCode: 201}, nil
		},
		getMachine: func(ctx context.Context, org string, mid string) (*nico.Machine, *http.Response, error) {
			getMachineCalled = true
			return nil, &http.Response{StatusCode: 200}, nil
		},
	}

	// Standard spec uses InstanceTypeID, not MachineID
	machine := createTypedTestMachine(validProviderSpec())
	actuator, _ := newTestActuatorWithMachine(mock, machine)

	err := actuator.Create(context.Background(), machine)
	if err != nil {
		t.Fatalf("Create() unexpected error: %v", err)
	}
	if getMachineCalled {
		t.Error("Create() should not have called GetMachine for instanceTypeId-based provisioning")
	}
}

func TestCreate_PreFlightHealthCheck_FailureReasonAfterMaxAttempts(t *testing.T) {
	origMax := MaxFaultBlockedAttempts
	MaxFaultBlockedAttempts = 2
	defer func() { MaxFaultBlockedAttempts = origMax }()

	msg := "Persistent GPU fault"
	mock := &mockNicoClient{
		createInstance: func(
			ctx context.Context, org string, req nico.InstanceCreateRequest,
		) (*nico.Instance, *http.Response, error) {
			return nil, nil, fmt.Errorf("should not be called")
		},
		getMachine: func(ctx context.Context, org string, mid string) (*nico.Machine, *http.Response, error) {
			return &nico.Machine{
				Id: &mid,
				Health: &nico.MachineHealth{
					Alerts: []nico.MachineHealthProbeAlert{
						{
							Message:         &msg,
							Classifications: []string{severityCritical},
						},
					},
				},
			}, &http.Response{StatusCode: 200}, nil
		},
	}

	spec := validProviderSpec()
	spec.InstanceTypeID = ""
	spec.MachineID = testTargetMachineID

	// First attempt: set FaultBlocked_1 condition
	machine := createTypedTestMachine(spec)
	actuator, _ := newTestActuatorWithMachine(mock, machine)

	err := actuator.Create(context.Background(), machine)
	if err == nil {
		t.Fatal("Create() expected error on first attempt")
	}

	// Second attempt: should set FailureReason (MaxFaultBlockedAttempts=2)
	// Re-read the machine to get updated status with FaultBlocked_1 condition
	err = actuator.Create(context.Background(), machine)
	if err == nil {
		t.Fatal("Create() expected error on second attempt")
	}

	if machine.Status.ErrorReason == nil {
		t.Error("Expected ErrorReason to be set after max attempts")
	} else if string(*machine.Status.ErrorReason) != "PreFlightHealthCheckFailed" {
		t.Errorf("Expected ErrorReason=PreFlightHealthCheckFailed, got %s", string(*machine.Status.ErrorReason))
	}
}

func tenantWithFaultManagement() func(context.Context, string) (*nico.Tenant, *http.Response, error) {
	return func(ctx context.Context, org string) (*nico.Tenant, *http.Response, error) {
		caps := &nico.TenantCapabilities{}
		caps.SetFaultManagement(true)
		return &nico.Tenant{Capabilities: caps}, &http.Response{StatusCode: 200}, nil
	}
}

func TestUpdate_HealthFromFaultEventsAPI(t *testing.T) {
	instanceID := uuid.New().String()

	mock := &mockNicoClient{
		getCurrentTenant: tenantWithFaultManagement(),
		getInstance: func(ctx context.Context, org string, id string) (*nico.Instance, *http.Response, error) {
			inst := testInstance(instanceID)
			return inst, &http.Response{StatusCode: 200}, nil
		},
		listFaultEvents: func(
			ctx context.Context, org, machineId, state string,
		) ([]nico.FaultEvent, *http.Response, error) {
			sev := severityCritical
			msg := "GPU ECC uncorrectable error"
			cls := "gpu-ecc-error"
			st := "open"
			return []nico.FaultEvent{
				{Severity: &sev, Message: &msg, Classification: &cls, State: &st},
			}, &http.Response{StatusCode: 200}, nil
		},
	}

	providerStatus := v1beta1.NicoMachineProviderStatus{InstanceID: &instanceID}
	machine := createTypedTestMachineWithStatus(validProviderSpec(), providerStatus)
	actuator, _ := newTestActuatorWithMachine(mock, machine)

	err := actuator.Update(context.Background(), machine)
	if err != nil {
		t.Fatalf("Update() unexpected error: %v", err)
	}
}

func TestUpdate_HealthFromFaultEventsAPI_Remediating(t *testing.T) {
	instanceID := uuid.New().String()

	mock := &mockNicoClient{
		getCurrentTenant: tenantWithFaultManagement(),
		getInstance: func(ctx context.Context, org string, id string) (*nico.Instance, *http.Response, error) {
			inst := testInstance(instanceID)
			return inst, &http.Response{StatusCode: 200}, nil
		},
		listFaultEvents: func(
			ctx context.Context, org, machineId, state string,
		) ([]nico.FaultEvent, *http.Response, error) {
			sev := severityCritical
			msg := "GPU reset in progress"
			st := severityRemediating
			return []nico.FaultEvent{
				{Severity: &sev, Message: &msg, State: &st},
			}, &http.Response{StatusCode: 200}, nil
		},
	}

	providerStatus := v1beta1.NicoMachineProviderStatus{InstanceID: &instanceID}
	machine := createTypedTestMachineWithStatus(validProviderSpec(), providerStatus)
	actuator, _ := newTestActuatorWithMachine(mock, machine)

	err := actuator.Update(context.Background(), machine)
	if err != nil {
		t.Fatalf("Update() unexpected error: %v", err)
	}
}

func TestUpdate_HealthFromFaultEventsAPI_NoFaults(t *testing.T) {
	instanceID := uuid.New().String()

	mock := &mockNicoClient{
		getCurrentTenant: tenantWithFaultManagement(),
		getInstance: func(ctx context.Context, org string, id string) (*nico.Instance, *http.Response, error) {
			inst := testInstance(instanceID)
			return inst, &http.Response{StatusCode: 200}, nil
		},
		listFaultEvents: func(
			ctx context.Context, org, machineId, state string,
		) ([]nico.FaultEvent, *http.Response, error) {
			return []nico.FaultEvent{}, &http.Response{StatusCode: 200}, nil
		},
	}

	providerStatus := v1beta1.NicoMachineProviderStatus{InstanceID: &instanceID}
	machine := createTypedTestMachineWithStatus(validProviderSpec(), providerStatus)
	actuator, _ := newTestActuatorWithMachine(mock, machine)

	err := actuator.Update(context.Background(), machine)
	if err != nil {
		t.Fatalf("Update() unexpected error: %v", err)
	}
}

func TestDelete_MHCRemediation_IngestsFaultEvent(t *testing.T) {
	var capturedIngestReq *nico.FaultIngestionRequest
	mock := &mockNicoClient{
		getCurrentTenant: tenantWithFaultManagement(),
		deleteInstance: func(
			ctx context.Context, org string, instanceId string,
			deleteReq *nico.InstanceDeleteRequest,
		) (*http.Response, error) {
			return &http.Response{StatusCode: 200}, nil
		},
		ingestFaultEvent: func(
			ctx context.Context, org string, req nico.FaultIngestionRequest,
		) (*nico.FaultEvent, *http.Response, error) {
			capturedIngestReq = &req
			id := "fault-123"
			return &nico.FaultEvent{Id: &id}, &http.Response{StatusCode: 201}, nil
		},
	}

	actuator := newTestActuator(mock)
	instanceID := testInstanceID
	machineID := "physical-machine-id"
	machine := createTestMachineWithStatus(validProviderSpec(), v1beta1.NicoMachineProviderStatus{
		InstanceID: &instanceID,
		MachineID:  &machineID,
	})
	machine.SetAnnotations(map[string]string{
		"machine.openshift.io/unhealthy": "",
	})

	err := actuator.Delete(context.Background(), machine)
	if err != nil {
		t.Fatalf("Delete() unexpected error: %v", err)
	}
	if capturedIngestReq == nil {
		t.Fatal("Delete() should have called IngestFaultEvent for MHC remediation")
	}
	if capturedIngestReq.Source != "k8s-mhc" {
		t.Errorf("Expected source=k8s-mhc, got %s", capturedIngestReq.Source)
	}
	if capturedIngestReq.Severity != severityCritical {
		t.Errorf("Expected severity=critical, got %s", capturedIngestReq.Severity)
	}
	if capturedIngestReq.MachineId == nil || *capturedIngestReq.MachineId != machineID {
		t.Errorf("Expected machineId=%s, got %v", machineID, capturedIngestReq.MachineId)
	}
	if !strings.Contains(capturedIngestReq.Message, "test-machine") {
		t.Errorf("Expected message to contain machine name, got: %s", capturedIngestReq.Message)
	}
}

func TestCreate_PreFlightHealthCheck_UsesFaultEventsAPI(t *testing.T) {
	createCalled := false
	mock := &mockNicoClient{
		getCurrentTenant: tenantWithFaultManagement(),
		createInstance: func(
			ctx context.Context, org string, req nico.InstanceCreateRequest,
		) (*nico.Instance, *http.Response, error) {
			createCalled = true
			return testInstance(uuid.New().String()), &http.Response{StatusCode: 201}, nil
		},
		listFaultEvents: func(
			ctx context.Context, org, machineId, state string,
		) ([]nico.FaultEvent, *http.Response, error) {
			sev := severityCritical
			msg := "Persistent GPU fault"
			st := "open"
			return []nico.FaultEvent{
				{Severity: &sev, Message: &msg, State: &st},
			}, &http.Response{StatusCode: 200}, nil
		},
	}

	spec := validProviderSpec()
	spec.InstanceTypeID = ""
	spec.MachineID = testTargetMachineID

	machine := createTypedTestMachine(spec)
	actuator, _ := newTestActuatorWithMachine(mock, machine)

	err := actuator.Create(context.Background(), machine)
	if err == nil {
		t.Fatal("Create() expected error when fault events API reports critical faults")
	}
	if createCalled {
		t.Error("Create() should not have called CreateInstance")
	}
}

func TestCreate_PreFlightHealthCheck_WarningOnlyAllowsCreation(t *testing.T) {
	instanceID := uuid.New().String()
	mock := &mockNicoClient{
		createInstance: func(
			ctx context.Context, org string, req nico.InstanceCreateRequest,
		) (*nico.Instance, *http.Response, error) {
			return testInstance(instanceID), &http.Response{StatusCode: 201}, nil
		},
		getMachine: func(ctx context.Context, org string, mid string) (*nico.Machine, *http.Response, error) {
			return &nico.Machine{
				Id: &mid,
				Health: &nico.MachineHealth{
					Alerts: []nico.MachineHealthProbeAlert{
						{Classifications: []string{severityWarning}},
					},
				},
			}, &http.Response{StatusCode: 200}, nil
		},
	}

	spec := validProviderSpec()
	spec.InstanceTypeID = ""
	spec.MachineID = testTargetMachineID

	machine := createTypedTestMachine(spec)
	actuator, _ := newTestActuatorWithMachine(mock, machine)

	err := actuator.Create(context.Background(), machine)
	if err != nil {
		t.Fatalf("Create() unexpected error: %v", err)
	}
}
