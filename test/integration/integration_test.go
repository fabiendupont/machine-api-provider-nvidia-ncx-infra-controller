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

package integration

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	machinev1 "github.com/openshift/api/machine/v1beta1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	"github.com/fabiendupont/machine-api-provider-nvidia-carbide/pkg/actuators/machine"
	v1beta1 "github.com/fabiendupont/machine-api-provider-nvidia-carbide/pkg/apis/nvidiacarbideprovider/v1beta1"
	bmm "github.com/nvidia/bare-metal-manager-rest/sdk/standard"
)

var (
	cfg       *rest.Config
	k8sClient client.Client
	testEnv   *envtest.Environment
	ctx       context.Context
	cancel    context.CancelFunc
	actuator  *machine.Actuator
)

func TestIntegration(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Machine API Integration Suite")
}

var _ = BeforeSuite(func() {
	logf.SetLogger(zap.New(zap.WriteTo(GinkgoWriter), zap.UseDevMode(true)))

	ctx, cancel = context.WithCancel(context.TODO())

	By("bootstrapping test environment")
	testEnv = &envtest.Environment{
		CRDDirectoryPaths: []string{
			filepath.Join("..", "..", "config", "crd", "external"),
		},
		ErrorIfCRDPathMissing: true,
	}

	var err error
	cfg, err = testEnv.Start()
	Expect(err).NotTo(HaveOccurred())
	Expect(cfg).NotTo(BeNil())

	err = machinev1.Install(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	err = v1beta1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	k8sClient, err = client.New(cfg, client.Options{Scheme: scheme.Scheme})
	Expect(err).NotTo(HaveOccurred())
	Expect(k8sClient).NotTo(BeNil())

	// Create actuator with mock client
	mockClient := &mockNvidiaCarbideClient{}
	actuator = machine.NewActuatorWithClient(k8sClient, nil, mockClient, "test-org")
})

var _ = AfterSuite(func() {
	cancel()
	By("tearing down the test environment")
	err := testEnv.Stop()
	Expect(err).NotTo(HaveOccurred())
})

// mockHTTPResponse creates a mock HTTP response with the given status code
func mockHTTPResponse(statusCode int) *http.Response {
	return &http.Response{
		StatusCode: statusCode,
		Body:       io.NopCloser(bytes.NewReader([]byte{})),
		Header:     make(http.Header),
	}
}

// mockNvidiaCarbideClient for testing
type mockNvidiaCarbideClient struct {
	createInstanceFunc func(
		ctx context.Context, org string, req bmm.InstanceCreateRequest,
	) (*bmm.Instance, *http.Response, error)
	getInstanceFunc    func(ctx context.Context, org string, instanceId string) (*bmm.Instance, *http.Response, error)
	deleteInstanceFunc func(ctx context.Context, org string, instanceId string) (*http.Response, error)
}

func (m *mockNvidiaCarbideClient) CreateInstance(
	ctx context.Context, org string, req bmm.InstanceCreateRequest,
) (*bmm.Instance, *http.Response, error) {
	if m.createInstanceFunc != nil {
		return m.createInstanceFunc(ctx, org, req)
	}
	// Default: return success
	instanceID := uuid.New().String()
	return &bmm.Instance{
		Id:   &instanceID,
		Name: &req.Name,
	}, mockHTTPResponse(201), nil
}

func (m *mockNvidiaCarbideClient) GetInstance(
	ctx context.Context, org string, instanceId string,
) (*bmm.Instance, *http.Response, error) {
	if m.getInstanceFunc != nil {
		return m.getInstanceFunc(ctx, org, instanceId)
	}
	return &bmm.Instance{
		Id:   &instanceId,
		Name: ptr("test-instance"),
	}, mockHTTPResponse(200), nil
}

func (m *mockNvidiaCarbideClient) DeleteInstance(
	ctx context.Context, org string, instanceId string,
) (*http.Response, error) {
	if m.deleteInstanceFunc != nil {
		return m.deleteInstanceFunc(ctx, org, instanceId)
	}
	return mockHTTPResponse(204), nil
}

var _ = Describe("Machine Actuator Integration", func() {
	var (
		namespace *corev1.Namespace
		machine   *unstructured.Unstructured
		secret    *corev1.Secret
	)

	BeforeEach(func() {
		// Create test namespace
		namespace = &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				GenerateName: "test-",
			},
		}
		Expect(k8sClient.Create(ctx, namespace)).To(Succeed())

		// Create credentials secret
		secret = &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "nvidia-carbide-creds",
				Namespace: namespace.Name,
			},
			Data: map[string][]byte{
				"endpoint": []byte("https://api.nvidia-carbide.test"),
				"orgName":  []byte("test-org"),
				"token":    []byte("test-token"),
			},
		}
		Expect(k8sClient.Create(ctx, secret)).To(Succeed())

		// Create Machine with NvidiaCarbideMachineProviderSpec
		providerSpec := v1beta1.NvidiaCarbideMachineProviderSpec{
			SiteID:   "8a880c71-fe4b-4e43-9e24-ebfcb8a84c5f",
			TenantID: "b013708a-99f0-47b2-a630-cabb4ae1d3df",
			VpcID:    "9bb2d7d0-a017-4018-a212-a3d6b38e4ec9",
			SubnetID: "63e3909a-dfae-4b8e-8090-3269c5d2a2da",
			CredentialsSecret: v1beta1.CredentialsSecretReference{
				Name:      secret.Name,
				Namespace: namespace.Name,
			},
		}

		machine = createTestMachine("test-machine", namespace.Name, providerSpec)
		Expect(k8sClient.Create(ctx, machine)).To(Succeed())
	})

	AfterEach(func() {
		Expect(k8sClient.Delete(ctx, namespace)).To(Succeed())
	})

	It("should create an instance via actuator", func() {
		err := actuator.Create(ctx, machine)
		Expect(err).NotTo(HaveOccurred())

		// Verify provider spec was updated with instance ID
		Eventually(func() string {
			updated := &unstructured.Unstructured{}
			updated.SetGroupVersionKind(machine.GroupVersionKind())
			err := k8sClient.Get(ctx, client.ObjectKeyFromObject(machine), updated)
			if err != nil {
				return ""
			}

			status, _, _ := unstructured.NestedMap(updated.Object, "status", "providerStatus")
			if status == nil {
				return ""
			}

			instanceID, _, _ := unstructured.NestedString(status, "instanceId")
			return instanceID
		}, 5*time.Second, 500*time.Millisecond).ShouldNot(BeEmpty())
	})

	It("should check if instance exists", func() {
		// First create
		err := actuator.Create(ctx, machine)
		Expect(err).NotTo(HaveOccurred())

		// Then check existence
		exists, err := actuator.Exists(ctx, machine)
		Expect(err).NotTo(HaveOccurred())
		Expect(exists).To(BeTrue())
	})

	It("should delete an instance", func() {
		// Create first
		err := actuator.Create(ctx, machine)
		Expect(err).NotTo(HaveOccurred())

		// Then delete
		err = actuator.Delete(ctx, machine)
		Expect(err).NotTo(HaveOccurred())
	})

	It("should update instance information", func() {
		// Create first
		err := actuator.Create(ctx, machine)
		Expect(err).NotTo(HaveOccurred())

		// Update
		err = actuator.Update(ctx, machine)
		Expect(err).NotTo(HaveOccurred())
	})
})

func createTestMachine(
	name, namespace string,
	providerSpec v1beta1.NvidiaCarbideMachineProviderSpec,
) *unstructured.Unstructured {
	obj := &unstructured.Unstructured{}
	obj.SetGroupVersionKind(machinev1.GroupVersion.WithKind("Machine"))
	obj.SetName(name)
	obj.SetNamespace(namespace)

	// Embed provider spec
	providerSpecMap, _ := runtime.DefaultUnstructuredConverter.ToUnstructured(&providerSpec)
	_ = unstructured.SetNestedField(obj.Object, providerSpecMap, "spec", "providerSpec", "value")

	return obj
}

func ptr[T any](v T) *T {
	return &v
}
