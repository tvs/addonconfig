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
	"bytes"
	"context"
	"fmt"
	"time"

	"github.com/pkg/errors"
	"k8s.io/apiextensions-apiserver/pkg/apis/apiextensions"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	structuralschema "k8s.io/apiextensions-apiserver/pkg/apiserver/schema"
	defaultingschema "k8s.io/apiextensions-apiserver/pkg/apiserver/schema/defaulting"
	"k8s.io/apiextensions-apiserver/pkg/apiserver/validation"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	kerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/json"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/source"

	addonv1 "github.com/tvs/addonconfig/api/v1alpha1"
	"github.com/tvs/addonconfig/util"
	"github.com/tvs/addonconfig/util/conditions"
	"github.com/tvs/addonconfig/util/patch"
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
func (r *AddonConfigReconciler) Reconcile(ctx context.Context, req ctrl.Request) (_ ctrl.Result, reterr error) {
	addonConfig := &addonv1.AddonConfig{}
	if err := r.Client.Get(ctx, req.NamespacedName, addonConfig); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
	}

	patchHelper, err := patch.NewHelper(addonConfig, r.Client)
	if err != nil {
		return ctrl.Result{}, err
	}

	// Always attempt to patch the AddonConfig status after each reconciliation
	defer func() {
		patchOpts := []patch.Option{}
		if reterr == nil {
			patchOpts = append(patchOpts, patch.WithStatusObservedGeneration{})
		}

		if err := patchAddonConfig(ctx, patchHelper, addonConfig, patchOpts...); err != nil {
			reterr = kerrors.NewAggregate([]error{reterr, err})
		}
	}()

	return r.reconcile(ctx, addonConfig)
}

func (r *AddonConfigReconciler) reconcile(ctx context.Context, addonConfig *addonv1.AddonConfig) (ctrl.Result, error) {
	log := ctrl.LoggerFrom(ctx)
	log.Info("Starting AddonConfig reconciliation")

	// TODO(tvs): Better validation; right now the API server will accept invalid
	// AddonConfigDefinitions (stick `required` under `properties` for example`)
	//   E0309 00:14:47.141174       1 reflector.go:140] pkg/mod/k8s.io/client-go@v0.26.0/tools/cache/reflector.go:169: Failed to watch *v1alpha1.AddonConfigDefinition: failed to list *v1alpha1.AddonConfigDefinition: json: cannot unmarshal array into Go struct field JSONSchemaProps.items.spec.schema.openAPIV3Schema.properties of type v1.JSONSchemaProps
	// Validating webhook would probably do the trick...
	addonConfigDefinition := &addonv1.AddonConfigDefinition{}
	acdKey := client.ObjectKey{Namespace: "", Name: addonConfig.Spec.Type}
	if err := r.Client.Get(ctx, acdKey, addonConfigDefinition); err != nil {
		if apierrors.IsNotFound(err) {
			log.Info("Could not find AddonConfigDefinition for AddonConfig, requeuing", "refName", acdKey.Name)

			conditions.MarkFalse(addonConfig,
				addonv1.ValidSchemaCondition,
				addonv1.SchemaNotFound,
				addonv1.ConditionSeverityError,
				addonv1.SchemaNotFoundMessage, acdKey.Name)

			return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
		}
	}

	// Manually updated the last observed generation of the schema
	addonConfig.Status.ObservedSchemaGeneration = addonConfigDefinition.GetGeneration()

	// Nothing to validate against so let's get out of here
	if addonConfigDefinition.Spec.Schema == nil {
		log.Info("No schema to validate against", "refName", acdKey.Name)

		conditions.MarkFalse(addonConfig,
			addonv1.ValidSchemaCondition,
			addonv1.SchemaNotFound,
			addonv1.ConditionSeverityError,
			addonv1.SchemaNotFoundMessage, acdKey.Name)

		return ctrl.Result{}, nil
	}

	conditions.MarkTrue(addonConfig, addonv1.ValidSchemaCondition)

	internalValidation := &apiextensions.CustomResourceValidation{}
	if err := apiextensionsv1.Convert_v1_CustomResourceValidation_To_apiextensions_CustomResourceValidation(addonConfigDefinition.Spec.Schema, internalValidation, nil); err != nil {
		return ctrl.Result{}, errors.Wrap(err, "failed converting CRD validation to internal version")
	}

	validator, _, err := validation.NewSchemaValidator(internalValidation)
	if err != nil {
		return ctrl.Result{}, errors.Wrap(err, "unable to convert internal validation to a validator")
	}

	if errs := validation.ValidateCustomResource(nil, addonConfig.Spec.Values, validator); len(errs) > 0 {
		log.Info("Unable to validate AddonConfig against the AddonConfigDefinition's schema")
		addonConfig.Status.FieldErrors = make(map[string]addonv1.FieldError, len(errs))
		for _, err := range errs {
			addonConfig.Status.FieldErrors.SetError(err)
		}

		conditions.MarkFalse(addonConfig,
			addonv1.ValidConfigCondition,
			addonv1.InvalidConfig,
			addonv1.ConditionSeverityError,
			addonv1.InvalidConfigMessage)

		// This isn't really an error, so we just need to requeue
		return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
	}

	// Explicitly reset the FieldErrors if nothing came up so we don't leave
	// stale data
	addonConfig.Status.FieldErrors = make(map[string]addonv1.FieldError, 0)

	log.Info("Validated AddonConfig against AddonConfigDefinition successfully", "addonConfig.Name", addonConfig.Name, "addonConfig.Namespace", addonConfig.Namespace, "addonConfigDefinition.Name", addonConfigDefinition.Name)
	conditions.MarkTrue(addonConfig, addonv1.ValidConfigCondition)

	// Explicitly fill out structural default values on the AddonConfig spec
	if ss, err := structuralschema.NewStructural(internalValidation.OpenAPIV3Schema); err == nil {
		raw, err := addonConfig.Spec.Values.MarshalJSON()
		if err != nil {
			defaultingInternalError(addonConfig)
			return ctrl.Result{}, errors.Wrap(err, "unable to marshal values to JSON")
		}

		var in interface{}
		if err := json.Unmarshal(raw, &in); err != nil {
			defaultingInternalError(addonConfig)
			return ctrl.Result{}, errors.Wrap(err, "unable to unmarshal JSON")
		}

		defaultingschema.Default(in, ss)

		var buf bytes.Buffer
		enc := json.NewEncoder(&buf)
		enc.SetIndent("", "  ")
		if err := enc.Encode(in); err != nil {
			defaultingInternalError(addonConfig)
			return ctrl.Result{}, errors.Wrap(err, "unable to encode defaulted JSON")
		}

		if err := addonConfig.Spec.Values.UnmarshalJSON(buf.Bytes()); err != nil {
			defaultingInternalError(addonConfig)
			return ctrl.Result{}, errors.Wrap(err, "unable to unmarshal the defaulted JSON")
		}

	} else {
		defaultingInternalError(addonConfig)
		return ctrl.Result{}, errors.Wrap(err, "unable to convert internal schema to structural")
	}
	conditions.MarkTrue(addonConfig, addonv1.DefaultingCompleteCondition)
	log.Info("Filled out default values for AddonConfig based on AddonConfigDefinition")

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *AddonConfigReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&addonv1.AddonConfig{}).
		Watches(
			&source.Kind{Type: &addonv1.AddonConfigDefinition{}},
			handler.EnqueueRequestsFromMapFunc(r.addonConfigDefinitionToAddonConfig),
		).
		Complete(r)
}

func (r *AddonConfigReconciler) addonConfigDefinitionToAddonConfig(o client.Object) []ctrl.Request {
	d, ok := o.(*addonv1.AddonConfigDefinition)
	if !ok {
		panic(fmt.Sprintf("Expected an AddonConfigDefinition but got a %T", o))
	}

	configs, err := util.GetAddonConfigsByType(context.TODO(), r.Client, d.Name)
	if err != nil {
		return nil
	}

	request := make([]ctrl.Request, 0, len(configs))
	for i := range configs {
		request = append(request, ctrl.Request{
			NamespacedName: util.ObjectKey(&configs[i]),
		})
	}
	return request
}

func patchAddonConfig(ctx context.Context, patchHelper *patch.Helper, addonConfig *addonv1.AddonConfig, options ...patch.Option) error {

	conditions.SetSummary(addonConfig,
		conditions.WithConditions(
			addonv1.ValidSchemaCondition,
			addonv1.ValidConfigCondition,
			addonv1.DefaultingCompleteCondition,
		),
	)

	options = append(options,
		patch.WithOwnedConditions{Conditions: []addonv1.ConditionType{
			addonv1.ReadyCondition,
			addonv1.ValidSchemaCondition,
			addonv1.ValidConfigCondition,
		}},
	)
	return patchHelper.Patch(ctx, addonConfig, options...)
}

// TODO(tvs): Clean up reconciliation logic so we only have to use this once
func defaultingInternalError(addonConfig *addonv1.AddonConfig) {
	conditions.MarkFalse(addonConfig,
		addonv1.DefaultingCompleteCondition,
		addonv1.DefaultingInternalError,
		addonv1.ConditionSeverityError,
		addonv1.DefaultingInternalErrorMessage)
}
