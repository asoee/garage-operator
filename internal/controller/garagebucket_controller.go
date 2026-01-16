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
	"time"

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
	garageBucketFinalizer = "garagebucket.garage.rajsingh.info/finalizer"
)

// GarageBucketReconciler reconciles a GarageBucket object
type GarageBucketReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=garage.rajsingh.info,resources=garagebuckets,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=garage.rajsingh.info,resources=garagebuckets/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=garage.rajsingh.info,resources=garagebuckets/finalizers,verbs=update
// +kubebuilder:rbac:groups=garage.rajsingh.info,resources=garagekeys,verbs=get;list;watch

func (r *GarageBucketReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	bucket := &garagev1alpha1.GarageBucket{}
	if err := r.Get(ctx, req.NamespacedName, bucket); err != nil {
		if errors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	// Get the cluster reference
	cluster := &garagev1alpha1.GarageCluster{}
	clusterNamespace := bucket.Namespace
	if bucket.Spec.ClusterRef.Namespace != "" {
		clusterNamespace = bucket.Spec.ClusterRef.Namespace
	}
	clusterErr := r.Get(ctx, types.NamespacedName{
		Name:      bucket.Spec.ClusterRef.Name,
		Namespace: clusterNamespace,
	}, cluster)

	// Handle deletion - check this early so we can handle cluster-gone case
	if !bucket.DeletionTimestamp.IsZero() {
		if controllerutil.ContainsFinalizer(bucket, garageBucketFinalizer) {
			// If cluster is gone, skip finalization and just remove finalizer
			if clusterErr != nil && errors.IsNotFound(clusterErr) {
				log.Info("Cluster is gone, skipping bucket finalization", "cluster", bucket.Spec.ClusterRef.Name)
				controllerutil.RemoveFinalizer(bucket, garageBucketFinalizer)
				if err := r.Update(ctx, bucket); err != nil {
					return ctrl.Result{}, err
				}
				return ctrl.Result{}, nil
			}
		}
	}

	// Now check cluster error for non-deletion cases
	if clusterErr != nil {
		return r.updateStatus(ctx, bucket, "Error", fmt.Errorf("cluster not found: %w", clusterErr))
	}

	// Get garage client
	garageClient, err := GetGarageClient(ctx, r.Client, cluster)
	if err != nil {
		return r.updateStatus(ctx, bucket, "Error", fmt.Errorf("failed to create garage client: %w", err))
	}

	// Handle deletion (cluster exists at this point)
	if !bucket.DeletionTimestamp.IsZero() {
		if controllerutil.ContainsFinalizer(bucket, garageBucketFinalizer) {
			if err := r.finalize(ctx, bucket, garageClient); err != nil {
				// Check if we've exceeded max retries
				if ShouldSkipFinalization(bucket) {
					log.Info("Finalization failed too many times, removing finalizer anyway",
						"retries", GetFinalizationRetryCount(bucket), "error", err)
				} else {
					IncrementFinalizationRetryCount(bucket)
					log.Error(err, "Failed to finalize bucket, will retry",
						"retries", GetFinalizationRetryCount(bucket))
					// Surface the finalization error in status before requeuing
					_, _ = r.updateStatus(ctx, bucket, PhaseDeleting, fmt.Errorf("finalization failed: %w", err))
					if updateErr := r.Update(ctx, bucket); updateErr != nil {
						log.Error(updateErr, "Failed to update retry count annotation")
					}
					return ctrl.Result{RequeueAfter: RequeueAfterError}, nil
				}
			}
			controllerutil.RemoveFinalizer(bucket, garageBucketFinalizer)
			if err := r.Update(ctx, bucket); err != nil {
				return ctrl.Result{}, err
			}
		}
		return ctrl.Result{}, nil
	}

	// Add finalizer
	if !controllerutil.ContainsFinalizer(bucket, garageBucketFinalizer) {
		controllerutil.AddFinalizer(bucket, garageBucketFinalizer)
		if err := r.Update(ctx, bucket); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{Requeue: true}, nil
	}

	// Reconcile the bucket
	if err := r.reconcileBucket(ctx, bucket, garageClient); err != nil {
		return r.updateStatus(ctx, bucket, "Error", err)
	}

	return r.updateStatusFromGarage(ctx, bucket, garageClient)
}

func (r *GarageBucketReconciler) reconcileBucket(ctx context.Context, bucket *garagev1alpha1.GarageBucket, garageClient *garage.Client) error {
	log := logf.FromContext(ctx)

	// Determine the alias to use
	alias := bucket.Name
	if bucket.Spec.GlobalAlias != "" {
		alias = bucket.Spec.GlobalAlias
	}

	// Check if bucket exists
	var existingBucket *garage.Bucket
	if bucket.Status.BucketID != "" {
		existing, err := garageClient.GetBucket(ctx, garage.GetBucketRequest{ID: bucket.Status.BucketID})
		if err == nil {
			existingBucket = existing
		}
	}

	if existingBucket == nil {
		// Try to find by alias
		existing, err := garageClient.GetBucket(ctx, garage.GetBucketRequest{GlobalAlias: alias})
		if err == nil {
			existingBucket = existing
		}
	}

	// Create bucket if it doesn't exist
	if existingBucket == nil {
		log.Info("Creating bucket", "alias", alias)
		created, err := garageClient.CreateBucket(ctx, garage.CreateBucketRequest{
			GlobalAlias: alias,
		})
		if err != nil {
			// Handle 409 Conflict - bucket may have been created by another controller
			if garage.IsConflict(err) {
				log.Info("Bucket creation conflict, checking if it was created by another controller", "alias", alias)
				existing, getErr := garageClient.GetBucket(ctx, garage.GetBucketRequest{GlobalAlias: alias})
				if getErr != nil {
					return fmt.Errorf("failed to create bucket (conflict) and failed to get existing bucket: %w (original: %v)", getErr, err)
				}
				existingBucket = existing
				bucket.Status.BucketID = existing.ID
			} else {
				return fmt.Errorf("failed to create bucket: %w", err)
			}
		} else {
			existingBucket = created
			bucket.Status.BucketID = created.ID
		}
	}

	// Update bucket settings
	updateReq := garage.UpdateBucketRequest{ID: existingBucket.ID}
	needsUpdate := false

	// Website configuration
	if bucket.Spec.Website != nil {
		// Garage requires indexDocument when enabling website access
		indexDoc := bucket.Spec.Website.IndexDocument
		if bucket.Spec.Website.Enabled && indexDoc == "" {
			indexDoc = "index.html" // Default per kubebuilder annotation
		}

		// Only update if website config actually changed
		currentEnabled := existingBucket.WebsiteAccess
		currentIndex := ""
		currentError := ""
		if existingBucket.WebsiteConfig != nil {
			currentIndex = existingBucket.WebsiteConfig.IndexDocument
			currentError = existingBucket.WebsiteConfig.ErrorDocument
		}

		if bucket.Spec.Website.Enabled != currentEnabled ||
			(bucket.Spec.Website.Enabled && (indexDoc != currentIndex || bucket.Spec.Website.ErrorDocument != currentError)) {
			updateReq.Body.WebsiteAccess = &garage.UpdateBucketWebsiteAccess{
				Enabled:       bucket.Spec.Website.Enabled,
				IndexDocument: indexDoc,
				ErrorDocument: bucket.Spec.Website.ErrorDocument,
			}
			needsUpdate = true
		}
	}

	// Quotas - only update if values actually changed
	if bucket.Spec.Quotas != nil {
		currentQuotas := existingBucket.Quotas
		var desiredMaxSize, desiredMaxObjects *uint64

		if bucket.Spec.Quotas.MaxSize != nil {
			maxSize := uint64(bucket.Spec.Quotas.MaxSize.Value())
			desiredMaxSize = &maxSize
		}
		if bucket.Spec.Quotas.MaxObjects != nil {
			maxObjects := uint64(*bucket.Spec.Quotas.MaxObjects)
			desiredMaxObjects = &maxObjects
		}

		quotasChanged := false
		if currentQuotas == nil {
			quotasChanged = desiredMaxSize != nil || desiredMaxObjects != nil
		} else {
			// Compare max size
			if (desiredMaxSize == nil) != (currentQuotas.MaxSize == nil) {
				quotasChanged = true
			} else if desiredMaxSize != nil && currentQuotas.MaxSize != nil && *desiredMaxSize != *currentQuotas.MaxSize {
				quotasChanged = true
			}
			// Compare max objects
			if (desiredMaxObjects == nil) != (currentQuotas.MaxObjects == nil) {
				quotasChanged = true
			} else if desiredMaxObjects != nil && currentQuotas.MaxObjects != nil && *desiredMaxObjects != *currentQuotas.MaxObjects {
				quotasChanged = true
			}
		}

		if quotasChanged {
			updateReq.Body.Quotas = &garage.BucketQuotas{
				MaxSize:    desiredMaxSize,
				MaxObjects: desiredMaxObjects,
			}
			needsUpdate = true
		}
	}

	if needsUpdate {
		if _, err := garageClient.UpdateBucket(ctx, updateReq); err != nil {
			return fmt.Errorf("failed to update bucket: %w", err)
		}
	}

	// Handle key permissions
	var permissionErrors []string
	pendingKeys := false
	for _, keyPerm := range bucket.Spec.KeyPermissions {
		// Get the key
		key := &garagev1alpha1.GarageKey{}
		if err := r.Get(ctx, types.NamespacedName{
			Name:      keyPerm.KeyRef,
			Namespace: bucket.Namespace,
		}, key); err != nil {
			if errors.IsNotFound(err) {
				log.Info("Key not found, will retry", "keyRef", keyPerm.KeyRef)
				pendingKeys = true
				continue
			}
			return fmt.Errorf("failed to get key %s: %w", keyPerm.KeyRef, err)
		}

		if key.Status.AccessKeyID == "" {
			log.Info("Key not yet created, will retry", "keyRef", keyPerm.KeyRef)
			pendingKeys = true
			continue
		}

		// Grant permissions
		_, err := garageClient.AllowBucketKey(ctx, garage.AllowBucketKeyRequest{
			BucketID:    existingBucket.ID,
			AccessKeyID: key.Status.AccessKeyID,
			Permissions: garage.BucketKeyPerms{
				Read:  keyPerm.Read,
				Write: keyPerm.Write,
				Owner: keyPerm.Owner,
			},
		})
		if err != nil {
			log.Error(err, "Failed to set key permissions", "keyRef", keyPerm.KeyRef)
			permissionErrors = append(permissionErrors, fmt.Sprintf("%s: %v", keyPerm.KeyRef, err))
		}
	}

	// If there are pending keys, return an error to requeue and retry
	if pendingKeys {
		log.Info("Some keys not ready, will retry permission grants")
		return fmt.Errorf("waiting for keys to be ready before granting permissions")
	}

	// If there were permission errors, return them so status reflects the issue
	if len(permissionErrors) > 0 {
		return fmt.Errorf("failed to set permissions for keys: %v", permissionErrors)
	}

	// Handle local aliases
	var aliasErrors []string
	pendingAliasKeys := false
	for _, localAlias := range bucket.Spec.LocalAliases {
		// Get the key
		key := &garagev1alpha1.GarageKey{}
		if err := r.Get(ctx, types.NamespacedName{
			Name:      localAlias.KeyRef,
			Namespace: bucket.Namespace,
		}, key); err != nil {
			if errors.IsNotFound(err) {
				log.Info("Key for local alias not found, will retry", "keyRef", localAlias.KeyRef, "alias", localAlias.Alias)
				pendingAliasKeys = true
				continue
			}
			return fmt.Errorf("failed to get key %s for local alias: %w", localAlias.KeyRef, err)
		}

		if key.Status.AccessKeyID == "" {
			log.Info("Key for local alias not yet created, will retry", "keyRef", localAlias.KeyRef, "alias", localAlias.Alias)
			pendingAliasKeys = true
			continue
		}

		// Add local alias
		_, err := garageClient.AddBucketAlias(ctx, garage.AddBucketAliasRequest{
			BucketID:    existingBucket.ID,
			LocalAlias:  localAlias.Alias,
			AccessKeyID: key.Status.AccessKeyID,
		})
		if err != nil && !garage.IsConflict(err) {
			log.Error(err, "Failed to add local alias", "keyRef", localAlias.KeyRef, "alias", localAlias.Alias)
			aliasErrors = append(aliasErrors, fmt.Sprintf("%s:%s: %v", localAlias.KeyRef, localAlias.Alias, err))
		}
	}

	if pendingAliasKeys {
		log.Info("Some keys for local aliases not ready, will retry")
		return fmt.Errorf("waiting for keys to be ready before creating local aliases")
	}

	if len(aliasErrors) > 0 {
		return fmt.Errorf("failed to create local aliases: %v", aliasErrors)
	}

	return nil
}

func (r *GarageBucketReconciler) finalize(ctx context.Context, bucket *garagev1alpha1.GarageBucket, garageClient *garage.Client) error {
	log := logf.FromContext(ctx)

	if bucket.Status.BucketID == "" {
		return nil
	}

	log.Info("Deleting bucket", "bucketID", bucket.Status.BucketID)

	// Note: Garage requires bucket to be empty before deletion
	// The operator doesn't delete objects - that's the user's responsibility
	if err := garageClient.DeleteBucket(ctx, bucket.Status.BucketID); err != nil {
		// Check if bucket doesn't exist (404) - that's okay, we can proceed
		if garage.IsNotFound(err) {
			log.Info("Bucket already deleted or not found", "bucketID", bucket.Status.BucketID)
			return nil
		}
		// Specific error for bucket not empty - give user actionable message
		if garage.IsBucketNotEmpty(err) {
			return fmt.Errorf("bucket %q is not empty - delete all objects before removing the GarageBucket resource", bucket.Name)
		}
		// For other errors, return generic message
		return fmt.Errorf("failed to delete bucket: %w", err)
	}

	return nil
}

func (r *GarageBucketReconciler) updateStatus(ctx context.Context, bucket *garagev1alpha1.GarageBucket, phase string, err error) (ctrl.Result, error) {
	bucket.Status.Phase = phase
	// Only set ObservedGeneration when reconciliation succeeded
	if err == nil {
		bucket.Status.ObservedGeneration = bucket.Generation
	}

	if err != nil {
		meta.SetStatusCondition(&bucket.Status.Conditions, metav1.Condition{
			Type:               "Ready",
			Status:             metav1.ConditionFalse,
			Reason:             "Error",
			Message:            err.Error(),
			ObservedGeneration: bucket.Generation,
		})
	}

	if statusErr := UpdateStatusWithRetry(ctx, r.Client, bucket); statusErr != nil {
		return ctrl.Result{}, statusErr
	}

	if err != nil {
		return ctrl.Result{RequeueAfter: RequeueAfterError}, nil
	}
	return ctrl.Result{}, nil
}

func (r *GarageBucketReconciler) updateStatusFromGarage(ctx context.Context, bucket *garagev1alpha1.GarageBucket, garageClient *garage.Client) (ctrl.Result, error) {
	if bucket.Status.BucketID == "" {
		return r.updateStatus(ctx, bucket, "Pending", nil)
	}

	// Get bucket info from Garage
	garageBucket, err := garageClient.GetBucket(ctx, garage.GetBucketRequest{ID: bucket.Status.BucketID})
	if err != nil {
		return r.updateStatus(ctx, bucket, "Error", fmt.Errorf("failed to get bucket info: %w", err))
	}

	// Update status
	bucket.Status.Phase = PhaseReady
	bucket.Status.ObservedGeneration = bucket.Generation
	bucket.Status.ObjectCount = garageBucket.Objects
	bucket.Status.Size = formatBytes(garageBucket.Bytes)

	// Parse creation timestamp
	if garageBucket.Created != "" {
		if t, err := time.Parse(time.RFC3339, garageBucket.Created); err == nil {
			bucket.Status.CreatedAt = &metav1.Time{Time: t}
		}
	}

	// Update incomplete upload stats
	bucket.Status.IncompleteUploads = garageBucket.UnfinishedMultipartUploads
	bucket.Status.IncompleteUploadParts = garageBucket.UnfinishedMultipartUploadParts
	bucket.Status.IncompleteUploadBytes = garageBucket.UnfinishedMultipartUploadBytes

	// Update website status
	bucket.Status.WebsiteEnabled = garageBucket.WebsiteAccess
	if garageBucket.WebsiteConfig != nil {
		bucket.Status.WebsiteConfig = &garagev1alpha1.WebsiteConfigStatus{
			IndexDocument: garageBucket.WebsiteConfig.IndexDocument,
			ErrorDocument: garageBucket.WebsiteConfig.ErrorDocument,
		}
	} else {
		bucket.Status.WebsiteConfig = nil
	}

	// Update quota usage status
	bucket.Status.QuotaUsage = &garagev1alpha1.QuotaUsageStatus{
		SizeBytes:   garageBucket.Bytes,
		ObjectCount: garageBucket.Objects,
	}
	if garageBucket.Quotas != nil {
		if garageBucket.Quotas.MaxSize != nil {
			bucket.Status.QuotaUsage.SizeLimit = int64(*garageBucket.Quotas.MaxSize)
			if *garageBucket.Quotas.MaxSize > 0 {
				bucket.Status.QuotaUsage.SizePercent = int32(garageBucket.Bytes * 100 / int64(*garageBucket.Quotas.MaxSize))
			}
		}
		if garageBucket.Quotas.MaxObjects != nil {
			bucket.Status.QuotaUsage.ObjectLimit = int64(*garageBucket.Quotas.MaxObjects)
			if *garageBucket.Quotas.MaxObjects > 0 {
				bucket.Status.QuotaUsage.ObjectPercent = int32(garageBucket.Objects * 100 / int64(*garageBucket.Quotas.MaxObjects))
			}
		}
	}

	if len(garageBucket.GlobalAliases) > 0 {
		bucket.Status.GlobalAlias = garageBucket.GlobalAliases[0]
	}

	// Update key status and collect local aliases
	bucket.Status.Keys = make([]garagev1alpha1.BucketKeyStatus, 0, len(garageBucket.Keys))
	bucket.Status.LocalAliases = nil // Reset local aliases
	for _, k := range garageBucket.Keys {
		bucket.Status.Keys = append(bucket.Status.Keys, garagev1alpha1.BucketKeyStatus{
			KeyID: k.AccessKeyID,
			Name:  k.Name,
			Permissions: garagev1alpha1.BucketKeyPermissions{
				Read:  k.Permissions.Read,
				Write: k.Permissions.Write,
				Owner: k.Permissions.Owner,
			},
		})
		// Collect local aliases from this key
		for _, alias := range k.BucketLocalAliases {
			bucket.Status.LocalAliases = append(bucket.Status.LocalAliases, garagev1alpha1.LocalAliasStatus{
				KeyID:   k.AccessKeyID,
				KeyName: k.Name,
				Alias:   alias,
			})
		}
	}

	meta.SetStatusCondition(&bucket.Status.Conditions, metav1.Condition{
		Type:               "Ready",
		Status:             metav1.ConditionTrue,
		Reason:             "BucketReady",
		Message:            "Bucket is ready",
		ObservedGeneration: bucket.Generation,
	})

	if err := UpdateStatusWithRetry(ctx, r.Client, bucket); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{RequeueAfter: RequeueAfterLong}, nil
}

func formatBytes(bytes int64) string {
	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%d B", bytes)
	}
	div, exp := int64(unit), 0
	for n := bytes / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(bytes)/float64(div), "KMGTPE"[exp])
}

// SetupWithManager sets up the controller with the Manager.
func (r *GarageBucketReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&garagev1alpha1.GarageBucket{}).
		Named("garagebucket").
		Complete(r)
}
