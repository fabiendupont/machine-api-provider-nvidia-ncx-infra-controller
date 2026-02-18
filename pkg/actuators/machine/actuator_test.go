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
	"testing"

	"github.com/google/uuid"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	v1beta1 "github.com/fabiendupont/machine-api-provider-nvidia-carbide/pkg/apis/nvidiacarbideprovider/v1beta1"
	"github.com/fabiendupont/machine-api-provider-nvidia-carbide/pkg/providerid"
)

func TestActuator_Create(t *testing.T) {
	tests := []struct {
		name    string
		machine *unstructured.Unstructured
		wantErr bool
	}{
		{
			name: "successful instance creation",
			machine: createTestMachine(v1beta1.NvidiaCarbideMachineProviderSpec{
				SiteID:   "550e8400-e29b-41d4-a716-446655440000",
				TenantID: "660e8400-e29b-41d4-a716-446655440001",
				VpcID:    "770e8400-e29b-41d4-a716-446655440002",
				SubnetID: "880e8400-e29b-41d4-a716-446655440003",
				CredentialsSecret: v1beta1.CredentialsSecretReference{
					Name:      "nvidia-carbide-creds",
					Namespace: "default",
				},
			}),
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scheme := runtime.NewScheme()
			_ = corev1.AddToScheme(scheme)

			secret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "nvidia-carbide-creds",
					Namespace: "default",
				},
				Data: map[string][]byte{
					"endpoint": []byte("https://api.nvidia-carbide.test"),
					"orgName":  []byte("test-org"),
					"token":    []byte("test-token"),
				},
			}

			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(secret).
				Build()

			actuator := NewActuator(
				fakeClient,
				record.NewFakeRecorder(10),
			)

			// NOTE: This test currently cannot run end-to-end because:
			// 1. The getNvidiaCarbideClient() needs network access
			// 2. We don't have a mock client injector yet
			//
			// Future improvement: Add dependency injection for NVIDIA Carbide client
			// to enable full unit testing without network calls

			_ = actuator
			_ = tt.machine

			// err := actuator.Create(context.Background(), tt.machine)
			// if (err != nil) != tt.wantErr {
			// 	t.Errorf("Create() error = %v, wantErr %v", err, tt.wantErr)
			// }
		})
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

	// Embed provider spec
	providerSpecMap, _ := runtime.DefaultUnstructuredConverter.ToUnstructured(&providerSpec)
	_ = unstructured.SetNestedField(machine.Object, providerSpecMap, "spec", "providerSpec", "value")

	return machine
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
