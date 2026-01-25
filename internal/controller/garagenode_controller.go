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

package controller

import (
	"context"
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	garagev1alpha1 "github.com/rajsinghtech/garage-operator/api/v1alpha1"
	"github.com/rajsinghtech/garage-operator/internal/garage"
)

const (
	garageNodeFinalizer = "garagenode.garage.rajsingh.info/finalizer"
)

// GarageNodeReconciler reconciles a GarageNode object
type GarageNodeReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=garage.rajsingh.info,resources=garagenodes,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=garage.rajsingh.info,resources=garagenodes/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=garage.rajsingh.info,resources=garagenodes/finalizers,verbs=update
// +kubebuilder:rbac:groups=core,resources=pods,verbs=get;list;watch
// +kubebuilder:rbac:groups=core,resources=pods/exec,verbs=create

func (r *GarageNodeReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	node := &garagev1alpha1.GarageNode{}
	if err := r.Get(ctx, req.NamespacedName, node); err != nil {
		if errors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	// Get the cluster reference
	cluster := &garagev1alpha1.GarageCluster{}
	clusterNamespace := node.Namespace
	if node.Spec.ClusterRef.Namespace != "" {
		clusterNamespace = node.Spec.ClusterRef.Namespace
	}
	if err := r.Get(ctx, types.NamespacedName{
		Name:      node.Spec.ClusterRef.Name,
		Namespace: clusterNamespace,
	}, cluster); err != nil {
		return r.updateStatus(ctx, node, "Error", fmt.Errorf("cluster not found: %w", err))
	}

	// Get garage client
	garageClient, err := GetGarageClient(ctx, r.Client, cluster)
	if err != nil {
		return r.updateStatus(ctx, node, "Error", fmt.Errorf("failed to create garage client: %w", err))
	}

	// Handle deletion
	if !node.DeletionTimestamp.IsZero() {
		if controllerutil.ContainsFinalizer(node, garageNodeFinalizer) {
			if err := r.finalize(ctx, node, garageClient); err != nil {
				// Check if we've exceeded max retries
				if ShouldSkipFinalization(node) {
					log.Info("Finalization failed too many times, removing finalizer anyway",
						"retries", GetFinalizationRetryCount(node), "error", err)
				} else {
					IncrementFinalizationRetryCount(node)
					retryCount := GetFinalizationRetryCount(node)
					log.Error(err, "Failed to finalize node, will retry",
						"retries", retryCount)
					// Persist the retry count annotation FIRST, then update status
					// (updateStatus may refetch the object on conflict, losing annotation changes)
					if updateErr := r.Update(ctx, node); updateErr != nil {
						log.Error(updateErr, "Failed to update retry count annotation")
					}
					// Now update status - this is best effort, don't fail if it errors
					_, _ = r.updateStatus(ctx, node, PhaseDeleting, fmt.Errorf("finalization failed (retry %d/%d): %w", retryCount, FinalizationMaxRetries, err))
					return ctrl.Result{RequeueAfter: RequeueAfterError}, nil
				}
			}
			controllerutil.RemoveFinalizer(node, garageNodeFinalizer)
			if err := r.Update(ctx, node); err != nil {
				return ctrl.Result{}, err
			}
		}
		return ctrl.Result{}, nil
	}

	// Add finalizer
	if !controllerutil.ContainsFinalizer(node, garageNodeFinalizer) {
		controllerutil.AddFinalizer(node, garageNodeFinalizer)
		if err := r.Update(ctx, node); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{Requeue: true}, nil
	}

	// Reconcile the node
	if err := r.reconcileNode(ctx, node, cluster, garageClient); err != nil {
		return r.updateStatus(ctx, node, "Error", err)
	}

	return r.updateStatusFromGarage(ctx, node, garageClient)
}

func (r *GarageNodeReconciler) reconcileNode(ctx context.Context, node *garagev1alpha1.GarageNode, cluster *garagev1alpha1.GarageCluster, garageClient *garage.Client) error {
	log := logf.FromContext(ctx)

	// Discover or use provided node ID
	nodeID := node.Spec.NodeID
	if nodeID == "" {
		discovered, err := r.discoverNodeID(ctx, node, cluster)
		if err != nil {
			return fmt.Errorf("failed to discover node ID: %w", err)
		}
		nodeID = discovered
	}

	// If node ID is still empty, try to discover from Admin API using pod IP
	if nodeID == "" {
		podIP, err := r.getPodIP(ctx, node, cluster)
		if err != nil {
			return fmt.Errorf("failed to get pod IP for node discovery: %w", err)
		}

		discovered, err := r.discoverNodeIDFromAdminAPI(ctx, garageClient, podIP)
		if err != nil {
			return fmt.Errorf("failed to discover node ID from Admin API: %w", err)
		}
		nodeID = discovered
	}

	if nodeID == "" {
		return fmt.Errorf("node ID not found and could not be discovered")
	}

	node.Status.NodeID = nodeID

	// Get current layout
	layout, err := garageClient.GetClusterLayout(ctx)
	if err != nil {
		return fmt.Errorf("failed to get cluster layout: %w", err)
	}

	// Check if node is already in layout with correct settings
	var existingRole *garage.LayoutRole
	for i := range layout.Roles {
		if layout.Roles[i].ID == nodeID {
			existingRole = &layout.Roles[i]
			break
		}
	}

	// Determine capacity
	var capacity *uint64
	if !node.Spec.Gateway {
		if node.Spec.Capacity == nil {
			return fmt.Errorf("capacity is required for non-gateway nodes")
		}
		nodeCapacity := uint64(node.Spec.Capacity.Value())
		// Garage requires minimum capacity of 1024 bytes (1 KB)
		if nodeCapacity < 1024 {
			return fmt.Errorf("capacity must be at least 1024 bytes (1 KB), got %d", nodeCapacity)
		}
		capacity = &nodeCapacity
	}

	// Check if update is needed
	needsUpdate := false
	if existingRole == nil {
		needsUpdate = true
		log.Info("Node not in layout, will add", "nodeID", nodeID)
	} else {
		if existingRole.Zone != node.Spec.Zone {
			needsUpdate = true
		}
		if (existingRole.Capacity == nil) != (capacity == nil) {
			needsUpdate = true
		} else if capacity != nil && existingRole.Capacity != nil && *existingRole.Capacity != *capacity {
			needsUpdate = true
		}
	}

	if needsUpdate {
		log.Info("Updating node in layout", "nodeID", nodeID, "zone", node.Spec.Zone)

		// Check if there are already staged changes that we need to work with
		if len(layout.StagedRoleChanges) > 0 {
			// There are already staged changes. We need to add our changes to them.
			// Check if our node is already in the staged changes.
			alreadyStaged := false
			for _, staged := range layout.StagedRoleChanges {
				if staged.ID == nodeID {
					alreadyStaged = true
					break
				}
			}
			if alreadyStaged {
				log.Info("Node already has staged changes, adding to existing staged layout")
			} else {
				log.Info("Adding to existing staged layout changes", "existingStagedCount", len(layout.StagedRoleChanges))
			}
		}

		// Version to apply will always be current version + 1
		stagedVersion := layout.Version + 1

		updates := []garage.UpdateLayoutRequest{{
			ID:       nodeID,
			Zone:     node.Spec.Zone,
			Capacity: capacity,
			Tags:     node.Spec.Tags,
		}}

		if err := garageClient.UpdateClusterLayout(ctx, updates); err != nil {
			return fmt.Errorf("failed to update layout: %w", err)
		}

		// Apply the layout with the computed version
		if err := garageClient.ApplyClusterLayout(ctx, stagedVersion); err != nil {
			// If apply fails due to version mismatch (409 Conflict), another controller may have applied.
			// Re-read layout and retry on next reconciliation.
			if garage.IsConflict(err) {
				log.Info("Layout version mismatch, will retry on next reconciliation", "attemptedVersion", stagedVersion)
				return nil // Don't return error, just retry later
			}
			return fmt.Errorf("failed to apply layout: %w", err)
		}

		log.Info("Applied layout update", "version", stagedVersion)
	}

	return nil
}

func (r *GarageNodeReconciler) discoverNodeID(ctx context.Context, node *garagev1alpha1.GarageNode, cluster *garagev1alpha1.GarageCluster) (string, error) {
	log := logf.FromContext(ctx)

	// If external node, we can't discover - must be provided
	if node.Spec.External != nil {
		return "", fmt.Errorf("external nodes must have nodeId specified")
	}

	// If pod selector is provided, find the pod
	if node.Spec.PodSelector != nil {
		return r.discoverFromPodSelector(ctx, node, cluster)
	}

	// Try to find pod by StatefulSet naming convention
	// Format: <cluster-name>-<index>
	podName := ""
	if strings.HasPrefix(node.Name, cluster.Name+"-") {
		// Node name matches StatefulSet pod naming, use it
		podName = node.Name
	}

	if podName == "" {
		return "", fmt.Errorf("could not determine pod for node discovery")
	}

	log.Info("Attempting to discover node ID from pod", "pod", podName)
	return r.getNodeIDFromPod(ctx, cluster.Namespace, podName)
}

func (r *GarageNodeReconciler) discoverFromPodSelector(ctx context.Context, node *garagev1alpha1.GarageNode, cluster *garagev1alpha1.GarageCluster) (string, error) {
	selector := node.Spec.PodSelector

	// Direct pod name
	if selector.Name != "" {
		return r.getNodeIDFromPod(ctx, cluster.Namespace, selector.Name)
	}

	// StatefulSet index
	if selector.StatefulSetIndex != nil {
		podName := fmt.Sprintf("%s-%d", cluster.Name, *selector.StatefulSetIndex)
		return r.getNodeIDFromPod(ctx, cluster.Namespace, podName)
	}

	// Label selector
	if len(selector.Labels) > 0 {
		pods := &corev1.PodList{}
		if err := r.List(ctx, pods, client.InNamespace(cluster.Namespace), client.MatchingLabels(selector.Labels)); err != nil {
			return "", fmt.Errorf("failed to list pods: %w", err)
		}
		if len(pods.Items) == 0 {
			return "", fmt.Errorf("no pods found matching labels")
		}
		if len(pods.Items) > 1 {
			return "", fmt.Errorf("multiple pods found matching labels, be more specific")
		}
		return r.getNodeIDFromPod(ctx, pods.Items[0].Namespace, pods.Items[0].Name)
	}

	return "", fmt.Errorf("pod selector does not specify how to find pod")
}

func (r *GarageNodeReconciler) getPodIP(ctx context.Context, node *garagev1alpha1.GarageNode, cluster *garagev1alpha1.GarageCluster) (string, error) {
	// If external node, we can't get pod IP
	if node.Spec.External != nil {
		return "", fmt.Errorf("external nodes must have nodeId specified")
	}

	// Determine pod name
	podName := ""
	if node.Spec.PodSelector != nil {
		if node.Spec.PodSelector.Name != "" {
			podName = node.Spec.PodSelector.Name
		} else if node.Spec.PodSelector.StatefulSetIndex != nil {
			podName = fmt.Sprintf("%s-%d", cluster.Name, *node.Spec.PodSelector.StatefulSetIndex)
		}
	}

	// Try StatefulSet naming convention
	if podName == "" && strings.HasPrefix(node.Name, cluster.Name+"-") {
		podName = node.Name
	}

	if podName == "" {
		return "", fmt.Errorf("could not determine pod for node discovery")
	}

	pod := &corev1.Pod{}
	if err := r.Get(ctx, types.NamespacedName{Name: podName, Namespace: cluster.Namespace}, pod); err != nil {
		return "", fmt.Errorf("failed to get pod %s: %w", podName, err)
	}

	if pod.Status.PodIP == "" {
		return "", fmt.Errorf("pod %s has no IP address yet", podName)
	}

	return pod.Status.PodIP, nil
}

func (r *GarageNodeReconciler) discoverNodeIDFromAdminAPI(ctx context.Context, garageClient *garage.Client, podIP string) (string, error) {
	log := logf.FromContext(ctx)

	status, err := garageClient.GetClusterStatus(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to get cluster status: %w", err)
	}

	// Find node by matching IP address
	for _, n := range status.Nodes {
		if n.Address != nil {
			nodeIP := extractIPFromAddress(*n.Address)
			if nodeIP == podIP {
				log.Info("Discovered node ID from Admin API", "nodeID", n.ID, "podIP", podIP)
				return n.ID, nil
			}
		}
	}

	return "", fmt.Errorf("no node found with IP address %s in cluster status (cluster has %d nodes)", podIP, len(status.Nodes))
}

// extractIPFromAddress extracts the IP address from an address string.
// Handles both IPv4 (ip:port) and IPv6 ([ip]:port) formats.
func extractIPFromAddress(addr string) string {
	// IPv6 format: [ip]:port
	if strings.HasPrefix(addr, "[") {
		if idx := strings.Index(addr, "]:"); idx != -1 {
			return addr[1:idx]
		}
		// Malformed but try to extract
		if idx := strings.Index(addr, "]"); idx != -1 {
			return addr[1:idx]
		}
		return addr
	}
	// IPv4 format: ip:port
	if idx := strings.LastIndex(addr, ":"); idx != -1 {
		return addr[:idx]
	}
	return addr
}

func (r *GarageNodeReconciler) getNodeIDFromPod(ctx context.Context, namespace, podName string) (string, error) {
	// Get the pod
	pod := &corev1.Pod{}
	if err := r.Get(ctx, types.NamespacedName{Name: podName, Namespace: namespace}, pod); err != nil {
		return "", fmt.Errorf("failed to get pod %s: %w", podName, err)
	}

	// Check if pod has the node ID annotation (set by user or previous discovery)
	if nodeID, ok := pod.Annotations["garage.rajsingh.info/node-id"]; ok && nodeID != "" {
		return nodeID, nil
	}

	// Pod must be running for discovery
	if pod.Status.Phase != corev1.PodRunning {
		return "", fmt.Errorf("pod %s is not running (phase: %s)", podName, pod.Status.Phase)
	}

	if pod.Status.PodIP == "" {
		return "", fmt.Errorf("pod %s has no IP address yet", podName)
	}

	// Node ID will be discovered from Admin API in reconcileNode using pod IP
	// Return empty string with no error - caller will use Admin API discovery
	return "", nil
}

func (r *GarageNodeReconciler) finalize(ctx context.Context, node *garagev1alpha1.GarageNode, garageClient *garage.Client) error {
	log := logf.FromContext(ctx)

	if node.Status.NodeID == "" {
		return nil
	}

	log.Info("Removing node from layout", "nodeID", node.Status.NodeID)

	// Get current layout
	layout, err := garageClient.GetClusterLayout(ctx)
	if err != nil {
		return fmt.Errorf("failed to get cluster layout: %w", err)
	}

	// Check if node is in layout
	inLayout := false
	var nodeRole *garage.LayoutRole
	storageNodeCount := 0
	for i, role := range layout.Roles {
		if role.Capacity != nil && *role.Capacity > 0 {
			storageNodeCount++
		}
		if role.ID == node.Status.NodeID {
			inLayout = true
			nodeRole = &layout.Roles[i]
		}
	}

	if !inLayout {
		log.Info("Node not in layout, nothing to remove")
		return nil
	}

	// Check if this node has capacity (is a storage node, not a gateway)
	isStorageNode := nodeRole != nil && nodeRole.Capacity != nil && *nodeRole.Capacity > 0
	if isStorageNode && storageNodeCount <= 1 {
		// Can't remove the last storage node - log warning and skip finalization
		log.Info("Cannot remove last storage node from layout, skipping layout removal",
			"nodeID", node.Status.NodeID, "storageNodes", storageNodeCount)
		return nil
	}

	// Stage removal
	updates := []garage.UpdateLayoutRequest{{
		ID:     node.Status.NodeID,
		Remove: true,
	}}

	if err := garageClient.UpdateClusterLayout(ctx, updates); err != nil {
		return fmt.Errorf("failed to stage node removal: %w", err)
	}

	// Compute version for apply (handle existing staged changes)
	stagedVersion := layout.Version + 1
	if len(layout.StagedRoleChanges) > 0 {
		log.Info("Adding removal to existing staged layout changes", "existingStagedCount", len(layout.StagedRoleChanges))
	}

	// Apply the layout
	if err := garageClient.ApplyClusterLayout(ctx, stagedVersion); err != nil {
		// If apply fails due to version mismatch (409 Conflict), another controller may have applied.
		if garage.IsConflict(err) {
			log.Info("Layout version mismatch during removal, will retry", "attemptedVersion", stagedVersion)
			return fmt.Errorf("layout version mismatch, retry needed: %w", err)
		}
		// If removal would violate replication factor, skip finalization
		// The node will be removed from K8s but remain in Garage layout
		// User must add more nodes or reduce replication factor to fix
		if garage.IsReplicationConstraint(err) {
			log.Info("Cannot remove node: would violate replication factor constraints. "+
				"The node will be removed from Kubernetes but will remain in the Garage layout. "+
				"Add more storage nodes or reduce the replication factor to fully remove this node.",
				"nodeID", node.Status.NodeID, "storageNodes", storageNodeCount)
			return nil
		}
		return fmt.Errorf("failed to apply layout removal: %w", err)
	}

	log.Info("Removed node from layout", "version", stagedVersion)

	// For gateway nodes, immediately skip dead nodes since gateways never store data.
	// This prevents the removed gateway node from getting stuck in Draining state.
	// For storage nodes, we don't auto-skip as it could cause data loss - the operator
	// will log warnings about stuck draining versions and users can use the skip-dead-nodes annotation.
	if node.Spec.Gateway {
		skipReq := garage.SkipDeadNodesRequest{
			Version:          stagedVersion,
			AllowMissingData: true, // Safe for gateways - they never have data
		}
		result, err := garageClient.ClusterLayoutSkipDeadNodes(ctx, skipReq)
		if err != nil {
			// Don't fail finalization if skip fails - will be cleaned up by cluster controller
			if !garage.IsBadRequest(err) {
				log.Error(err, "Failed to skip dead gateway node (will be cleaned up later)")
			}
		} else if len(result.AckUpdated) > 0 || len(result.SyncUpdated) > 0 {
			log.Info("Skipped dead gateway node to prevent draining stall",
				"ackUpdated", len(result.AckUpdated),
				"syncUpdated", len(result.SyncUpdated))
		}
	}

	return nil
}

func (r *GarageNodeReconciler) updateStatus(ctx context.Context, node *garagev1alpha1.GarageNode, phase string, err error) (ctrl.Result, error) {
	node.Status.Phase = phase
	// Only set ObservedGeneration when reconciliation succeeded
	if err == nil {
		node.Status.ObservedGeneration = node.Generation
	}

	if err != nil {
		meta.SetStatusCondition(&node.Status.Conditions, metav1.Condition{
			Type:               "Ready",
			Status:             metav1.ConditionFalse,
			Reason:             "Error",
			Message:            err.Error(),
			ObservedGeneration: node.Generation,
		})
	}

	if statusErr := UpdateStatusWithRetry(ctx, r.Client, node); statusErr != nil {
		return ctrl.Result{}, statusErr
	}

	if err != nil {
		return ctrl.Result{RequeueAfter: RequeueAfterError}, nil
	}
	return ctrl.Result{}, nil
}

func (r *GarageNodeReconciler) updateStatusFromGarage(ctx context.Context, node *garagev1alpha1.GarageNode, garageClient *garage.Client) (ctrl.Result, error) {
	if node.Status.NodeID == "" {
		return r.updateStatus(ctx, node, "Pending", nil)
	}

	// Get cluster status
	status, err := garageClient.GetClusterStatus(ctx)
	if err != nil {
		return r.updateStatus(ctx, node, "Error", fmt.Errorf("failed to get cluster status: %w", err))
	}

	// Find node in known nodes
	var nodeInfo *garage.NodeInfo
	for i := range status.Nodes {
		if status.Nodes[i].ID == node.Status.NodeID {
			nodeInfo = &status.Nodes[i]
			break
		}
	}

	// Get layout info
	layout, err := garageClient.GetClusterLayout(ctx)
	if err != nil {
		return r.updateStatus(ctx, node, "Error", fmt.Errorf("failed to get cluster layout: %w", err))
	}

	// Find node in layout
	var layoutRole *garage.LayoutRole
	for i := range layout.Roles {
		if layout.Roles[i].ID == node.Status.NodeID {
			layoutRole = &layout.Roles[i]
			break
		}
	}

	// Update status
	node.Status.Phase = "Ready"
	node.Status.ObservedGeneration = node.Generation
	node.Status.InLayout = layoutRole != nil

	if layoutRole != nil {
		node.Status.LayoutVersion = layout.Version
	}

	if nodeInfo != nil {
		node.Status.Connected = nodeInfo.IsUp
		if nodeInfo.Address != nil {
			node.Status.Address = *nodeInfo.Address
		}
		if nodeInfo.IsUp {
			now := metav1.Now()
			node.Status.LastSeen = &now
		}
	}

	// Set ready condition
	conditionStatus := metav1.ConditionTrue
	reason := "NodeReady"
	message := "Node is ready and in layout"

	if !node.Status.InLayout {
		conditionStatus = metav1.ConditionFalse
		reason = "NotInLayout"
		message = "Node is not yet in the cluster layout"
	} else if nodeInfo != nil && !nodeInfo.IsUp {
		conditionStatus = metav1.ConditionFalse
		reason = "NodeDisconnected"
		message = "Node is in layout but not connected"
	}

	meta.SetStatusCondition(&node.Status.Conditions, metav1.Condition{
		Type:               "Ready",
		Status:             conditionStatus,
		Reason:             reason,
		Message:            message,
		ObservedGeneration: node.Generation,
	})

	if err := UpdateStatusWithRetry(ctx, r.Client, node); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{RequeueAfter: RequeueAfterShort}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *GarageNodeReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&garagev1alpha1.GarageNode{}).
		Named("garagenode").
		Complete(r)
}
