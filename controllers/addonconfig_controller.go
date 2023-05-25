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
	"fmt"

	"k8s.io/apiextensions-apiserver/pkg/apis/apiextensions"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	kerrors "k8s.io/apimachinery/pkg/util/errors"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/source"

	addonv1 "github.com/tvs/addonconfig/api/v1alpha1"
	templatev1 "github.com/tvs/addonconfig/types/template/v1alpha1"
	"github.com/tvs/addonconfig/util"
	"github.com/tvs/addonconfig/util/conditions"
	"github.com/tvs/addonconfig/util/patch"
)

// AddonConfigReconciler reconciles a AddonConfig object
type AddonConfigReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

type addonConfigContext struct {
	AddonConfig              *addonv1.AddonConfig
	AddonConfigDefinition    *addonv1.AddonConfigDefinition
	CustomResourceValidation *apiextensions.CustomResourceValidation
	TemplateVariables        *templatev1.AddonConfigTemplateVariables
	RenderedTemplate         string
}

func newAddonConfigContext(ac *addonv1.AddonConfig) *addonConfigContext {
	return &addonConfigContext{
		AddonConfig:              ac,
		AddonConfigDefinition:    &addonv1.AddonConfigDefinition{},
		CustomResourceValidation: &apiextensions.CustomResourceValidation{},
		TemplateVariables:        &templatev1.AddonConfigTemplateVariables{},
	}
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
		patchOpts := []patch.Option{patch.WithStatusObservedGeneration{}}

		if err := patchAddonConfig(ctx, patchHelper, addonConfig, patchOpts...); err != nil {
			reterr = kerrors.NewAggregate([]error{reterr, err})
		}
	}()

	return r.reconcile(ctx, addonConfig)
}

func (r *AddonConfigReconciler) reconcile(ctx context.Context, addonConfig *addonv1.AddonConfig) (ctrl.Result, error) {
	log := ctrl.LoggerFrom(ctx)
	log.Info("Starting AddonConfig reconciliation")

	// TODO(tvs): This should become a validation on the CRD itself
	if addonConfig.Spec.DefinitionRef == "" {
		log.Info("AddonConfig does not define a definitionRef to validate against")
		return ctrl.Result{}, nil
	}

	atx := newAddonConfigContext(addonConfig)
	phases := []func(context.Context, *addonConfigContext) (ctrl.Result, error){
		r.reconcileValidation,
		r.reconcileTarget,
		r.reconcileDependencies,
		r.reconcileTemplateValues,
		r.reconcileTemplate,
	}

	res := ctrl.Result{}
	errs := []error{}
	for _, phase := range phases {
		phaseResult, err := phase(ctx, atx)
		if err != nil {
			errs = append(errs, err)
		}

		// Even if there's an error, we want to continue so that other conditions
		// can be populated
		if len(errs) > 0 {
			continue
		}

		res = util.LowestNonZeroResult(res, phaseResult)
	}

	// Only want to save the rendered template if nothing has failed
	if len(errs) == 0 {
		result, err := r.saveRenderedTemplate(ctx, atx)
		if err != nil {
			errs = append(errs, err)
		}

		res = util.LowestNonZeroResult(res, result)
	}

	return res, kerrors.NewAggregate(errs)
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
			addonv1.ValidTargetCondition,
			addonv1.ValidTemplateCondition,
		),
	)

	options = append(options,
		patch.WithOwnedConditions{Conditions: []addonv1.ConditionType{
			addonv1.ReadyCondition,
			addonv1.ValidSchemaCondition,
			addonv1.ValidConfigCondition,
			addonv1.DefaultingCompleteCondition,
			addonv1.ValidTargetCondition,
			addonv1.ValidTemplateCondition,
		}},
	)

	return patchHelper.Patch(ctx, addonConfig, options...)
}
