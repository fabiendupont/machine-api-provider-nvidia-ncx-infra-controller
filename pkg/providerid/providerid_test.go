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
	"strings"
	"testing"

	"github.com/google/uuid"
)

const testOrg = "myorg"

func TestProviderID_String(t *testing.T) {
	id := uuid.MustParse("550e8400-e29b-41d4-a716-446655440000")
	pid := NewProviderID(testOrg, "mytenant", "mysite", id)

	got := pid.String()
	want := "nico://myorg/mytenant/mysite/550e8400-e29b-41d4-a716-446655440000"
	if got != want {
		t.Errorf("String() = %q, want %q", got, want)
	}
	if !strings.HasPrefix(got, "nico://") {
		t.Error("String() should always use nico:// scheme")
	}
}

func TestParseProviderID_NicoScheme(t *testing.T) {
	id := uuid.MustParse("550e8400-e29b-41d4-a716-446655440000")
	input := "nico://myorg/mytenant/mysite/550e8400-e29b-41d4-a716-446655440000"

	parsed, err := ParseProviderID(input)
	if err != nil {
		t.Fatalf("ParseProviderID() error = %v", err)
	}
	if parsed.OrgName != testOrg {
		t.Errorf("OrgName = %q, want %q", parsed.OrgName, testOrg)
	}
	if parsed.TenantName != "mytenant" {
		t.Errorf("TenantName = %q, want %q", parsed.TenantName, "mytenant")
	}
	if parsed.SiteName != "mysite" {
		t.Errorf("SiteName = %q, want %q", parsed.SiteName, "mysite")
	}
	if parsed.InstanceID != id {
		t.Errorf("InstanceID = %s, want %s", parsed.InstanceID, id)
	}
}

func TestParseProviderID_LegacyScheme(t *testing.T) {
	input := "nvidia-carbide://myorg/mytenant/mysite/550e8400-e29b-41d4-a716-446655440000"

	parsed, err := ParseProviderID(input)
	if err != nil {
		t.Fatalf("ParseProviderID() error = %v", err)
	}
	if parsed.OrgName != testOrg {
		t.Errorf("OrgName = %q, want %q", parsed.OrgName, testOrg)
	}
	if parsed.TenantName != "mytenant" {
		t.Errorf("TenantName = %q, want %q", parsed.TenantName, "mytenant")
	}
}

func TestParseProviderID_LegacyThreeSegment(t *testing.T) {
	input := "nvidia-carbide://myorg/mysite/550e8400-e29b-41d4-a716-446655440000"

	parsed, err := ParseProviderID(input)
	if err != nil {
		t.Fatalf("ParseProviderID() error = %v", err)
	}
	if parsed.OrgName != testOrg {
		t.Errorf("OrgName = %q, want %q", parsed.OrgName, testOrg)
	}
	if parsed.TenantName != "" {
		t.Errorf("TenantName = %q, want empty for legacy 3-segment", parsed.TenantName)
	}
	if parsed.SiteName != "mysite" {
		t.Errorf("SiteName = %q, want %q", parsed.SiteName, "mysite")
	}
}

func TestParseProviderID_NicoThreeSegment(t *testing.T) {
	input := "nico://myorg/mysite/550e8400-e29b-41d4-a716-446655440000"

	parsed, err := ParseProviderID(input)
	if err != nil {
		t.Fatalf("ParseProviderID() error = %v", err)
	}
	if parsed.TenantName != "" {
		t.Errorf("TenantName = %q, want empty for 3-segment", parsed.TenantName)
	}
}

func TestParseProviderID_InvalidPrefix(t *testing.T) {
	_, err := ParseProviderID("aws://myorg/mysite/550e8400-e29b-41d4-a716-446655440000")
	if err == nil {
		t.Fatal("ParseProviderID() expected error for invalid prefix")
	}
}

func TestParseProviderID_InvalidSegments(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{"too few segments", "nico://myorg/550e8400-e29b-41d4-a716-446655440000"},
		{"too many segments", "nico://a/b/c/d/550e8400-e29b-41d4-a716-446655440000"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ParseProviderID(tt.input)
			if err == nil {
				t.Errorf("ParseProviderID(%q) expected error", tt.input)
			}
		})
	}
}

func TestParseProviderID_InvalidUUID(t *testing.T) {
	_, err := ParseProviderID("nico://myorg/mytenant/mysite/not-a-uuid")
	if err == nil {
		t.Fatal("ParseProviderID() expected error for invalid UUID")
	}
}

func TestProviderID_RoundTrip(t *testing.T) {
	id := uuid.New()
	original := NewProviderID("org1", "tenant1", "site1", id)

	parsed, err := ParseProviderID(original.String())
	if err != nil {
		t.Fatalf("RoundTrip ParseProviderID() error = %v", err)
	}
	if parsed.OrgName != original.OrgName ||
		parsed.TenantName != original.TenantName ||
		parsed.SiteName != original.SiteName ||
		parsed.InstanceID != original.InstanceID {
		t.Errorf("RoundTrip mismatch: got %+v, want %+v", parsed, original)
	}
}
