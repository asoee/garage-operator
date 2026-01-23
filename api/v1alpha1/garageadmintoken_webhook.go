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

var garageadmintokenlog = logf.Log.WithName("garageadmintoken-resource")

// SetupWebhookWithManager sets up the webhook with the Manager.
func (r *GarageAdminToken) SetupWebhookWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr, r).
		WithValidator(&GarageAdminTokenValidator{}).
		Complete()
}

// +kubebuilder:webhook:path=/validate-garage-garage-rajsingh-info-v1alpha1-garageadmintoken,mutating=false,failurePolicy=fail,sideEffects=None,groups=garage.garage.rajsingh.info,resources=garageadmintokens,verbs=create;update,versions=v1alpha1,name=vgarageadmintoken.kb.io,admissionReviewVersions=v1

var _ admission.Validator[*GarageAdminToken] = &GarageAdminTokenValidator{}

// GarageAdminTokenValidator handles validation for GarageAdminToken.
type GarageAdminTokenValidator struct{}

// ValidateCreate implements admission.Validator so a webhook will be registered for the type.
func (v *GarageAdminTokenValidator) ValidateCreate(ctx context.Context, obj *GarageAdminToken) (admission.Warnings, error) {
	garageadmintokenlog.Info("validate create", "name", obj.Name)
	return obj.validateGarageAdminToken()
}

// ValidateUpdate implements admission.Validator so a webhook will be registered for the type.
func (v *GarageAdminTokenValidator) ValidateUpdate(ctx context.Context, oldObj, newObj *GarageAdminToken) (admission.Warnings, error) {
	garageadmintokenlog.Info("validate update", "name", newObj.Name)
	return newObj.validateGarageAdminToken()
}

// ValidateDelete implements admission.Validator so a webhook will be registered for the type.
func (v *GarageAdminTokenValidator) ValidateDelete(ctx context.Context, obj *GarageAdminToken) (admission.Warnings, error) {
	garageadmintokenlog.Info("validate delete", "name", obj.Name)
	return nil, nil
}

// validateGarageAdminToken validates the GarageAdminToken spec.
func (r *GarageAdminToken) validateGarageAdminToken() (admission.Warnings, error) {
	var warnings admission.Warnings

	// Validate cluster reference
	if r.Spec.ClusterRef.Name == "" {
		return warnings, fmt.Errorf("clusterRef.name is required")
	}

	// Validate expiration settings
	if r.Spec.Expiration != "" && r.Spec.NeverExpires {
		return warnings, fmt.Errorf("expiration and neverExpires are mutually exclusive")
	}

	return warnings, nil
}
