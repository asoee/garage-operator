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
	"context"
	"fmt"

	ctrl "sigs.k8s.io/controller-runtime"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

var garagebucketlog = logf.Log.WithName("garagebucket-resource")

// SetupWebhookWithManager sets up the webhook with the Manager.
func (r *GarageBucket) SetupWebhookWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr, r).
		WithDefaulter(&GarageBucketDefaulter{}).
		WithValidator(&GarageBucketValidator{}).
		Complete()
}

// +kubebuilder:webhook:path=/mutate-garage-rajsingh-info-v1alpha1-garagebucket,mutating=true,failurePolicy=fail,sideEffects=None,groups=garage.rajsingh.info,resources=garagebuckets,verbs=create;update,versions=v1alpha1,name=mgaragebucket.kb.io,admissionReviewVersions=v1

var _ admission.Defaulter[*GarageBucket] = &GarageBucketDefaulter{}

// GarageBucketDefaulter handles defaulting for GarageBucket.
type GarageBucketDefaulter struct{}

// Default implements admission.Defaulter so a webhook will be registered for the type.
func (d *GarageBucketDefaulter) Default(ctx context.Context, obj *GarageBucket) error {
	garagebucketlog.Info("default", "name", obj.Name)

	// Set default global alias to metadata.name if not specified
	if obj.Spec.GlobalAlias == "" {
		obj.Spec.GlobalAlias = obj.Name
	}

	// Default website index document
	if obj.Spec.Website != nil && obj.Spec.Website.Enabled && obj.Spec.Website.IndexDocument == "" {
		obj.Spec.Website.IndexDocument = "index.html"
	}

	return nil
}

// +kubebuilder:webhook:path=/validate-garage-rajsingh-info-v1alpha1-garagebucket,mutating=false,failurePolicy=fail,sideEffects=None,groups=garage.rajsingh.info,resources=garagebuckets,verbs=create;update,versions=v1alpha1,name=vgaragebucket.kb.io,admissionReviewVersions=v1

var _ admission.Validator[*GarageBucket] = &GarageBucketValidator{}

// GarageBucketValidator handles validation for GarageBucket.
type GarageBucketValidator struct{}

// ValidateCreate implements admission.Validator so a webhook will be registered for the type.
func (v *GarageBucketValidator) ValidateCreate(ctx context.Context, obj *GarageBucket) (admission.Warnings, error) {
	garagebucketlog.Info("validate create", "name", obj.Name)
	return obj.validateGarageBucket()
}

// ValidateUpdate implements admission.Validator so a webhook will be registered for the type.
func (v *GarageBucketValidator) ValidateUpdate(ctx context.Context, oldObj, newObj *GarageBucket) (admission.Warnings, error) {
	garagebucketlog.Info("validate update", "name", newObj.Name)
	return newObj.validateGarageBucket()
}

// ValidateDelete implements admission.Validator so a webhook will be registered for the type.
func (v *GarageBucketValidator) ValidateDelete(ctx context.Context, obj *GarageBucket) (admission.Warnings, error) {
	garagebucketlog.Info("validate delete", "name", obj.Name)
	return nil, nil
}

// validateGarageBucket validates the GarageBucket spec.
func (r *GarageBucket) validateGarageBucket() (admission.Warnings, error) {
	var warnings admission.Warnings

	// Validate cluster reference
	if r.Spec.ClusterRef.Name == "" {
		return warnings, fmt.Errorf("clusterRef.name is required")
	}

	// Validate key permissions
	if err := r.validateKeyPermissions(); err != nil {
		return warnings, err
	}

	return warnings, nil
}

// validateKeyPermissions validates key permissions.
func (r *GarageBucket) validateKeyPermissions() error {
	seen := make(map[string]bool)
	for i, perm := range r.Spec.KeyPermissions {
		if perm.KeyRef == "" {
			return fmt.Errorf("keyPermissions[%d]: keyRef is required", i)
		}
		if seen[perm.KeyRef] {
			return fmt.Errorf("keyPermissions[%d]: duplicate keyRef '%s'", i, perm.KeyRef)
		}
		seen[perm.KeyRef] = true

		// At least one permission should be granted
		if !perm.Read && !perm.Write && !perm.Owner {
			return fmt.Errorf("keyPermissions[%d]: at least one permission (read, write, or owner) must be granted", i)
		}
	}

	return nil
}
