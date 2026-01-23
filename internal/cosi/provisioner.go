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

package cosi

import (
	"context"
	"fmt"
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	garagev1alpha1 "github.com/rajsinghtech/garage-operator/api/v1alpha1"
	"github.com/rajsinghtech/garage-operator/internal/controller"
	"github.com/rajsinghtech/garage-operator/internal/garage"
	cosiproto "sigs.k8s.io/container-object-storage-interface/proto"
)

var log = ctrl.Log.WithName("cosi-provisioner")

// GarageClient defines the interface for Garage API operations used by COSI
type GarageClient interface {
	CreateBucket(ctx context.Context, req garage.CreateBucketRequest) (*garage.Bucket, error)
	GetBucket(ctx context.Context, req garage.GetBucketRequest) (*garage.Bucket, error)
	UpdateBucket(ctx context.Context, req garage.UpdateBucketRequest) (*garage.Bucket, error)
	DeleteBucket(ctx context.Context, bucketID string) error
	CreateKey(ctx context.Context, name string) (*garage.Key, error)
	GetKey(ctx context.Context, req garage.GetKeyRequest) (*garage.Key, error)
	DeleteKey(ctx context.Context, accessKeyID string) error
	AllowBucketKey(ctx context.Context, req garage.AllowBucketKeyRequest) (*garage.Bucket, error)
	DenyBucketKey(ctx context.Context, req garage.DenyBucketKeyRequest) (*garage.Bucket, error)
}

// GarageClientFactory creates a GarageClient for a given cluster
type GarageClientFactory func(ctx context.Context, c client.Client, cluster *garagev1alpha1.GarageCluster) (GarageClient, error)

// defaultGarageClientFactory uses the controller helper to create real Garage clients
func defaultGarageClientFactory(ctx context.Context, c client.Client, cluster *garagev1alpha1.GarageCluster) (GarageClient, error) {
	return controller.GetGarageClient(ctx, c, cluster)
}

// ProvisionerServer implements the COSI Provisioner service
type ProvisionerServer struct {
	cosiproto.UnimplementedProvisionerServer
	client              client.Client
	namespace           string // Namespace for shadow resources
	shadowManager       *ShadowManager
	garageClientFactory GarageClientFactory
}

// NewProvisionerServer creates a new ProvisionerServer
func NewProvisionerServer(c client.Client, namespace string) *ProvisionerServer {
	return &ProvisionerServer{
		client:              c,
		namespace:           namespace,
		shadowManager:       NewShadowManager(c, namespace),
		garageClientFactory: defaultGarageClientFactory,
	}
}

// NewProvisionerServerWithFactory creates a ProvisionerServer with a custom GarageClient factory (for testing)
func NewProvisionerServerWithFactory(c client.Client, namespace string, factory GarageClientFactory) *ProvisionerServer {
	return &ProvisionerServer{
		client:              c,
		namespace:           namespace,
		shadowManager:       NewShadowManager(c, namespace),
		garageClientFactory: factory,
	}
}

// DriverCreateBucket creates a new bucket
func (s *ProvisionerServer) DriverCreateBucket(ctx context.Context, req *cosiproto.DriverCreateBucketRequest) (*cosiproto.DriverCreateBucketResponse, error) {
	log.Info("DriverCreateBucket called", "name", req.Name)

	// Parse parameters
	params, err := ParseBucketClassParameters(req.Parameters, s.namespace)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid parameters: %v", err)
	}

	// Get the GarageCluster
	cluster := &garagev1alpha1.GarageCluster{}
	if err := s.client.Get(ctx, types.NamespacedName{
		Name:      params.ClusterRef,
		Namespace: params.ClusterNamespace,
	}, cluster); err != nil {
		if client.IgnoreNotFound(err) == nil {
			return nil, ErrClusterNotFound(params.ClusterRef, params.ClusterNamespace)
		}
		return nil, status.Errorf(codes.Unavailable, "failed to get cluster: %v", err)
	}

	// Check cluster is ready
	if cluster.Status.Phase != "Running" {
		return nil, ErrClusterNotReady(params.ClusterRef, params.ClusterNamespace)
	}

	// Get Garage client
	garageClient, err := s.garageClientFactory(ctx, s.client, cluster)
	if err != nil {
		return nil, status.Errorf(codes.Unavailable, "failed to create garage client: %v", err)
	}

	// Use the COSI-provided name directly for bucket creation
	// COSI names are DNS-safe and unique
	bucketAlias := sanitizeBucketName(req.Name)
	createReq := garage.CreateBucketRequest{
		GlobalAlias: bucketAlias,
	}

	garageBucket, err := garageClient.CreateBucket(ctx, createReq)
	if err != nil {
		if garage.IsConflict(err) {
			// Bucket exists - check if it's ours (idempotent)
			existing, getErr := garageClient.GetBucket(ctx, garage.GetBucketRequest{GlobalAlias: bucketAlias})
			if getErr == nil {
				log.Info("Bucket already exists, returning existing", "bucketId", existing.ID)
				return s.buildCreateBucketResponse(existing.ID, cluster)
			}
		}
		return nil, MapGarageErrorToCOSI(err)
	}

	// Apply quotas if specified - return error on failure
	if params.MaxSize != nil || params.MaxObjects != nil {
		updateReq := garage.UpdateBucketRequest{
			ID: garageBucket.ID,
		}
		updateReq.Body.Quotas = &garage.BucketQuotas{}
		if params.MaxSize != nil {
			size := uint64(params.MaxSize.Value())
			updateReq.Body.Quotas.MaxSize = &size
		}
		if params.MaxObjects != nil {
			maxObj := uint64(*params.MaxObjects)
			updateReq.Body.Quotas.MaxObjects = &maxObj
		}
		if _, err := garageClient.UpdateBucket(ctx, updateReq); err != nil {
			log.Error(err, "Failed to apply quotas to bucket, deleting bucket", "bucketId", garageBucket.ID)
			// Clean up the bucket since we couldn't apply required quotas
			_ = garageClient.DeleteBucket(ctx, garageBucket.ID)
			return nil, status.Errorf(codes.Internal, "failed to apply quotas to bucket: %v", err)
		}
	}

	// Create shadow GarageBucket resource with bucketId annotation for later lookup
	_, err = s.shadowManager.CreateShadowBucketWithID(ctx, req.Name, garageBucket.ID, params.ClusterRef, params.ClusterNamespace, params)
	if err != nil && !isAlreadyExists(err) {
		log.Error(err, "Failed to create shadow GarageBucket, rolling back Garage bucket", "name", req.Name, "bucketId", garageBucket.ID)
		// Rollback: delete the Garage bucket since we can't track it without the shadow resource
		if deleteErr := garageClient.DeleteBucket(ctx, garageBucket.ID); deleteErr != nil {
			log.Error(deleteErr, "Failed to rollback Garage bucket after shadow resource failure", "bucketId", garageBucket.ID)
		}
		return nil, status.Errorf(codes.Internal, "failed to create shadow resource: %v", err)
	}

	log.Info("Bucket created successfully", "bucketId", garageBucket.ID, "name", bucketAlias)
	return s.buildCreateBucketResponse(garageBucket.ID, cluster)
}

// DriverDeleteBucket deletes a bucket
func (s *ProvisionerServer) DriverDeleteBucket(ctx context.Context, req *cosiproto.DriverDeleteBucketRequest) (*cosiproto.DriverDeleteBucketResponse, error) {
	log.Info("DriverDeleteBucket called", "bucketId", req.BucketId)

	// Parse parameters from DeleteContext
	params, err := ParseBucketClassParameters(req.DeleteContext, s.namespace)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid parameters: %v", err)
	}

	// Get the GarageCluster
	cluster := &garagev1alpha1.GarageCluster{}
	if err := s.client.Get(ctx, types.NamespacedName{
		Name:      params.ClusterRef,
		Namespace: params.ClusterNamespace,
	}, cluster); err != nil {
		if client.IgnoreNotFound(err) == nil {
			// Cluster doesn't exist, still try to cleanup shadow resources
			log.Info("Cluster not found, cleaning up shadow resources only", "cluster", params.ClusterRef)
			if cleanupErr := s.shadowManager.DeleteShadowBucketByID(ctx, req.BucketId); cleanupErr != nil {
				log.Error(cleanupErr, "Failed to delete shadow bucket by ID", "bucketId", req.BucketId)
			}
			return &cosiproto.DriverDeleteBucketResponse{}, nil
		}
		return nil, status.Errorf(codes.Unavailable, "failed to get cluster: %v", err)
	}

	// Get Garage client
	garageClient, err := s.garageClientFactory(ctx, s.client, cluster)
	if err != nil {
		return nil, status.Errorf(codes.Unavailable, "failed to create garage client: %v", err)
	}

	// Delete bucket from Garage
	if err := garageClient.DeleteBucket(ctx, req.BucketId); err != nil {
		if garage.IsNotFound(err) {
			log.Info("Bucket already deleted from Garage", "bucketId", req.BucketId)
		} else if garage.IsBucketNotEmpty(err) {
			return nil, status.Errorf(codes.FailedPrecondition, "bucket is not empty, delete all objects first")
		} else {
			return nil, MapGarageErrorToCOSI(err)
		}
	}

	// Delete shadow GarageBucket resource by bucketId
	if err := s.shadowManager.DeleteShadowBucketByID(ctx, req.BucketId); err != nil {
		log.Error(err, "Failed to delete shadow GarageBucket", "bucketId", req.BucketId)
	}

	log.Info("Bucket deleted successfully", "bucketId", req.BucketId)
	return &cosiproto.DriverDeleteBucketResponse{}, nil
}

// DriverGrantBucketAccess grants access to a bucket
func (s *ProvisionerServer) DriverGrantBucketAccess(ctx context.Context, req *cosiproto.DriverGrantBucketAccessRequest) (*cosiproto.DriverGrantBucketAccessResponse, error) {
	log.Info("DriverGrantBucketAccess called", "name", req.Name, "bucketId", req.BucketId)

	// Reject IAM authentication (Garage only supports KEY authentication)
	if req.AuthenticationType == cosiproto.AuthenticationType_IAM {
		return nil, ErrUnsupportedAuthType
	}

	// Validate we have a bucket
	if req.BucketId == "" {
		return nil, status.Errorf(codes.InvalidArgument, "bucketId is required")
	}

	// Parse parameters
	params, err := ParseBucketAccessClassParameters(req.Parameters, s.namespace)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid parameters: %v", err)
	}

	// Get the GarageCluster
	cluster := &garagev1alpha1.GarageCluster{}
	if err := s.client.Get(ctx, types.NamespacedName{
		Name:      params.ClusterRef,
		Namespace: params.ClusterNamespace,
	}, cluster); err != nil {
		if client.IgnoreNotFound(err) == nil {
			return nil, ErrClusterNotFound(params.ClusterRef, params.ClusterNamespace)
		}
		return nil, status.Errorf(codes.Unavailable, "failed to get cluster: %v", err)
	}

	// Check cluster is ready
	if cluster.Status.Phase != "Running" {
		return nil, ErrClusterNotReady(params.ClusterRef, params.ClusterNamespace)
	}

	// Get Garage client
	garageClient, err := s.garageClientFactory(ctx, s.client, cluster)
	if err != nil {
		return nil, status.Errorf(codes.Unavailable, "failed to create garage client: %v", err)
	}

	// Use COSI-provided name for key name
	keyName := sanitizeKeyName(req.Name)

	// Check for idempotency - see if key already exists
	existingKey, err := garageClient.GetKey(ctx, garage.GetKeyRequest{Search: keyName, ShowSecretKey: true})
	if err == nil && existingKey != nil {
		log.Info("Key already exists, verifying bucket permissions", "keyId", existingKey.AccessKeyID)

		// Check if key has access to the requested bucket
		hasAccess := false
		for _, bucket := range existingKey.Buckets {
			if bucket.ID == req.BucketId {
				hasAccess = true
				break
			}
		}

		if !hasAccess {
			log.Info("Granting access to bucket for existing key", "keyId", existingKey.AccessKeyID, "bucketId", req.BucketId)
			allowReq := garage.AllowBucketKeyRequest{
				BucketID:    req.BucketId,
				AccessKeyID: existingKey.AccessKeyID,
				Permissions: garage.BucketKeyPerms{
					Read:  true,
					Write: true,
					Owner: false,
				},
			}
			if _, err := garageClient.AllowBucketKey(ctx, allowReq); err != nil {
				log.Error(err, "Failed to grant access to bucket for existing key", "bucketId", req.BucketId, "keyId", existingKey.AccessKeyID)
				return nil, MapGarageErrorToCOSI(err)
			}
		}

		// Verify secret key is available
		if existingKey.SecretAccessKey == "" {
			return nil, status.Errorf(codes.Internal, "existing key secret is not available")
		}

		return s.buildGrantAccessResponse(existingKey, req.BucketId, cluster)
	}

	// Create key in Garage
	key, err := garageClient.CreateKey(ctx, keyName)
	if err != nil {
		return nil, MapGarageErrorToCOSI(err)
	}

	// Grant access to the bucket
	allowReq := garage.AllowBucketKeyRequest{
		BucketID:    req.BucketId,
		AccessKeyID: key.AccessKeyID,
		Permissions: garage.BucketKeyPerms{
			Read:  true,
			Write: true,
			Owner: false,
		},
	}
	if _, err := garageClient.AllowBucketKey(ctx, allowReq); err != nil {
		log.Error(err, "Failed to grant access to bucket", "bucketId", req.BucketId, "keyId", key.AccessKeyID)
		// Clean up: delete key
		_ = garageClient.DeleteKey(ctx, key.AccessKeyID)
		return nil, MapGarageErrorToCOSI(err)
	}

	// Create shadow GarageKey resource
	bucketPerms := []BucketPermission{{
		BucketID: req.BucketId,
		Read:     true,
		Write:    true,
		Owner:    false,
	}}
	_, err = s.shadowManager.CreateShadowKeyWithID(ctx, req.Name, key.AccessKeyID, params.ClusterRef, params.ClusterNamespace, bucketPerms)
	if err != nil && !isAlreadyExists(err) {
		log.Error(err, "Failed to create shadow GarageKey", "name", req.Name)
	}

	log.Info("Bucket access granted successfully", "accountId", key.AccessKeyID, "bucketId", req.BucketId)
	return s.buildGrantAccessResponse(key, req.BucketId, cluster)
}

// buildGrantAccessResponse builds response for granted access
func (s *ProvisionerServer) buildGrantAccessResponse(key *garage.Key, _ string, cluster *garagev1alpha1.GarageCluster) (*cosiproto.DriverGrantBucketAccessResponse, error) {
	// Validate that secret key is available
	if key.SecretAccessKey == "" {
		return nil, status.Errorf(codes.Internal, "key secret is not available (was showSecretKey=true used?)")
	}

	return &cosiproto.DriverGrantBucketAccessResponse{
		AccountId: key.AccessKeyID,
		Credentials: map[string]*cosiproto.CredentialDetails{
			"s3": {
				Secrets: map[string]string{
					"accessKeyID":     key.AccessKeyID,
					"accessSecretKey": key.SecretAccessKey,
					"endpoint":        s.getS3Endpoint(cluster),
					"region":          s.getS3Region(cluster),
				},
			},
		},
	}, nil
}

// DriverRevokeBucketAccess revokes access to a bucket
func (s *ProvisionerServer) DriverRevokeBucketAccess(ctx context.Context, req *cosiproto.DriverRevokeBucketAccessRequest) (*cosiproto.DriverRevokeBucketAccessResponse, error) {
	log.Info("DriverRevokeBucketAccess called", "accountId", req.AccountId, "bucketId", req.BucketId)

	// Parse parameters from RevokeAccessContext
	params, err := ParseBucketAccessClassParameters(req.RevokeAccessContext, s.namespace)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid parameters: %v", err)
	}

	// Get the GarageCluster
	cluster := &garagev1alpha1.GarageCluster{}
	if err := s.client.Get(ctx, types.NamespacedName{
		Name:      params.ClusterRef,
		Namespace: params.ClusterNamespace,
	}, cluster); err != nil {
		if client.IgnoreNotFound(err) == nil {
			// Cluster doesn't exist, still try to cleanup shadow resources
			log.Info("Cluster not found, cleaning up shadow resources only", "cluster", params.ClusterRef)
			if cleanupErr := s.shadowManager.DeleteShadowKeyByID(ctx, req.AccountId); cleanupErr != nil {
				log.Error(cleanupErr, "Failed to delete shadow key by ID", "accountId", req.AccountId)
			}
			return &cosiproto.DriverRevokeBucketAccessResponse{}, nil
		}
		return nil, status.Errorf(codes.Unavailable, "failed to get cluster: %v", err)
	}

	// Get Garage client
	garageClient, err := s.garageClientFactory(ctx, s.client, cluster)
	if err != nil {
		return nil, status.Errorf(codes.Unavailable, "failed to create garage client: %v", err)
	}

	// Revoke access from the bucket
	denyReq := garage.DenyBucketKeyRequest{
		BucketID:    req.BucketId,
		AccessKeyID: req.AccountId,
	}
	if _, err := garageClient.DenyBucketKey(ctx, denyReq); err != nil {
		if !garage.IsNotFound(err) {
			return nil, MapGarageErrorToCOSI(err)
		}
	}

	// Delete the key from Garage
	if err := garageClient.DeleteKey(ctx, req.AccountId); err != nil {
		if !garage.IsNotFound(err) {
			return nil, MapGarageErrorToCOSI(err)
		}
	}

	// Delete shadow GarageKey resource by accountId
	if err := s.shadowManager.DeleteShadowKeyByID(ctx, req.AccountId); err != nil {
		log.Error(err, "Failed to delete shadow GarageKey", "accountId", req.AccountId)
	}

	log.Info("Bucket access revoked successfully", "accountId", req.AccountId, "bucketId", req.BucketId)
	return &cosiproto.DriverRevokeBucketAccessResponse{}, nil
}

func (s *ProvisionerServer) buildCreateBucketResponse(bucketID string, cluster *garagev1alpha1.GarageCluster) (*cosiproto.DriverCreateBucketResponse, error) {
	return &cosiproto.DriverCreateBucketResponse{
		BucketId: bucketID,
		BucketInfo: &cosiproto.Protocol{
			Type: &cosiproto.Protocol_S3{
				S3: &cosiproto.S3{
					Region:           s.getS3Region(cluster),
					SignatureVersion: cosiproto.S3SignatureVersion_S3V4,
				},
			},
		},
	}, nil
}

func (s *ProvisionerServer) getS3Endpoint(cluster *garagev1alpha1.GarageCluster) string {
	if cluster.Status.Endpoints.S3 != "" {
		return cluster.Status.Endpoints.S3
	}
	// Fallback to constructing from service
	port := 3900
	if cluster.Spec.S3API != nil && cluster.Spec.S3API.BindPort > 0 {
		port = int(cluster.Spec.S3API.BindPort)
	}
	return fmt.Sprintf("%s.%s.svc.cluster.local:%d", cluster.Name, cluster.Namespace, port)
}

func (s *ProvisionerServer) getS3Region(cluster *garagev1alpha1.GarageCluster) string {
	if cluster.Spec.S3API != nil && cluster.Spec.S3API.Region != "" {
		return cluster.Spec.S3API.Region
	}
	return "garage"
}

// sanitizeBucketName ensures the bucket name is valid for Garage
func sanitizeBucketName(name string) string {
	// COSI names are already DNS-safe, just ensure length limit
	if len(name) > 63 {
		return name[:63]
	}
	return name
}

// sanitizeKeyName ensures the key name is valid for Garage
func sanitizeKeyName(name string) string {
	// COSI names are already DNS-safe, just ensure length limit
	if len(name) > 128 {
		return name[:128]
	}
	return name
}

func isAlreadyExists(err error) bool {
	return err != nil && strings.Contains(err.Error(), "already exists")
}
