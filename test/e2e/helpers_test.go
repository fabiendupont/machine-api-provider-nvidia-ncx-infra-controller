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

//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	keycloakRealm        = "carbide-dev"
	keycloakClientID     = "carbide-api"
	keycloakClientSecret = "carbide-local-secret"
	keycloakUsername      = "admin@example.com"
	keycloakPassword     = "adminpassword"
)

// getKeycloakToken acquires a JWT from Keycloak using the resource owner password grant.
func getKeycloakToken() string {
	keycloakURL := os.Getenv("NVIDIA_CARBIDE_KEYCLOAK_URL")
	Expect(keycloakURL).NotTo(BeEmpty(), "NVIDIA_CARBIDE_KEYCLOAK_URL must be set")

	tokenURL := fmt.Sprintf("%s/realms/%s/protocol/openid-connect/token", keycloakURL, keycloakRealm)

	data := url.Values{
		"grant_type":    {"password"},
		"client_id":     {keycloakClientID},
		"client_secret": {keycloakClientSecret},
		"username":      {keycloakUsername},
		"password":      {keycloakPassword},
	}

	resp, err := http.Post(tokenURL, "application/x-www-form-urlencoded", strings.NewReader(data.Encode()))
	Expect(err).NotTo(HaveOccurred(), "Failed to request Keycloak token")
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	Expect(err).NotTo(HaveOccurred())
	Expect(resp.StatusCode).To(Equal(http.StatusOK),
		"Keycloak token request failed with status %d: %s", resp.StatusCode, string(body))

	var tokenResp struct {
		AccessToken string `json:"access_token"`
	}
	Expect(json.Unmarshal(body, &tokenResp)).To(Succeed())
	Expect(tokenResp.AccessToken).NotTo(BeEmpty(), "Received empty access token from Keycloak")

	_, _ = fmt.Fprintf(GinkgoWriter, "Successfully acquired Keycloak token\n")
	return tokenResp.AccessToken
}

// createCredentialsSecret creates a Kubernetes secret with NVIDIA Carbide API credentials.
func createCredentialsSecret(ctx context.Context, k8sClient client.Client, name, namespace, token string) *corev1.Secret {
	// Use the in-cluster endpoint if available (for controllers running inside the cluster),
	// otherwise fall back to the external endpoint.
	endpoint := os.Getenv("NVIDIA_CARBIDE_API_ENDPOINT_INTERNAL")
	if endpoint == "" {
		endpoint = os.Getenv("NVIDIA_CARBIDE_API_ENDPOINT")
	}
	Expect(endpoint).NotTo(BeEmpty(), "NVIDIA_CARBIDE_API_ENDPOINT or NVIDIA_CARBIDE_API_ENDPOINT_INTERNAL must be set")

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Data: map[string][]byte{
			"endpoint": []byte(endpoint),
			"orgName":  []byte("test-org"),
			"token":    []byte(token),
		},
	}
	Expect(k8sClient.Create(ctx, secret)).To(Succeed())
	_, _ = fmt.Fprintf(GinkgoWriter, "Created credentials secret %s/%s\n", namespace, name)
	return secret
}

// carbideAPIRequest makes an authenticated request to the Carbide REST API.
func carbideAPIRequest(method, path, token string, body interface{}) (map[string]interface{}, int) {
	endpoint := os.Getenv("NVIDIA_CARBIDE_API_ENDPOINT")
	Expect(endpoint).NotTo(BeEmpty())

	var reqBody io.Reader
	if body != nil {
		jsonBytes, err := json.Marshal(body)
		Expect(err).NotTo(HaveOccurred())
		reqBody = bytes.NewReader(jsonBytes)
	}

	req, err := http.NewRequest(method, endpoint+path, reqBody)
	Expect(err).NotTo(HaveOccurred())
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := http.DefaultClient.Do(req)
	Expect(err).NotTo(HaveOccurred())
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	Expect(err).NotTo(HaveOccurred())

	var result map[string]interface{}
	if len(respBody) > 0 {
		_ = json.Unmarshal(respBody, &result)
	}

	_, _ = fmt.Fprintf(GinkgoWriter, "%s %s -> %d\n", method, path, resp.StatusCode)
	return result, resp.StatusCode
}

// createVPCViaAPI creates a VPC via the Carbide REST API and returns its ID.
func createVPCViaAPI(token, orgName, siteID, name string) string {
	body := map[string]interface{}{
		"name":   name,
		"siteId": siteID,
	}
	result, status := carbideAPIRequest("POST", fmt.Sprintf("/v2/org/%s/carbide/vpc", orgName), token, body)
	Expect(status).To(Equal(http.StatusCreated), "Failed to create VPC: %v", result)
	vpcID, ok := result["id"].(string)
	Expect(ok).To(BeTrue(), "VPC response missing id")
	_, _ = fmt.Fprintf(GinkgoWriter, "Created VPC %s (id=%s)\n", name, vpcID)
	return vpcID
}

// createIPBlockViaAPI creates an IP block via the Carbide REST API and returns its ID.
func createIPBlockViaAPI(token, orgName, siteID, name string) string {
	body := map[string]interface{}{
		"name":            name,
		"siteId":          siteID,
		"prefix":          "10.0.0.0",
		"prefixLength":    16,
		"protocolVersion": "ipv4",
		"routingType":     "datacenter_only",
	}
	result, status := carbideAPIRequest("POST", fmt.Sprintf("/v2/org/%s/carbide/ipblock", orgName), token, body)
	Expect(status).To(Equal(http.StatusCreated), "Failed to create IP block: %v", result)
	ipBlockID, ok := result["id"].(string)
	Expect(ok).To(BeTrue(), "IP block response missing id")
	_, _ = fmt.Fprintf(GinkgoWriter, "Created IP block %s (id=%s)\n", name, ipBlockID)
	return ipBlockID
}

// createSubnetViaAPI creates a subnet via the Carbide REST API and returns its ID.
func createSubnetViaAPI(token, orgName, vpcID, ipBlockID, name string) string {
	body := map[string]interface{}{
		"name":         name,
		"vpcId":        vpcID,
		"ipv4BlockId":  ipBlockID,
		"prefixLength": 24,
	}
	result, status := carbideAPIRequest("POST", fmt.Sprintf("/v2/org/%s/carbide/subnet", orgName), token, body)
	Expect(status).To(Equal(http.StatusCreated), "Failed to create subnet: %v", result)
	subnetID, ok := result["id"].(string)
	Expect(ok).To(BeTrue(), "Subnet response missing id")
	_, _ = fmt.Fprintf(GinkgoWriter, "Created subnet %s (id=%s)\n", name, subnetID)
	return subnetID
}

// registerSiteInDB marks a site as Registered by updating PostgreSQL directly.
func registerSiteInDB(siteID string) {
	cmd := exec.Command("kubectl", "exec", "-n", "postgres", "statefulset/postgres", "--",
		"psql", "-U", "forge", "-d", "forge", "-c",
		fmt.Sprintf("UPDATE site SET status = 'Registered' WHERE id = '%s'", siteID))
	cmd.Env = append(os.Environ(), "KUBECONFIG=/tmp/carbide-e2e-kubeconfig")
	output, err := cmd.CombinedOutput()
	Expect(err).NotTo(HaveOccurred(), "Failed to register site in DB: %s", string(output))
	_, _ = fmt.Fprintf(GinkgoWriter, "Registered site %s in DB\n", siteID)
}

// setupInfrastructureViaAPI creates the full Carbide infrastructure chain:
// Infrastructure Provider -> Site (registered) -> Tenant -> IP Block -> Allocation -> VPC -> Subnet.
// Returns siteID, tenantID, vpcID, subnetID for use in tests.
func setupInfrastructureViaAPI(token, orgName, prefix string) (siteID, tenantID, vpcID, subnetID string) {
	apiBase := fmt.Sprintf("/v2/org/%s/carbide", orgName)

	// Step 1: Create Infrastructure Provider (idempotent)
	carbideAPIRequest("POST", apiBase+"/infrastructure-provider", token, map[string]interface{}{
		"org": orgName,
	})

	// Step 2: Create Site
	siteResult, status := carbideAPIRequest("POST", apiBase+"/site", token, map[string]interface{}{
		"name": prefix + "-site", "displayName": prefix + " Site",
	})
	Expect(status).To(Equal(http.StatusCreated), "Failed to create site: %v", siteResult)
	siteID = siteResult["id"].(string)
	_, _ = fmt.Fprintf(GinkgoWriter, "Created site (id=%s)\n", siteID)

	// Register site in DB (must be Registered before VPC creation)
	registerSiteInDB(siteID)

	// Step 3: Create Tenant (idempotent)
	tenantResult, _ := carbideAPIRequest("POST", apiBase+"/tenant", token, map[string]interface{}{
		"org": orgName,
	})
	if id, ok := tenantResult["id"].(string); ok {
		tenantID = id
	} else {
		currentTenant, tStatus := carbideAPIRequest("GET", apiBase+"/tenant/current", token, nil)
		Expect(tStatus).To(Equal(http.StatusOK), "Failed to get current tenant: %v", currentTenant)
		tenantID = currentTenant["id"].(string)
	}
	_, _ = fmt.Fprintf(GinkgoWriter, "Tenant ID: %s\n", tenantID)

	// Step 4: Create IP Block
	ipBlockID := createIPBlockViaAPI(token, orgName, siteID, prefix+"-ipblock")

	// Step 5: Create Allocation (links Tenant to Site with IP Block access)
	allocResult, status := carbideAPIRequest("POST", apiBase+"/allocation", token, map[string]interface{}{
		"name":     prefix + "-allocation",
		"tenantId": tenantID,
		"siteId":   siteID,
		"allocationConstraints": []map[string]interface{}{
			{"resourceType": "IPBlock", "resourceTypeId": ipBlockID, "constraintType": "OnDemand", "constraintValue": 24},
		},
	})
	Expect(status).To(Equal(http.StatusCreated), "Failed to create allocation: %v", allocResult)

	// Step 6: Create VPC
	vpcID = createVPCViaAPI(token, orgName, siteID, prefix+"-vpc")

	// Step 7: Create Subnet
	subnetID = createSubnetViaAPI(token, orgName, vpcID, ipBlockID, prefix+"-subnet")

	return siteID, tenantID, vpcID, subnetID
}

// cleanupInfrastructureViaAPI deletes the infrastructure created by setupInfrastructureViaAPI.
func cleanupInfrastructureViaAPI(token, orgName, subnetID, vpcID, siteID string) {
	if subnetID != "" {
		carbideAPIRequest("DELETE", fmt.Sprintf("/v2/org/%s/carbide/subnet/%s", orgName, subnetID), token, nil)
	}
	if vpcID != "" {
		carbideAPIRequest("DELETE", fmt.Sprintf("/v2/org/%s/carbide/vpc/%s", orgName, vpcID), token, nil)
	}
	if siteID != "" {
		carbideAPIRequest("DELETE", fmt.Sprintf("/v2/org/%s/carbide/site/%s", orgName, siteID), token, nil)
	}
}
