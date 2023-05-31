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
	"text/template"
	"time"

	"github.com/pkg/errors"
	"k8s.io/api/core/v1"
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
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	addonv1 "github.com/tvs/addonconfig/api/v1alpha1"
	"github.com/tvs/addonconfig/util"
	"github.com/tvs/addonconfig/util/conditions"
)

const (
	ClusterKind                     = "Cluster"
	ClusterAPIVersion               = "cluster.x-k8s.io/v1beta1"
	DependencyConstraintUnique      = "unique"
	DependencyConstraintSelectFirst = "selectFirst"
	OutputResourceSecret            = "Secret"
	OutputResourceConfigMap         = "ConfigMap"
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
func (r *AddonConfigReconciler) reconcileValidation(ctx context.Context, atx *addonConfigContext) (ctrl.Result, error) {
	log := ctrl.LoggerFrom(ctx)

	phases := []func(context.Context, *addonConfigContext) (ctrl.Result, error){
		r.reconcileSchema,
		r.reconcileSchemaDefaulting,
		r.reconcileSchemaValidation,
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

	if len(errs) == 0 {
		conditions.MarkTrue(atx.AddonConfig, addonv1.ValidConfigCondition)
	}

	log.Info("Validated AddonConfig against AddonConfigDefinition successfully", "addonConfig.Name", atx.AddonConfig.Name, "addonConfig.Namespace", atx.AddonConfig.Namespace, "addonConfigDefinition.Name", atx.AddonConfigDefinition.Name)

	return res, kerrors.NewAggregate(errs)
}

func (r *AddonConfigReconciler) reconcileSchema(ctx context.Context, atx *addonConfigContext) (ctrl.Result, error) {
	log := ctrl.LoggerFrom(ctx)

	acdKey := client.ObjectKey{Namespace: "", Name: atx.AddonConfig.Spec.DefinitionRef}
	if err := r.Client.Get(ctx, acdKey, atx.AddonConfigDefinition); err != nil {
		if apierrors.IsNotFound(err) {
			log.Info("Could not find AddonConfigDefinition for AddonConfig, requeuing", "refName", acdKey.Name)

			// Unable to find an addon config, can't report that we've observed any
			// generation. Reset to a negative value to eliminate any straggling
			// values
			atx.AddonConfig.Status.ObservedSchemaUID = nil

			conditions.MarkFalse(atx.AddonConfig,
				addonv1.ValidSchemaCondition,
				addonv1.SchemaNotFound,
				addonv1.ConditionSeverityError,
				addonv1.SchemaNotFoundMessage, acdKey.Name)

			return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
		}
	}

	// Update the last observed generation of the schema
	uid := atx.AddonConfigDefinition.GetUID()
	atx.AddonConfig.Status.ObservedSchemaUID = &uid

	// Nothing to validate against so let's get out of here
	if atx.AddonConfigDefinition.Spec.Schema == nil {
		log.Info("No schema to validate against", "addonConfigDefinition", atx.AddonConfigDefinition.Name)

		conditions.MarkFalse(atx.AddonConfig,
			addonv1.ValidSchemaCondition,
			addonv1.InvalidSchema,
			addonv1.ConditionSeverityError,
			addonv1.SchemaUndefinedMessage)

		return ctrl.Result{}, errors.New("No schema to validate against")
	}

	// TODO(tvs): Is there any way to avoid doing this conversion every
	// reconcile?
	if err := apiextensionsv1.Convert_v1_CustomResourceValidation_To_apiextensions_CustomResourceValidation(
		atx.AddonConfigDefinition.Spec.Schema,
		atx.CustomResourceValidation, nil); err != nil {

		conditions.MarkFalse(atx.AddonConfig,
			addonv1.ValidSchemaCondition,
			addonv1.InvalidSchema,
			addonv1.ConditionSeverityError,
			addonv1.InvalidSchemaMessage)

		return ctrl.Result{}, errors.Wrap(err, "failed converting CRD validation to internal version")
	}

	conditions.MarkTrue(atx.AddonConfig, addonv1.ValidSchemaCondition)

	return ctrl.Result{}, nil
}

func (r *AddonConfigReconciler) reconcileSchemaValidation(ctx context.Context, atx *addonConfigContext) (ctrl.Result, error) {
	log := ctrl.LoggerFrom(ctx)

	validator, _, err := validation.NewSchemaValidator(atx.CustomResourceValidation)
	if err != nil {
		return ctrl.Result{}, errors.Wrap(err, "unable to convert internal validation to a validator")
	}

	// TODO(tvs): Test validation success/failure
	if errs := validation.ValidateCustomResource(nil, atx.AddonConfig.Spec.Values, validator); len(errs) > 0 {
		log.Info("Unable to validate AddonConfig against the AddonConfigDefinition's schema")
		atx.AddonConfig.Status.FieldErrors = make(map[string]addonv1.FieldError, len(errs))
		for _, err := range errs {
			atx.AddonConfig.Status.FieldErrors.SetError(err)
		}

		conditions.MarkFalse(atx.AddonConfig,
			addonv1.ValidConfigCondition,
			addonv1.InvalidConfig,
			addonv1.ConditionSeverityError,
			addonv1.InvalidConfigMessage)

		return ctrl.Result{}, errs.ToAggregate()
	}

	// Explicitly reset the FieldErrors if nothing came up so we don't leave
	// stale data
	// TODO(tvs): Test eliminating FieldErrors
	atx.AddonConfig.Status.FieldErrors = make(map[string]addonv1.FieldError, 0)

	return ctrl.Result{}, nil
}

func (r *AddonConfigReconciler) reconcileSchemaDefaulting(ctx context.Context, atx *addonConfigContext) (ctrl.Result, error) {
	log := ctrl.LoggerFrom(ctx)

	// Explicitly fill out structural default values on the AddonConfig spec
	if ss, err := structuralschema.NewStructural(atx.CustomResourceValidation.OpenAPIV3Schema); err == nil {
		raw, err := atx.AddonConfig.Spec.Values.MarshalJSON()
		if err != nil {
			defaultingInternalError(atx.AddonConfig)
			return ctrl.Result{}, errors.Wrap(err, "unable to marshal values to JSON")
		}

		var in interface{}
		if err := json.Unmarshal(raw, &in); err != nil {
			defaultingInternalError(atx.AddonConfig)
			return ctrl.Result{}, errors.Wrap(err, "unable to unmarshal JSON")
		}

		defaultingschema.Default(in, ss)

		var buf bytes.Buffer
		enc := json.NewEncoder(&buf)
		enc.SetIndent("", "  ")
		if err := enc.Encode(in); err != nil {
			defaultingInternalError(atx.AddonConfig)
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
		if err := atx.AddonConfig.Spec.Values.UnmarshalJSON(buf.Bytes()); err != nil {
			defaultingInternalError(atx.AddonConfig)
			return ctrl.Result{}, errors.Wrap(err, "unable to unmarshal the defaulted JSON")
		}
	} else {
		defaultingInternalError(atx.AddonConfig)
		return ctrl.Result{}, errors.Wrap(err, "unable to convert internal schema to structural")
	}

	conditions.MarkTrue(atx.AddonConfig, addonv1.DefaultingCompleteCondition)
	log.Info("Filled out default values for AddonConfig based on AddonConfigDefinition")

	return ctrl.Result{}, nil
}

func (r *AddonConfigReconciler) reconcileTemplateValues(ctx context.Context, atx *addonConfigContext) (ctrl.Result, error) {

	// TODO(tvs): Figure out if we really have to marshal this to a map or not
	var valueMap map[string]interface{}
	jsonVal, err := json.Marshal(atx.AddonConfig.Spec.Values)
	if err != nil {
		return ctrl.Result{}, errors.Wrap(err, "Unable to marshal addonconfig values to json")
	}

	if err = json.Unmarshal(jsonVal, &valueMap); err != nil {
		return ctrl.Result{}, errors.Wrap(err, "Unable to unmarshal addonconfig value json to map")
	}

	atx.TemplateVariables.Values = valueMap

	return ctrl.Result{}, nil
}

func (r *AddonConfigReconciler) reconcileTarget(ctx context.Context, atx *addonConfigContext) (ctrl.Result, error) {
	log := ctrl.LoggerFrom(ctx)

	target := atx.AddonConfig.Spec.Target

	// TODO(tvs): This should be a strict validation on AddonConfig creation
	if target.Name == "" {
		log.Info("AddonConfig does not define a target")
		conditions.MarkFalse(atx.AddonConfig,
			addonv1.ValidTargetCondition,
			addonv1.TargetUndefined,
			addonv1.ConditionSeverityError,
			addonv1.TargetUndefinedMessage)

		// No target identified isn't necessarily an error. May crop up as a
		// templating error later.
		return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
	}

	objRef := &v1.ObjectReference{Kind: ClusterKind, Namespace: atx.AddonConfig.GetNamespace(), Name: target.Name, APIVersion: ClusterAPIVersion}
	unstructuredCluster, err := external.Get(ctx, r.Client, objRef, atx.AddonConfig.GetNamespace())
	if err != nil {
		if !apierrors.IsNotFound(err) {
			return ctrl.Result{}, errors.Wrap(err, "failed to retrieve the target cluster object")
		}

		conditions.MarkFalse(atx.AddonConfig,
			addonv1.ValidTargetCondition,
			addonv1.TargetNotFound,
			addonv1.ConditionSeverityError,
			addonv1.TargetNotFoundMessage)

		// No target identified isn't necessarily an error. May crop up as a
		// templating error later. We requeue with a static time so that we may
		// quickly address the issue instead of entering into the back-off state.
		return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
	}

	infrastructureRefAPIVersion, _, err := unstructured.NestedString(unstructuredCluster.Object, "spec", "infrastructureRef", "apiVersion")
	if err != nil {
		// TODO(tvs): This should probably be expressed with a condition and error
		// message.  This is sort of an internal error but may crop up as a result
		// of changes to the CAPI API and conversions
		return ctrl.Result{}, errors.New("unable to find the value of the nested field spec.infrastructureRef.apiVersion in the target cluster")
	}

	infrastructureRefKind, _, err := unstructured.NestedString(unstructuredCluster.Object, "spec", "infrastructureRef", "kind")
	if err != nil {
		// TODO(tvs): This should probably be expressed with a condition and error
		// message.  This is sort of an internal error but may crop up as a result
		// of changes to the CAPI API and conversions
		return ctrl.Result{}, errors.New("unable to find the value of the nested field spec.infrastructureRef.kind in the target cluster")
	}

	infraType := util.GetInfrastructureProvider(infrastructureRefAPIVersion, infrastructureRefKind)

	atx.TemplateVariables.Default.Cluster = unstructuredCluster.UnstructuredContent()
	atx.TemplateVariables.Default.Infrastructure = infraType

	conditions.MarkTrue(atx.AddonConfig, addonv1.ValidTargetCondition)

	return ctrl.Result{}, nil
}

func (r *AddonConfigReconciler) reconcileDependencies(ctx context.Context, atx *addonConfigContext) (ctrl.Result, error) {
	log := ctrl.LoggerFrom(ctx)

	atx.TemplateVariables.Dependencies = make(map[string]interface{}, len(atx.AddonConfigDefinition.Spec.Dependencies))

	for _, dep := range atx.AddonConfigDefinition.Spec.Dependencies {
		var obj *unstructured.Unstructured

		target := dep.Target

		// TODO(tvs): This should be prevented by a validating webhook
		if target.Name != "" && target.Selector != nil {
			log.Info("Dependency defines a target with both a name and selector", "dependencyName", dep.Name)
			return ctrl.Result{}, errors.New("Dependency target has both name and a label selector")
		}

		if target.Selector != nil {
			// Render our label selector and expressions
			ls, err := renderSelector(target.Selector, atx)
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
				client.InNamespace(atx.AddonConfig.GetNamespace()),
			); err != nil {
				conditions.MarkFalse(atx.AddonConfig,
					addonv1.ValidDependencyCondition,
					addonv1.DependencyNotFound,
					addonv1.ConditionSeverityError,
					addonv1.DependencyNotFoundBySelectorMessage)
				return ctrl.Result{}, errors.Wrap(err, "failed to list dependencies matching the specified selector")
			}

			if len(objectList.Items) == 0 {
				conditions.MarkFalse(atx.AddonConfig,
					addonv1.ValidDependencyCondition,
					addonv1.DependencyNotFound,
					addonv1.ConditionSeverityError,
					addonv1.DependencyNotFoundBySelectorMessage)
				return ctrl.Result{}, errors.Wrap(err, "unable to find any dependency matching the specified selector")
			}

			if len(target.Constraints) > 1 {
				conditions.MarkFalse(atx.AddonConfig,
					addonv1.ValidDependencyCondition,
					addonv1.DependencyConstraintFailed,
					addonv1.ConditionSeverityError,
					addonv1.DependencyInvalidNumberOfDependenciesMessage)
				return ctrl.Result{}, errors.Wrap(err, "invalid number of dependency constraints")
			}

			if len(target.Constraints) == 1 {
				constraint := target.Constraints[0]
				switch constraint.Operator {
				case DependencyConstraintUnique:
					if len(objectList.Items) > 1 {
						conditions.MarkFalse(atx.AddonConfig,
							addonv1.ValidDependencyCondition,
							addonv1.DependencyUniquenessNotSatisfied,
							addonv1.ConditionSeverityError,
							addonv1.DependencyUniquenessNotSatisfiedMessage)
						return ctrl.Result{}, errors.New("multiple dependency resources matched the label selector")
					}
				case DependencyConstraintSelectFirst:
					log.Info("multiple dependency resources matched the label selector")
				default:
					return ctrl.Result{}, errors.New("invalid dependency constraint operator used. Only 'unique' and 'selectFirst' allowed")
				}
			}
			obj = &objectList.Items[0]
		} else {
			t, err := template.New("").Parse(target.Name)
			if err != nil {
				conditions.MarkFalse(atx.AddonConfig,
					addonv1.ValidDependencyCondition,
					addonv1.DependencyTemplatingFailed,
					addonv1.ConditionSeverityError,
					fmt.Sprintf(addonv1.DependencyNameTemplatingFailedMessage, target.Name))
				return ctrl.Result{}, errors.Wrap(err, "Target name could not be parsed")
			}
			var out bytes.Buffer
			err = t.Execute(&out, atx.TemplateVariables)
			if err != nil {
				conditions.MarkFalse(atx.AddonConfig,
					addonv1.ValidDependencyCondition,
					addonv1.DependencyTemplatingFailed,
					addonv1.ConditionSeverityError,
					fmt.Sprintf(addonv1.DependencyNameTemplatingFailedMessage, target.Name))
				return ctrl.Result{}, errors.Wrap(err, "Rendering template failed")
			}
			target.Name = out.String()

			objRef := &v1.ObjectReference{Kind: target.Kind, Namespace: atx.AddonConfig.GetNamespace(), Name: target.Name, APIVersion: target.APIVersion}
			obj, err = external.Get(ctx, r.Client, objRef, atx.AddonConfig.GetNamespace())
			if err != nil {
				conditions.MarkFalse(atx.AddonConfig,
					addonv1.ValidDependencyCondition,
					addonv1.DependencyNotFound,
					addonv1.ConditionSeverityError,
					fmt.Sprintf(addonv1.DependencyNotFoundByNameMessage, target.Name))
				return ctrl.Result{}, errors.Wrapf(err, "unable to find object %s/%s", atx.AddonConfig.GetNamespace(), target.Name)
			}
		}

		atx.TemplateVariables.Dependencies[dep.Name] = obj.UnstructuredContent()
	}

	return ctrl.Result{}, nil
}

func (r *AddonConfigReconciler) reconcileTemplate(ctx context.Context, atx *addonConfigContext) (ctrl.Result, error) {
	log := ctrl.LoggerFrom(ctx)

	if atx.AddonConfigDefinition.Spec.Template != "" {
		t, err := template.New("").Option("missingkey=error").Parse(atx.AddonConfigDefinition.Spec.Template)
		if err != nil {
			conditions.MarkFalse(atx.AddonConfig,
				addonv1.ValidTemplateCondition,
				addonv1.InvalidTemplate,
				addonv1.ConditionSeverityError,
				addonv1.TemplateParseErrorMessage)

			return ctrl.Result{}, errors.Wrap(err, "Unable to parse the template")
		}

		if len(t.Templates()) > 1 {
			conditions.MarkFalse(atx.AddonConfig,
				addonv1.ValidTemplateCondition,
				addonv1.InvalidTemplate,
				addonv1.ConditionSeverityError,
				addonv1.TemplateDefinesNestedTemplatesErrorMessage)

			return ctrl.Result{}, errors.Wrap(err, "Template does not support sub-templates")
		}

		var out bytes.Buffer
		err = t.Execute(&out, atx.TemplateVariables)
		if err != nil {
			if isExecError(err) {
				conditions.MarkFalse(atx.AddonConfig,
					addonv1.ValidTemplateCondition,
					addonv1.FailedRendering,
					addonv1.ConditionSeverityError,
					err.Error())

				return ctrl.Result{}, errors.Wrap(err, "Unable to render the template")
			}

			conditions.MarkFalse(atx.AddonConfig,
				addonv1.ValidTemplateCondition,
				addonv1.FailedWritingRenderedTemplate,
				addonv1.ConditionSeverityError,
				addonv1.TemplateWriteErrorMessage)

			return ctrl.Result{}, errors.Wrap(err, addonv1.TemplateWriteErrorMessage)
		}

		// TODO(tvs): Write template to resultant resource
		atx.RenderedTemplate = out.String()
		log.Info("Successfully rendered template:", "render", r)
	}

	conditions.MarkTrue(atx.AddonConfig, addonv1.ValidTemplateCondition)

	return ctrl.Result{}, nil
}

// TODO(tvs): Code up the saving element
func (r *AddonConfigReconciler) saveRenderedTemplate(ctx context.Context, atx *addonConfigContext) (ctrl.Result, error) {

	log := ctrl.LoggerFrom(ctx)

	outputResource := atx.AddonConfigDefinition.Spec.OutputResource

	if outputResource.Name == "" {
		log.Info("AddonConfigDefinition does not define a outputResource")
		conditions.MarkFalse(atx.AddonConfig,
			addonv1.OutputResourceUpdatedCondition,
			addonv1.OutputResourceUndefined,
			addonv1.ConditionSeverityError,
			addonv1.OutputResourceUndefinedMessage)

		return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
	}

	t, err := template.New("").Parse(outputResource.Name)
	if err != nil {
		conditions.MarkFalse(atx.AddonConfig,
			addonv1.OutputResourceUpdatedCondition,
			addonv1.NameTemplatingFailed,
			addonv1.ConditionSeverityError,
			err.Error())
		return ctrl.Result{}, errors.Wrap(err, "Output resource name could not be parsed")
	}
	var out bytes.Buffer
	err = t.Execute(&out, atx.TemplateVariables)
	if err != nil {
		conditions.MarkFalse(atx.AddonConfig,
			addonv1.OutputResourceUpdatedCondition,
			addonv1.NameTemplatingFailed,
			addonv1.ConditionSeverityError,
			err.Error())
		return ctrl.Result{}, errors.Wrap(err, "Rendering template failed")
	}
	outputResource.Name = out.String()

	data := make(map[string]interface{})
	data["values.yaml"] = atx.RenderedTemplate

	outputResourceUnstructured := &unstructured.Unstructured{}
	outputResourceUnstructured.SetAPIVersion(outputResource.APIVersion)
	outputResourceUnstructured.SetKind(outputResource.Kind)
	outputResourceUnstructured.SetName(outputResource.Name)
	outputResourceUnstructured.SetNamespace(atx.AddonConfig.GetNamespace())

	_, err = controllerutil.CreateOrUpdate(ctx, r.Client, outputResourceUnstructured, func() error {
		if outputResource.Kind == OutputResourceSecret {
			unstructured.SetNestedField(outputResourceUnstructured.Object, data, "stringData")

		} else if outputResource.Kind == OutputResourceConfigMap {
			unstructured.SetNestedField(outputResourceUnstructured.Object, data, "data")
		}
		return nil
	})

	log.Info("Successfully persisted the rendered template into output resource:", "name", outputResource.Name)

	conditions.MarkTrue(atx.AddonConfig, addonv1.OutputResourceUpdatedCondition)

	atx.AddonConfig.SetOutputRef(outputResource.APIVersion, outputResource.Kind, outputResource.Name)

	return ctrl.Result{}, nil
}

// TODO(tvs): Do we need to render keys or can we get away with just the
// values?
func renderSelector(selector *metav1.LabelSelector, atx *addonConfigContext) (*metav1.LabelSelector, error) {
	ls := &metav1.LabelSelector{}
	ls.MatchLabels = make(map[string]string)
	if len(selector.MatchLabels) > 0 || len(selector.MatchExpressions) > 0 {
		for k, v := range selector.MatchLabels {
			t, err := template.New("").Parse(v)
			if err != nil {
				conditions.MarkFalse(atx.AddonConfig,
					addonv1.ValidDependencyCondition,
					addonv1.DependencyTemplatingFailed,
					addonv1.ConditionSeverityError,
					addonv1.DependencySelectorTemplatingFailedMessage)
				return nil, errors.Wrap(err, "Target label selector could not be parsed")
			}

			var out bytes.Buffer
			err = t.Execute(&out, atx.TemplateVariables)
			if err != nil {
				conditions.MarkFalse(atx.AddonConfig,
					addonv1.ValidDependencyCondition,
					addonv1.DependencyTemplatingFailed,
					addonv1.ConditionSeverityError,
					addonv1.DependencySelectorTemplatingFailedMessage)
				return nil, errors.Wrap(err, "Rendering template failed")
			}
			ls.MatchLabels[k] = out.String()
		}

		ls.MatchExpressions = selector.MatchExpressions
		for i, e := range selector.MatchExpressions {

			if len(e.Values) > 0 {
				rendered := make([]string, 0, len(e.Values))

				for _, v := range e.Values {
					var out bytes.Buffer
					t, err := template.New("").Parse(v)
					if err != nil {
						conditions.MarkFalse(atx.AddonConfig,
							addonv1.ValidDependencyCondition,
							addonv1.DependencyTemplatingFailed,
							addonv1.ConditionSeverityError,
							addonv1.DependencySelectorTemplatingFailedMessage)
						return nil, errors.Wrap(err, "Target label expression's value could not be parsed")
					}

					err = t.Execute(&out, atx.TemplateVariables)
					if err != nil {
						conditions.MarkFalse(atx.AddonConfig,
							addonv1.ValidDependencyCondition,
							addonv1.DependencyTemplatingFailed,
							addonv1.ConditionSeverityError,
							addonv1.DependencySelectorTemplatingFailedMessage)
						return nil, errors.Wrap(err, "Rendering template failed")
					}

					rendered = append(rendered, out.String())
				}

				e.Values = rendered
				ls.MatchExpressions[i].Values = rendered
			}
		}
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
