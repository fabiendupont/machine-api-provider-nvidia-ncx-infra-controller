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

package providerid

import (
	"fmt"
	"strings"

	"github.com/google/uuid"
)

const ProviderPrefix = "nvidia-carbide://"

// ProviderID represents a parsed NVIDIA Carbide provider ID.
// Format: nvidia-carbide://org/tenant/site/instance-id
type ProviderID struct {
	OrgName    string
	TenantName string
	SiteName   string
	InstanceID uuid.UUID
}

// NewProviderID creates a new ProviderID.
func NewProviderID(orgName, tenantName, siteName string, instanceID uuid.UUID) *ProviderID {
	return &ProviderID{
		OrgName:    orgName,
		TenantName: tenantName,
		SiteName:   siteName,
		InstanceID: instanceID,
	}
}

// String returns the provider ID string representation.
func (p *ProviderID) String() string {
	return fmt.Sprintf("%s%s/%s/%s/%s", ProviderPrefix, p.OrgName, p.TenantName, p.SiteName, p.InstanceID.String())
}

// ParseProviderID parses a provider ID string.
// Supports both legacy 3-segment format (nvidia-carbide://org/site/id) and
// new 4-segment format (nvidia-carbide://org/tenant/site/id).
func ParseProviderID(providerIDStr string) (*ProviderID, error) {
	if !strings.HasPrefix(providerIDStr, ProviderPrefix) {
		return nil, fmt.Errorf("invalid provider ID prefix, expected %q: %s", ProviderPrefix, providerIDStr)
	}

	trimmed := strings.TrimPrefix(providerIDStr, ProviderPrefix)
	parts := strings.Split(trimmed, "/")

	switch len(parts) {
	case 3:
		// Legacy format: nvidia-carbide://org/site/instance-id
		instanceID, err := uuid.Parse(parts[2])
		if err != nil {
			return nil, fmt.Errorf("invalid instance ID %q: %w", parts[2], err)
		}
		return &ProviderID{
			OrgName:    parts[0],
			TenantName: "",
			SiteName:   parts[1],
			InstanceID: instanceID,
		}, nil
	case 4:
		// New format: nvidia-carbide://org/tenant/site/instance-id
		instanceID, err := uuid.Parse(parts[3])
		if err != nil {
			return nil, fmt.Errorf("invalid instance ID %q: %w", parts[3], err)
		}
		return &ProviderID{
			OrgName:    parts[0],
			TenantName: parts[1],
			SiteName:   parts[2],
			InstanceID: instanceID,
		}, nil
	default:
		return nil, fmt.Errorf("invalid provider ID format, expected 3 or 4 segments: %s", providerIDStr)
	}
}
