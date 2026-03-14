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
	"testing"
)

func TestValidateProviderSpec(t *testing.T) {
	tests := []struct {
		name    string
		spec    NvidiaCarbideMachineProviderSpec
		wantErr bool
	}{
		{
			name: "valid with instanceTypeId",
			spec: NvidiaCarbideMachineProviderSpec{
				SiteID:         "550e8400-e29b-41d4-a716-446655440000",
				TenantID:       "660e8400-e29b-41d4-a716-446655440001",
				InstanceTypeID: "990e8400-e29b-41d4-a716-446655440004",
				VpcID:          "770e8400-e29b-41d4-a716-446655440002",
				SubnetID:       "880e8400-e29b-41d4-a716-446655440003",
			},
			wantErr: false,
		},
		{
			name: "both instanceTypeId and machineId",
			spec: NvidiaCarbideMachineProviderSpec{
				SiteID:         "550e8400-e29b-41d4-a716-446655440000",
				TenantID:       "660e8400-e29b-41d4-a716-446655440001",
				InstanceTypeID: "990e8400-e29b-41d4-a716-446655440004",
				MachineID:      "aa0e8400-e29b-41d4-a716-446655440005",
				VpcID:          "770e8400-e29b-41d4-a716-446655440002",
				SubnetID:       "880e8400-e29b-41d4-a716-446655440003",
			},
			wantErr: true,
		},
		{
			name: "missing siteId",
			spec: NvidiaCarbideMachineProviderSpec{
				TenantID:       "660e8400-e29b-41d4-a716-446655440001",
				InstanceTypeID: "990e8400-e29b-41d4-a716-446655440004",
				VpcID:          "770e8400-e29b-41d4-a716-446655440002",
				SubnetID:       "880e8400-e29b-41d4-a716-446655440003",
			},
			wantErr: true,
		},
		{
			name: "too many additional subnets",
			spec: NvidiaCarbideMachineProviderSpec{
				SiteID:              "550e8400-e29b-41d4-a716-446655440000",
				TenantID:            "660e8400-e29b-41d4-a716-446655440001",
				InstanceTypeID:      "990e8400-e29b-41d4-a716-446655440004",
				VpcID:               "770e8400-e29b-41d4-a716-446655440002",
				SubnetID:            "880e8400-e29b-41d4-a716-446655440003",
				AdditionalSubnetIDs: make([]AdditionalSubnet, 11),
			},
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

func TestValidateUUIDs(t *testing.T) {
	tests := []struct {
		name    string
		spec    NvidiaCarbideMachineProviderSpec
		wantErr bool
	}{
		{
			name: "valid UUIDs",
			spec: NvidiaCarbideMachineProviderSpec{
				SiteID:         "550e8400-e29b-41d4-a716-446655440000",
				TenantID:       "660e8400-e29b-41d4-a716-446655440001",
				InstanceTypeID: "990e8400-e29b-41d4-a716-446655440004",
				VpcID:          "770e8400-e29b-41d4-a716-446655440002",
				SubnetID:       "880e8400-e29b-41d4-a716-446655440003",
			},
			wantErr: false,
		},
		{
			name: "invalid siteId UUID",
			spec: NvidiaCarbideMachineProviderSpec{
				SiteID:         "not-a-uuid",
				TenantID:       "660e8400-e29b-41d4-a716-446655440001",
				InstanceTypeID: "990e8400-e29b-41d4-a716-446655440004",
				VpcID:          "770e8400-e29b-41d4-a716-446655440002",
				SubnetID:       "880e8400-e29b-41d4-a716-446655440003",
			},
			wantErr: true,
		},
		{
			name: "invalid optional operatingSystemId UUID",
			spec: NvidiaCarbideMachineProviderSpec{
				SiteID:            "550e8400-e29b-41d4-a716-446655440000",
				TenantID:          "660e8400-e29b-41d4-a716-446655440001",
				InstanceTypeID:    "990e8400-e29b-41d4-a716-446655440004",
				VpcID:             "770e8400-e29b-41d4-a716-446655440002",
				SubnetID:          "880e8400-e29b-41d4-a716-446655440003",
				OperatingSystemID: "bad-uuid",
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateUUIDs(&tt.spec)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateUUIDs() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestValidateImmutableFields(t *testing.T) {
	oldSpec := &NvidiaCarbideMachineProviderSpec{
		SiteID:   "550e8400-e29b-41d4-a716-446655440000",
		TenantID: "660e8400-e29b-41d4-a716-446655440001",
	}

	t.Run("same values allowed", func(t *testing.T) {
		newSpec := &NvidiaCarbideMachineProviderSpec{
			SiteID:   "550e8400-e29b-41d4-a716-446655440000",
			TenantID: "660e8400-e29b-41d4-a716-446655440001",
		}
		if err := validateImmutableFields(oldSpec, newSpec); err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("changed siteId rejected", func(t *testing.T) {
		newSpec := &NvidiaCarbideMachineProviderSpec{
			SiteID:   "different-site-id",
			TenantID: "660e8400-e29b-41d4-a716-446655440001",
		}
		if err := validateImmutableFields(oldSpec, newSpec); err == nil {
			t.Error("expected error for changed siteId")
		}
	})

	t.Run("changed tenantId rejected", func(t *testing.T) {
		newSpec := &NvidiaCarbideMachineProviderSpec{
			SiteID:   "550e8400-e29b-41d4-a716-446655440000",
			TenantID: "different-tenant-id",
		}
		if err := validateImmutableFields(oldSpec, newSpec); err == nil {
			t.Error("expected error for changed tenantId")
		}
	})
}
