/*
Copyright 2023.

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

package controllers

import (
	"context"
	"time"

	"github.com/pkg/errors"
	"k8s.io/apiextensions-apiserver/pkg/apis/apiextensions"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apiextensions-apiserver/pkg/apiserver/validation"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	addonv1 "tvs.io/addonconfig/api/v1alpha1"
)

// AddonConfigReconciler reconciles a AddonConfig object
type AddonConfigReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

//+kubebuilder:rbac:groups=addon.tvs.io,resources=addonconfigs,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=addon.tvs.io,resources=addonconfigs/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=addon.tvs.io,resources=addonconfigs/finalizers,verbs=update
//+kubebuilder:rbac:groups=addon.tvs.io,resources=addonconfigdefinitions,verbs=get;list;watch

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.14.1/pkg/reconcile
func (r *AddonConfigReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := ctrl.LoggerFrom(ctx)
	log.Info("Starting AddonConfig reconciliation")

	addonConfig := &addonv1.AddonConfig{}
	if err := r.Client.Get(ctx, req.NamespacedName, addonConfig); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
	}

	addonConfigDefinition := &addonv1.AddonConfigDefinition{}
	acdKey := client.ObjectKey{Namespace: "", Name: addonConfig.Spec.Type}
	if err := r.Client.Get(ctx, acdKey, addonConfigDefinition); err != nil {
		if apierrors.IsNotFound(err) {
			log.Info("Could not find AddonConfigDefinition for AddonConfig, requeuing", "refName", acdKey.Name)
			// TODO(tvs): Set condition, but it's not an error we just need to requeue
			// until it shows up
			return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
		}
	}

	// Nothing to validate against so let's get out of here
	if addonConfigDefinition.Spec.Schema == nil {
		log.Info("No schema to validate against", "refName", acdKey.Name)
		return ctrl.Result{}, nil
	}

	internalValidation := &apiextensions.CustomResourceValidation{}
	if err := apiextensionsv1.Convert_v1_CustomResourceValidation_To_apiextensions_CustomResourceValidation(addonConfigDefinition.Spec.Schema, internalValidation, nil); err != nil {
		return ctrl.Result{}, errors.Wrap(err, "failed converting CRD validation to internal version")
	}

	validator, _, err := validation.NewSchemaValidator(internalValidation)
	if err != nil {
		return ctrl.Result{}, errors.Wrap(err, "unable to convert internal validation to a validator")
	}

	if errs := validation.ValidateCustomResource(nil, addonConfig.Spec.Values, validator); len(errs) > 0 {
		log.Info("Unable to validate AddonConfig against the AddonConfigDefinition's schema", "errs", errs)
		// TODO(tvs): Set a condition with the errors, but it's not an error, we just need to requeue
		return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
	}

	log.Info("Validated AddonConfig against AddonConfigDefinition successfully", "addonConfig.Name", addonConfig.Name, "addonConfig.Namespace", addonConfig.Namespace, "addonConfigDefinition.Name", addonConfigDefinition.Name)
	// TODO(tvs): Set condition and ready status

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *AddonConfigReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&addonv1.AddonConfig{}).
		Complete(r)
}
