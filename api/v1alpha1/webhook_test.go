/*
Copyright 2026 Raj Singh.

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

package v1alpha1

import (
	"testing"

	"k8s.io/apimachinery/pkg/api/resource"
)

func TestValidateBindAddress(t *testing.T) {
	tests := []struct {
		name    string
		addr    string
		field   string
		wantErr bool
	}{
		{
			name:    "valid TCP address with port only",
			addr:    ":3900",
			field:   "s3Api",
			wantErr: false,
		},
		{
			name:    "valid TCP address with host and port",
			addr:    "0.0.0.0:3900",
			field:   "s3Api",
			wantErr: false,
		},
		{
			name:    "valid TCP address with IPv6",
			addr:    "[::]:3900",
			field:   "s3Api",
			wantErr: false,
		},
		{
			name:    "valid unix socket",
			addr:    "unix:///run/garage/s3.sock",
			field:   "s3Api",
			wantErr: false,
		},
		{
			name:    "invalid address - no port",
			addr:    "localhost",
			field:   "s3Api",
			wantErr: true,
		},
		{
			name:    "invalid address - empty",
			addr:    "",
			field:   "s3Api",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateBindAddress(tt.addr, tt.field)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateBindAddress() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestGarageCluster_ValidateZoneRedundancy(t *testing.T) {
	tests := []struct {
		name           string
		zoneRedundancy string
		replication    int
		wantErr        bool
	}{
		{
			name:           "empty zoneRedundancy is valid",
			zoneRedundancy: "",
			replication:    3,
			wantErr:        false,
		},
		{
			name:           "Maximum is valid",
			zoneRedundancy: "Maximum",
			replication:    3,
			wantErr:        false,
		},
		{
			name:           "AtLeast(1) is valid with replication 3",
			zoneRedundancy: "AtLeast(1)",
			replication:    3,
			wantErr:        false,
		},
		{
			name:           "AtLeast(2) is valid with replication 3",
			zoneRedundancy: "AtLeast(2)",
			replication:    3,
			wantErr:        false,
		},
		{
			name:           "AtLeast(3) is valid with replication 3",
			zoneRedundancy: "AtLeast(3)",
			replication:    3,
			wantErr:        false,
		},
		{
			name:           "AtLeast(4) exceeds replication 3",
			zoneRedundancy: "AtLeast(4)",
			replication:    3,
			wantErr:        true,
		},
		{
			name:           "AtLeast(0) is invalid",
			zoneRedundancy: "AtLeast(0)",
			replication:    3,
			wantErr:        true,
		},
		{
			name:           "invalid format",
			zoneRedundancy: "Invalid",
			replication:    3,
			wantErr:        true,
		},
		{
			name:           "invalid AtLeast format",
			zoneRedundancy: "AtLeast(abc)",
			replication:    3,
			wantErr:        true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cluster := &GarageCluster{
				Spec: GarageClusterSpec{
					Replication: ReplicationConfig{
						Factor:         tt.replication,
						ZoneRedundancy: tt.zoneRedundancy,
					},
				},
			}
			err := cluster.validateZoneRedundancy()
			if (err != nil) != tt.wantErr {
				t.Errorf("validateZoneRedundancy() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestGarageCluster_ValidateStorage(t *testing.T) {
	size := resource.MustParse("100Gi")
	tests := []struct {
		name    string
		storage StorageConfig
		wantErr bool
	}{
		{
			name: "valid size config",
			storage: StorageConfig{
				Data: &DataStorageConfig{
					Size: &size,
				},
			},
			wantErr: false,
		},
		{
			name: "invalid - paths not supported",
			storage: StorageConfig{
				Data: &DataStorageConfig{
					Paths: []DataPath{{Path: "/data"}},
				},
			},
			wantErr: true,
		},
		{
			name: "invalid - no size specified",
			storage: StorageConfig{
				Data: &DataStorageConfig{},
			},
			wantErr: true,
		},
		{
			name:    "invalid - no data config",
			storage: StorageConfig{},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cluster := &GarageCluster{
				Spec: GarageClusterSpec{
					Storage: tt.storage,
				},
			}
			err := cluster.validateStorage()
			if (err != nil) != tt.wantErr {
				t.Errorf("validateStorage() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestGarageBucket_ValidateGarageBucket(t *testing.T) {
	tests := []struct {
		name    string
		bucket  GarageBucket
		wantErr bool
	}{
		{
			name: "valid bucket with cluster ref",
			bucket: GarageBucket{
				Spec: GarageBucketSpec{
					ClusterRef: ClusterReference{
						Name: "my-cluster",
					},
				},
			},
			wantErr: false,
		},
		{
			name: "missing cluster name",
			bucket: GarageBucket{
				Spec: GarageBucketSpec{
					ClusterRef: ClusterReference{},
				},
			},
			wantErr: true,
		},
		{
			name: "bucket with valid key permissions",
			bucket: GarageBucket{
				Spec: GarageBucketSpec{
					ClusterRef: ClusterReference{Name: "test"},
					KeyPermissions: []KeyPermission{
						{
							KeyRef: "my-key",
							Read:   true,
						},
					},
				},
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := tt.bucket.validateGarageBucket()
			if (err != nil) != tt.wantErr {
				t.Errorf("validateGarageBucket() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestGarageBucket_ValidateKeyPermissions(t *testing.T) {
	tests := []struct {
		name        string
		permissions []KeyPermission
		wantErr     bool
	}{
		{
			name:        "nil permissions is valid",
			permissions: nil,
			wantErr:     false,
		},
		{
			name:        "empty permissions is valid",
			permissions: []KeyPermission{},
			wantErr:     false,
		},
		{
			name: "valid permission with read",
			permissions: []KeyPermission{
				{KeyRef: "key1", Read: true},
			},
			wantErr: false,
		},
		{
			name: "valid permission with write",
			permissions: []KeyPermission{
				{KeyRef: "key1", Write: true},
			},
			wantErr: false,
		},
		{
			name: "valid permission with owner",
			permissions: []KeyPermission{
				{KeyRef: "key1", Owner: true},
			},
			wantErr: false,
		},
		{
			name: "invalid - missing keyRef",
			permissions: []KeyPermission{
				{Read: true},
			},
			wantErr: true,
		},
		{
			name: "invalid - no permissions granted",
			permissions: []KeyPermission{
				{KeyRef: "key1"},
			},
			wantErr: true,
		},
		{
			name: "invalid - duplicate keyRef",
			permissions: []KeyPermission{
				{KeyRef: "key1", Read: true},
				{KeyRef: "key1", Write: true},
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bucket := &GarageBucket{
				Spec: GarageBucketSpec{
					ClusterRef:     ClusterReference{Name: "test"},
					KeyPermissions: tt.permissions,
				},
			}
			err := bucket.validateKeyPermissions()
			if (err != nil) != tt.wantErr {
				t.Errorf("validateKeyPermissions() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
