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
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// GarageNodeSpec defines the desired state of GarageNode.
//
// GarageNode represents a node in the Garage cluster layout. Use this resource when:
//
// 1. GarageCluster.layoutPolicy is "Manual" - You must create GarageNode for each pod
// 2. External nodes - Nodes outside this Kubernetes cluster (air-gapped, VMs, other K8s clusters)
// 3. Fine-grained control - Different zones/capacities per node within the same cluster
// 4. Gateway nodes - Nodes that handle API requests but don't store data
//
// When NOT to use GarageNode:
// - With layoutPolicy: "Auto" (default) - The controller auto-manages local pod layouts
// - For simple single-zone deployments - Let the controller handle it
//
// For multi-cluster federation, prefer using GarageCluster.remoteClusters with auto-discovery
// instead of manually creating GarageNode resources for each remote node.
type GarageNodeSpec struct {
	// ClusterRef references the GarageCluster this node belongs to
	// +required
	ClusterRef ClusterReference `json:"clusterRef"`

	// NodeID is the public key of the Garage node
	// If not specified, the operator will auto-discover from the pod
	// +optional
	NodeID string `json:"nodeId,omitempty"`

	// Zone is the zone assignment for this node
	// Used for data placement and fault tolerance
	// +required
	Zone string `json:"zone"`

	// Capacity is the storage capacity of this node
	// Required unless Gateway is true
	// +optional
	Capacity *resource.Quantity `json:"capacity,omitempty"`

	// Gateway marks this node as a gateway-only node (no storage)
	// Gateway nodes handle API requests but don't store data
	// +optional
	Gateway bool `json:"gateway,omitempty"`

	// Tags are custom tags for this node
	// +optional
	Tags []string `json:"tags,omitempty"`

	// PodSelector selects the pod for this node
	// Used for auto-discovery of node ID
	// +optional
	PodSelector *PodSelector `json:"podSelector,omitempty"`

	// External marks this node as an external node (not managed by this operator)
	// Used for multi-cluster federation
	// +optional
	External *ExternalNodeConfig `json:"external,omitempty"`
}

// PodSelector selects a pod for node auto-discovery
type PodSelector struct {
	// Name of the pod
	// +optional
	Name string `json:"name,omitempty"`

	// Labels to match
	// +optional
	Labels map[string]string `json:"labels,omitempty"`

	// StatefulSetIndex for StatefulSet-managed pods
	// +optional
	StatefulSetIndex *int `json:"statefulSetIndex,omitempty"`
}

// ExternalNodeConfig configures an external node
type ExternalNodeConfig struct {
	// Address is the IP or hostname of the external node
	// +required
	Address string `json:"address"`

	// Port is the RPC port of the external node
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=65535
	// +kubebuilder:default=3901
	Port int32 `json:"port,omitempty"`

	// RemoteClusterRef references a GarageCluster in another namespace/cluster
	// +optional
	RemoteClusterRef *ClusterReference `json:"remoteClusterRef,omitempty"`
}

// GarageNodeStatus defines the observed state of GarageNode
type GarageNodeStatus struct {
	// NodeID is the discovered or assigned node ID
	// +optional
	NodeID string `json:"nodeId,omitempty"`

	// Phase represents the current phase
	// +optional
	Phase string `json:"phase,omitempty"`

	// InLayout indicates if this node is part of the current layout
	// +optional
	InLayout bool `json:"inLayout,omitempty"`

	// LayoutVersion is the layout version when this node was added
	// +optional
	LayoutVersion int64 `json:"layoutVersion,omitempty"`

	// Connected indicates if the node is currently connected
	// +optional
	Connected bool `json:"connected,omitempty"`

	// LastSeen is when the node was last seen connected
	// +optional
	LastSeen *metav1.Time `json:"lastSeen,omitempty"`

	// Address is the node's address in the cluster
	// +optional
	Address string `json:"address,omitempty"`

	// Hostname is the hostname reported by this Garage node
	// +optional
	Hostname string `json:"hostname,omitempty"`

	// Tags are the tags assigned to this node in the layout
	// +optional
	Tags []string `json:"tags,omitempty"`

	// DataPartition contains disk space info for the data partition
	// Note: Garage reports a single partition even with multiple data paths
	// +optional
	DataPartition *DiskPartitionStatus `json:"dataPartition,omitempty"`

	// MetadataPartition contains disk space info for the metadata partition
	// +optional
	MetadataPartition *DiskPartitionStatus `json:"metadataPartition,omitempty"`

	// Version is the Garage version on this node
	// +optional
	Version string `json:"version,omitempty"`

	// DBEngine is the database engine used by this node (lmdb, sqlite, fjall)
	// +optional
	DBEngine string `json:"dbEngine,omitempty"`

	// GarageFeatures lists the enabled Cargo features on this node
	// +optional
	GarageFeatures []string `json:"garageFeatures,omitempty"`

	// Partitions is the number of partitions assigned to this node
	// +optional
	Partitions int `json:"partitions,omitempty"`

	// StoredData is the amount of data stored on this node
	// +optional
	StoredData string `json:"storedData,omitempty"`

	// RepairInProgress indicates if a repair operation is running
	// +optional
	RepairInProgress bool `json:"repairInProgress,omitempty"`

	// RepairType is the type of repair operation in progress
	// +optional
	RepairType string `json:"repairType,omitempty"`

	// RepairProgress is a human-readable repair progress description
	// +optional
	RepairProgress string `json:"repairProgress,omitempty"`

	// BlockErrors is the count of blocks with sync errors on this node
	// +optional
	BlockErrors int32 `json:"blockErrors,omitempty"`

	// ObservedGeneration is the last observed generation
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// Conditions represent the current state
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// DiskPartitionStatus contains disk space information for a partition
type DiskPartitionStatus struct {
	// Available is the available disk space
	// +optional
	Available string `json:"available,omitempty"`

	// Total is the total disk space
	// +optional
	Total string `json:"total,omitempty"`

	// UsedPercent is the percentage of disk space used (0-100)
	// +optional
	UsedPercent int32 `json:"usedPercent,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=gn
// +kubebuilder:printcolumn:name="Cluster",type="string",JSONPath=".spec.clusterRef.name"
// +kubebuilder:printcolumn:name="Zone",type="string",JSONPath=".spec.zone"
// +kubebuilder:printcolumn:name="Capacity",type="string",JSONPath=".spec.capacity"
// +kubebuilder:printcolumn:name="Gateway",type="boolean",JSONPath=".spec.gateway"
// +kubebuilder:printcolumn:name="Connected",type="boolean",JSONPath=".status.connected"
// +kubebuilder:printcolumn:name="InLayout",type="boolean",JSONPath=".status.inLayout"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"

// GarageNode is the Schema for the garagenodes API
type GarageNode struct {
	metav1.TypeMeta `json:",inline"`

	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// +required
	Spec GarageNodeSpec `json:"spec"`

	// +optional
	Status GarageNodeStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// GarageNodeList contains a list of GarageNode
type GarageNodeList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []GarageNode `json:"items"`
}

func init() {
	SchemeBuilder.Register(&GarageNode{}, &GarageNodeList{})
}
