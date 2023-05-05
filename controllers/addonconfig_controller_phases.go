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
	"text/template"
	"time"

	"github.com/pkg/errors"
	"k8s.io/api/core/v1"
	"k8s.io/apiextensions-apiserver/pkg/apis/apiextensions"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	structuralschema "k8s.io/apiextensions-apiserver/pkg/apiserver/schema"
	defaultingschema "k8s.io/apiextensions-apiserver/pkg/apiserver/schema/defaulting"
	"k8s.io/apiextensions-apiserver/pkg/apiserver/validation"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	kerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/json"
	"sigs.k8s.io/cluster-api/controllers/external"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	addonv1 "github.com/tvs/addonconfig/api/v1alpha1"
	templatev1 "github.com/tvs/addonconfig/types/template/v1alpha1"
	"github.com/tvs/addonconfig/util"
	"github.com/tvs/addonconfig/util/conditions"
)

const (
	ClusterKind       = "Cluster"
	ClusterAPIVersion = "cluster.x-k8s.io/v1beta1"
)

// TODO(tvs): Extract some of this to an AddonConfigDefinition controller
// and key off of a condition rather than rebuilding the resource every
// reconciliation
// TODO(tvs): Better validation; right now the API server will accept invalid
// AddonConfigDefinitions (stick `required` under `properties` for example`)
//
//	E0309 00:14:47.141174       1 reflector.go:140] pkg/mod/k8s.io/client-go@v0.26.0/tools/cache/reflector.go:169: Failed to watch *v1alpha1.AddonConfigDefinition: failed to list *v1alpha1.AddonConfigDefinition: json: cannot unmarshal array into Go struct field JSONSchemaProps.items.spec.schema.openAPIV3Schema.properties of type v1.JSONSchemaProps
//
// Validating webhook would probably do the trick...
func (r *AddonConfigReconciler) reconcileValidation(ctx context.Context, ac *addonv1.AddonConfig, acd *addonv1.AddonConfigDefinition, tv *templatev1.AddonConfigTemplateVariables) (ctrl.Result, error) {
	log := ctrl.LoggerFrom(ctx)

	// TODO(tvs): Consider extracting some of this to a typed context
	internalValidation := &apiextensions.CustomResourceValidation{}
	phases := []func(context.Context, *addonv1.AddonConfig, *addonv1.AddonConfigDefinition, *apiextensions.CustomResourceValidation) (ctrl.Result, error){
		r.reconcileSchema,
		r.reconcileSchemaValidation,
		r.reconcileSchemaDefaulting,
	}

	res := ctrl.Result{}
	errs := []error{}
	for _, phase := range phases {
		phaseResult, err := phase(ctx, ac, acd, internalValidation)
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

	if len(errs) == 0 {
		conditions.MarkTrue(ac, addonv1.ValidConfigCondition)
	}

	log.Info("Validated AddonConfig against AddonConfigDefinition successfully", "addonConfig.Name", ac.Name, "addonConfig.Namespace", ac.Namespace, "addonConfigDefinition.Name", acd.Name)

	return res, kerrors.NewAggregate(errs)
}

func (r *AddonConfigReconciler) reconcileSchema(ctx context.Context, ac *addonv1.AddonConfig, acd *addonv1.AddonConfigDefinition, iv *apiextensions.CustomResourceValidation) (ctrl.Result, error) {
	log := ctrl.LoggerFrom(ctx)

	acdKey := client.ObjectKey{Namespace: "", Name: ac.Spec.Type}
	if err := r.Client.Get(ctx, acdKey, acd); err != nil {
		if apierrors.IsNotFound(err) {
			log.Info("Could not find AddonConfigDefinition for AddonConfig, requeuing", "refName", acdKey.Name)

			// Unable to find an addon config, can't report that we've observed any
			// generation. Reset to a negative value to eliminate any straggling
			// values
			ac.Status.ObservedSchemaGeneration = -1

			conditions.MarkFalse(ac,
				addonv1.ValidSchemaCondition,
				addonv1.SchemaNotFound,
				addonv1.ConditionSeverityError,
				addonv1.SchemaNotFoundMessage, acdKey.Name)

			return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
		}
	}

	// Update the last observed generation of the schema
	ac.Status.ObservedSchemaGeneration = acd.GetGeneration()

	// Nothing to validate against so let's get out of here
	if acd.Spec.Schema == nil {
		log.Info("No schema to validate against", "addonConfigDefinition", acd.Name)

		conditions.MarkFalse(ac,
			addonv1.ValidSchemaCondition,
			addonv1.SchemaNotFound,
			addonv1.ConditionSeverityError,
			addonv1.SchemaNotFoundMessage, acd.Name)

		return ctrl.Result{}, errors.New("No schema to validate against")
	}

	// TODO(tvs): Is there any way to avoid doing this conversion every
	// reconcile?
	if err := apiextensionsv1.Convert_v1_CustomResourceValidation_To_apiextensions_CustomResourceValidation(acd.Spec.Schema, iv, nil); err != nil {
		conditions.MarkFalse(ac,
			addonv1.ValidSchemaCondition,
			addonv1.InvalidSchema,
			addonv1.ConditionSeverityError,
			addonv1.InvalidSchemaMessage)

		return ctrl.Result{}, errors.Wrap(err, "failed converting CRD validation to internal version")
	}

	conditions.MarkTrue(ac, addonv1.ValidSchemaCondition)

	return ctrl.Result{}, nil
}

func (r *AddonConfigReconciler) reconcileSchemaValidation(ctx context.Context, ac *addonv1.AddonConfig, acd *addonv1.AddonConfigDefinition, iv *apiextensions.CustomResourceValidation) (ctrl.Result, error) {
	log := ctrl.LoggerFrom(ctx)

	validator, _, err := validation.NewSchemaValidator(iv)
	if err != nil {
		return ctrl.Result{}, errors.Wrap(err, "unable to convert internal validation to a validator")
	}

	// TODO(tvs): Test validation success/failure
	if errs := validation.ValidateCustomResource(nil, ac.Spec.Values, validator); len(errs) > 0 {
		log.Info("Unable to validate AddonConfig against the AddonConfigDefinition's schema")
		ac.Status.FieldErrors = make(map[string]addonv1.FieldError, len(errs))
		for _, err := range errs {
			ac.Status.FieldErrors.SetError(err)
		}

		conditions.MarkFalse(ac,
			addonv1.ValidConfigCondition,
			addonv1.InvalidConfig,
			addonv1.ConditionSeverityError,
			addonv1.InvalidConfigMessage)

		return ctrl.Result{}, errs.ToAggregate()
	}

	// Explicitly reset the FieldErrors if nothing came up so we don't leave
	// stale data
	// TODO(tvs): Test eliminating FieldErrors
	ac.Status.FieldErrors = make(map[string]addonv1.FieldError, 0)

	return ctrl.Result{}, nil
}

func (r *AddonConfigReconciler) reconcileSchemaDefaulting(ctx context.Context, ac *addonv1.AddonConfig, _ *addonv1.AddonConfigDefinition, internalValidation *apiextensions.CustomResourceValidation) (ctrl.Result, error) {
	log := ctrl.LoggerFrom(ctx)

	// Explicitly fill out structural default values on the AddonConfig spec
	if ss, err := structuralschema.NewStructural(internalValidation.OpenAPIV3Schema); err == nil {
		raw, err := ac.Spec.Values.MarshalJSON()
		if err != nil {
			defaultingInternalError(ac)
			return ctrl.Result{}, errors.Wrap(err, "unable to marshal values to JSON")
		}

		var in interface{}
		if err := json.Unmarshal(raw, &in); err != nil {
			defaultingInternalError(ac)
			return ctrl.Result{}, errors.Wrap(err, "unable to unmarshal JSON")
		}

		defaultingschema.Default(in, ss)

		var buf bytes.Buffer
		enc := json.NewEncoder(&buf)
		enc.SetIndent("", "  ")
		if err := enc.Encode(in); err != nil {
			defaultingInternalError(ac)
			return ctrl.Result{}, errors.Wrap(err, "unable to encode defaulted JSON")
		}

		// TODO(tvs): Weigh the pros and cons of rendered defaults:
		//    * Rendering defaults make future upgrades safer as there is no change
		//      in configuration.
		//    * Rendering defaults means that users have to intervene to update
		//      values to take new defaults.
		//    * Not rendering defaults means that users don't have to intervene
		//      when rebasing to a new AddonConfigDefinition.
		//    * Not rendering defaults means that addon configuration might change
		//      when rebasing to a new AddonConfigDefinition.
		// Persist defaulted values back to the AddonConfig
		if err := ac.Spec.Values.UnmarshalJSON(buf.Bytes()); err != nil {
			defaultingInternalError(ac)
			return ctrl.Result{}, errors.Wrap(err, "unable to unmarshal the defaulted JSON")
		}

	} else {
		defaultingInternalError(ac)
		return ctrl.Result{}, errors.Wrap(err, "unable to convert internal schema to structural")
	}

	conditions.MarkTrue(ac, addonv1.DefaultingCompleteCondition)
	log.Info("Filled out default values for AddonConfig based on AddonConfigDefinition")

	return ctrl.Result{}, nil
}

func (r *AddonConfigReconciler) reconcileTemplateValues(ctx context.Context, ac *addonv1.AddonConfig, _ *addonv1.AddonConfigDefinition, tv *templatev1.AddonConfigTemplateVariables) (ctrl.Result, error) {

	// TODO(tvs): Figure out if we really have to marshal this to a map or not
	var valueMap map[string]interface{}
	jsonVal, err := json.Marshal(ac.Spec.Values)
	if err != nil {
		return ctrl.Result{}, errors.Wrap(err, "Unable to marshal addonconfig values to json")
	}

	if err = json.Unmarshal(jsonVal, &valueMap); err != nil {
		return ctrl.Result{}, errors.Wrap(err, "Unable to unmarshal addonconfig value json to map")
	}

	tv.Values = valueMap

	return ctrl.Result{}, nil
}

func (r *AddonConfigReconciler) reconcileTarget(ctx context.Context, ac *addonv1.AddonConfig, _ *addonv1.AddonConfigDefinition, tv *templatev1.AddonConfigTemplateVariables) (ctrl.Result, error) {
	log := ctrl.LoggerFrom(ctx)

	target := ac.Spec.Target

	if target.Name == "" && target.Selector == nil {
		log.Info("AddonConfig does not define a target")
		conditions.MarkFalse(ac,
			addonv1.ValidTargetCondition,
			addonv1.TargetNotFound,
			addonv1.ConditionSeverityError,
			addonv1.TargetNotDefinedMessage)

		// No target identified isn't necessarily an error. May crop up as a
		// templating error later.
		return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
	}

	// TODO(tvs): This should be prevented by a validating webhook
	if target.Name != "" && target.Selector != nil {
		log.Info("AddonConfig defines a target with both a name and selector")
		conditions.MarkFalse(ac,
			addonv1.ValidTargetCondition,
			addonv1.TargetCoDefined,
			addonv1.ConditionSeverityError,
			addonv1.TargetCoDefinedMessage)

		return ctrl.Result{}, errors.New("AddonConfig has both a label selector and explicit cluster name")
	}

	var unstructuredCluster *unstructured.Unstructured
	if target.Selector != nil {
		selector, err := metav1.LabelSelectorAsSelector(target.Selector)
		if err != nil {
			return ctrl.Result{}, errors.Wrap(err, "unable to convert AddonConfig's LabelSelector into Selector")
		}

		var clusterList unstructured.UnstructuredList
		clusterList.SetAPIVersion(ClusterAPIVersion)
		clusterList.SetKind(ClusterKind)
		if err := r.Client.List(
			ctx,
			&clusterList,
			client.MatchingLabelsSelector{Selector: selector},
			client.InNamespace(ac.GetNamespace()),
		); err != nil {
			return ctrl.Result{}, errors.Wrap(err, "failed to list clusters")
		}

		if len(clusterList.Items) == 0 {
			conditions.MarkFalse(ac,
				addonv1.ValidTargetCondition,
				addonv1.TargetNotFound,
				addonv1.ConditionSeverityError,
				addonv1.TargetNotDefinedMessage)

			// No target identified isn't necessarily an error. May crop up as a
			// templating error later.
			return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
		}

		// TODO(tvs): Handle multi-cluster selection
		if len(clusterList.Items) > 1 {
			conditions.MarkFalse(ac,
				addonv1.ValidTargetCondition,
				addonv1.TargetNotUnique,
				addonv1.ConditionSeverityError,
				addonv1.TargetNotUniqueMessage)

			// No target identified isn't necessarily an error. May crop up as a
			// templating error later.
			return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
		}

		unstructuredCluster = &clusterList.Items[0]
	} else {
		var err error

		objRef := &v1.ObjectReference{Kind: ClusterKind, Namespace: ac.GetNamespace(), Name: target.Name, APIVersion: ClusterAPIVersion}
		unstructuredCluster, err = external.Get(ctx, r.Client, objRef, ac.GetNamespace())
		if err != nil {
			if !apierrors.IsNotFound(err) {
				return ctrl.Result{}, errors.Wrap(err, "failed to retrieve the target cluster object")
			}

			conditions.MarkFalse(ac,
				addonv1.ValidTargetCondition,
				addonv1.TargetNotFound,
				addonv1.ConditionSeverityError,
				addonv1.TargetNotFoundMessage)

			// No target identified isn't necessarily an error. May crop up as a
			// templating error later.
			return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
		}
	}

	infrastructureRefAPIVersion, _, err := unstructured.NestedString(unstructuredCluster.Object, "spec", "infrastructureRef", "apiVersion")
	if err != nil {
		return ctrl.Result{}, errors.New("unable to find the value of the nested field spec.infrastructureRef.apiVersion in the target cluster")
	}

	infrastructureRefKind, _, err := unstructured.NestedString(unstructuredCluster.Object, "spec", "infrastructureRef", "kind")
	if err != nil {
		return ctrl.Result{}, errors.New("unable to find the value of the nested field spec.infrastructureRef.kind in the target cluster")
	}

	infraType := util.GetInfrastructureProvider(infrastructureRefAPIVersion, infrastructureRefKind)

	// TODO(tvs): Do we really want to deal with having to provide this plumbing
	// for every infrastructure type we deign to support? Seems like we should
	// find a better way to convert the InfrastructureRef to a provider type that
	// doesn't necessitate plumbing in supported types
	if infraType == templatev1.InfrastructureProviderUnknown {
		conditions.MarkFalse(ac,
			addonv1.ValidTargetCondition,
			addonv1.TargetUnsupported,
			addonv1.ConditionSeverityError,
			addonv1.TargetUnsupportedMessage)

		return ctrl.Result{}, errors.New("cluster has an infrastructure reference of an unsupported type")
	}

	tv.Default.Cluster = unstructuredCluster.UnstructuredContent()
	tv.Default.Infrastructure = infraType

	conditions.MarkTrue(ac, addonv1.ValidTargetCondition)

	return ctrl.Result{}, nil
}

func (r *AddonConfigReconciler) reconcileDependencies(ctx context.Context, ac *addonv1.AddonConfig, acd *addonv1.AddonConfigDefinition, tv *templatev1.AddonConfigTemplateVariables) (ctrl.Result, error) {
	log := ctrl.LoggerFrom(ctx)

	var err error

	tv.Dependencies = make(map[string]interface{}, len(acd.Spec.Dependencies))

	for _, dep := range acd.Spec.Dependencies {
		var obj *unstructured.Unstructured

		target := dep.Target

		// TODO(tvs): This should be prevented by a validating webhook
		if target.Name != "" && target.Selector != nil {
			log.Info("Dependency defines a target with both a name and selector", "dependencyName", dep.Name)
			return ctrl.Result{}, errors.New("Dependency target has both name and a label selector")
		}

		if target.Selector != nil {
			// Render our label selector and expressions
			ls, err := renderSelector(target.Selector, tv)
			if err != nil {
				return ctrl.Result{}, errors.Wrap(err, "unable to render target selector")
			}

			selector, err := metav1.LabelSelectorAsSelector(ls)
			if err != nil {
				return ctrl.Result{}, errors.Wrap(err, "unable to convert rendered LabelSelector into Selector")
			}

			var objectList unstructured.UnstructuredList
			objectList.SetAPIVersion(target.APIVersion)
			objectList.SetKind(target.Kind)

			if err := r.Client.List(
				ctx,
				&objectList,
				client.MatchingLabelsSelector{Selector: selector},
				client.InNamespace(ac.GetNamespace()),
			); err != nil {
				return ctrl.Result{}, errors.Wrap(err, "failed to list dependencies matching the specified selector")
			}

			if len(objectList.Items) == 0 {
				return ctrl.Result{}, errors.Wrap(err, "unable to find any dependency matching the specified selector")
			}

			if len(objectList.Items) >= 1 {
				obj = &objectList.Items[0]
			}
		} else {
			objRef := &v1.ObjectReference{Kind: target.Kind, Namespace: ac.GetNamespace(), Name: target.Name, APIVersion: target.APIVersion}
			obj, err = external.Get(ctx, r.Client, objRef, ac.GetNamespace())
			if err != nil {
				return ctrl.Result{}, errors.Wrapf(err, "unable to find object %s/%s", ac.GetNamespace(), target.Name)
			}
		}

		tv.Dependencies[dep.Name] = obj.UnstructuredContent()
	}

	return ctrl.Result{}, nil
}

func (r *AddonConfigReconciler) reconcileTemplate(ctx context.Context, ac *addonv1.AddonConfig, acd *addonv1.AddonConfigDefinition, tv *templatev1.AddonConfigTemplateVariables) (ctrl.Result, error) {
	log := ctrl.LoggerFrom(ctx)

	if acd.Spec.Template != "" {
		t, err := template.New("").Option("missingkey=error").Parse(acd.Spec.Template)
		if err != nil {
			conditions.MarkFalse(ac,
				addonv1.ValidTemplateCondition,
				addonv1.InvalidTemplate,
				addonv1.ConditionSeverityError,
				addonv1.TemplateParseErrorMessage)

			return ctrl.Result{}, errors.Wrap(err, "Unable to parse template")
		}

		if len(t.Templates()) > 1 {
			conditions.MarkFalse(ac,
				addonv1.ValidTemplateCondition,
				addonv1.InvalidTemplate,
				addonv1.ConditionSeverityError,
				addonv1.TemplateDefinesSubTemplatesErrorMessage)

			return ctrl.Result{}, errors.Wrap(err, "Template does not support sub-templates")
		}

		var out bytes.Buffer
		err = t.Execute(&out, tv)
		if err != nil {
			if isExecError(err) {
				conditions.MarkFalse(ac,
					addonv1.ValidTemplateCondition,
					addonv1.FailedRendering,
					addonv1.ConditionSeverityError,
					err.Error())

				return ctrl.Result{}, errors.Wrap(err, "Unable to render template")
			}

			conditions.MarkFalse(ac,
				addonv1.ValidTemplateCondition,
				addonv1.InvalidTemplate,
				addonv1.ConditionSeverityError,
				addonv1.TemplateRenderErrorMessage)

			return ctrl.Result{}, errors.Wrap(err, "Unable to write template")

		}

		// TODO(tvs): Write template to resultant resource
		log.Info("Successfully rendered template:", "render", out.String())
	}

	conditions.MarkTrue(ac, addonv1.ValidTemplateCondition)

	return ctrl.Result{}, nil
}

// TODO(tvs): Do we need to render keys or can we get away with just the
// values?
func renderSelector(selector *metav1.LabelSelector, tv *templatev1.AddonConfigTemplateVariables) (*metav1.LabelSelector, error) {
	ls := &metav1.LabelSelector{}
	ls.MatchLabels = make(map[string]string)
	if len(selector.MatchLabels) > 0 || len(selector.MatchExpressions) > 0 {
		for k, v := range selector.MatchLabels {
			t, err := template.New("").Parse(v)
			if err != nil {
				return nil, errors.Wrap(err, "Target label selector could not be parsed")
			}

			var out bytes.Buffer
			err = t.Execute(&out, tv)
			if err != nil {
				return nil, errors.Wrap(err, "Rendering template failed")
			}
			ls.MatchLabels[k] = out.String()
		}

		// TODO(tvs): This can be removed if we choose not to render matchLabel keys
		//ls.MatchExpressions = selector.MatchExpressions
		//for _, e := range selector.MatchExpressions {
		//	var out bytes.Buffer

		//	t, err := template.New("").Parse(e.Key)
		//	if err != nil {
		//		return nil, errors.Wrap(err, "Target label expression could not be parsed")
		//	}

		//	err = t.Execute(&out, tv)
		//	if err != nil {
		//		return nil, errors.Wrap(err, "Rendering template failed")
		//	}

		//	e.Key = out.String()
		//}
	}

	return ls, nil
}

func isExecError(err error) bool {
	_, ok := err.(template.ExecError)
	return ok
}

// TODO(tvs): Clean up reconciliation logic so we only have to use this once
func defaultingInternalError(addonConfig *addonv1.AddonConfig) {
	conditions.MarkFalse(addonConfig,
		addonv1.DefaultingCompleteCondition,
		addonv1.DefaultingInternalError,
		addonv1.ConditionSeverityError,
		addonv1.DefaultingInternalErrorMessage)
}
