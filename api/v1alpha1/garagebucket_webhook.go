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

	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

var garagebucketlog = logf.Log.WithName("garagebucket-resource")

// SetupWebhookWithManager sets up the webhook with the Manager.
func (r *GarageBucket) SetupWebhookWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr).
		For(r).
		WithDefaulter(&GarageBucketDefaulter{}).
		WithValidator(&GarageBucketValidator{}).
		Complete()
}

// +kubebuilder:webhook:path=/mutate-garage-rajsingh-info-v1alpha1-garagebucket,mutating=true,failurePolicy=fail,sideEffects=None,groups=garage.rajsingh.info,resources=garagebuckets,verbs=create;update,versions=v1alpha1,name=mgaragebucket.kb.io,admissionReviewVersions=v1

var _ webhook.CustomDefaulter = &GarageBucketDefaulter{}

// GarageBucketDefaulter handles defaulting for GarageBucket.
type GarageBucketDefaulter struct{}

// Default implements webhook.CustomDefaulter so a webhook will be registered for the type.
func (d *GarageBucketDefaulter) Default(ctx context.Context, obj runtime.Object) error {
	r, ok := obj.(*GarageBucket)
	if !ok {
		return fmt.Errorf("expected GarageBucket but got %T", obj)
	}

	garagebucketlog.Info("default", "name", r.Name)

	// Set default global alias to metadata.name if not specified
	if r.Spec.GlobalAlias == "" {
		r.Spec.GlobalAlias = r.Name
	}

	// Default website index document
	if r.Spec.Website != nil && r.Spec.Website.Enabled && r.Spec.Website.IndexDocument == "" {
		r.Spec.Website.IndexDocument = "index.html"
	}

	return nil
}

// +kubebuilder:webhook:path=/validate-garage-rajsingh-info-v1alpha1-garagebucket,mutating=false,failurePolicy=fail,sideEffects=None,groups=garage.rajsingh.info,resources=garagebuckets,verbs=create;update,versions=v1alpha1,name=vgaragebucket.kb.io,admissionReviewVersions=v1

var _ webhook.CustomValidator = &GarageBucketValidator{}

// GarageBucketValidator handles validation for GarageBucket.
type GarageBucketValidator struct{}

// ValidateCreate implements webhook.CustomValidator so a webhook will be registered for the type.
func (v *GarageBucketValidator) ValidateCreate(ctx context.Context, obj runtime.Object) (admission.Warnings, error) {
	r, ok := obj.(*GarageBucket)
	if !ok {
		return nil, fmt.Errorf("expected GarageBucket but got %T", obj)
	}

	garagebucketlog.Info("validate create", "name", r.Name)
	return r.validateGarageBucket()
}

// ValidateUpdate implements webhook.CustomValidator so a webhook will be registered for the type.
func (v *GarageBucketValidator) ValidateUpdate(ctx context.Context, oldObj, newObj runtime.Object) (admission.Warnings, error) {
	r, ok := newObj.(*GarageBucket)
	if !ok {
		return nil, fmt.Errorf("expected GarageBucket but got %T", newObj)
	}

	garagebucketlog.Info("validate update", "name", r.Name)
	return r.validateGarageBucket()
}

// ValidateDelete implements webhook.CustomValidator so a webhook will be registered for the type.
func (v *GarageBucketValidator) ValidateDelete(ctx context.Context, obj runtime.Object) (admission.Warnings, error) {
	r, ok := obj.(*GarageBucket)
	if !ok {
		return nil, fmt.Errorf("expected GarageBucket but got %T", obj)
	}

	garagebucketlog.Info("validate delete", "name", r.Name)
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
