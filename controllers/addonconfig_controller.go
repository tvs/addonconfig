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
	"os"
	"text/template"
	"time"

	"github.com/pkg/errors"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apiextensions-apiserver/pkg/apis/apiextensions"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	structuralschema "k8s.io/apiextensions-apiserver/pkg/apiserver/schema"
	defaultingschema "k8s.io/apiextensions-apiserver/pkg/apiserver/schema/defaulting"
	"k8s.io/apiextensions-apiserver/pkg/apiserver/validation"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	kerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/json"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
	clusterapiv1beta1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/cluster-api/controllers/external"
	controlplanev1beta1 "sigs.k8s.io/cluster-api/controlplane/kubeadm/api/v1beta1"
	clusterapipatchutil "sigs.k8s.io/cluster-api/util/patch"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
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
	DynamicClient         dynamic.Interface
	CachedDiscoveryClient discovery.CachedDiscoveryInterface
	Scheme                *runtime.Scheme
}

type ClusterResources struct {
	Cluster               *clusterapiv1beta1.Cluster
	InfrastructureCluster *unstructured.Unstructured
	MachineDeployments    *clusterapiv1beta1.MachineDeploymentList
	KubeadmControlPlane   *controlplanev1beta1.KubeadmControlPlane
	CfgTemplates          []*unstructured.Unstructured
	MachineTemplates      []*unstructured.Unstructured
	ClusterGroupVersion   string
	ClusterKind           string
}

type addonConfiguration struct {
	Values       apiextensionsv1.JSON
	Cluster      *ClusterResources
	Dependencies []*unstructured.Unstructured
}

type dependencySpec struct {
	Kind          string              `yaml:"kind,omitempty"`
	Name          string              `yaml:"name,omitempty"`
	Namespace     string              `yaml:"namespace,omitempty"`
	ApiVersion    string              `yaml:"apiVersion,omitempty"`
	LabelSelector labelSelectorKeyVal `yaml:"labelSelector,omitempty"`
}

type labelSelectorKeyVal struct {
	Key   string `yaml:"key,omitempty"`
	Value string `json:"value,omitempty"`
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

	cluster, err := util.GetOwnerCluster(ctx, r.Client, addonConfig, addonConfig.Namespace)
	if err != nil {
		if apierrors.IsNotFound(err) && cluster != nil {
			log.Info(fmt.Sprintf("'%s/%s' is listed as owner reference but could not be found", cluster.Namespace, cluster.Name))
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, nil
	}

	patchHelper, err := clusterapipatchutil.NewHelper(cluster, r.Client)
	if err != nil {
		return ctrl.Result{}, nil
	}

	cluster.Annotations["tkg.tanzu.vmware.com/tkg-ip-family"] = "ipv4"
	cluster.Annotations["tkg.tanzu.vmware.com/tkg-http-proxy"] = "http://10.202.27.228:3128"
	cluster.Annotations["tkg.tanzu.vmware.com/tkg-https-proxy"] = "http://10.202.27.228:3128"
	cluster.Annotations["tkg.tanzu.vmware.com/tkg-no-proxy"] = "10.246.0.0/16"

	if err := patchHelper.Patch(ctx, cluster); err != nil {
		log.Error(err, "unable to patch Cluster Annotation")
		return ctrl.Result{}, nil
	}

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

		clusterResources, err := r.getCAPIObjectsTree(ctx, cluster)
		if err != nil {
			return ctrl.Result{}, err
		}

		dependencyObjects, err := r.getDependencyObjects(ctx, addonConfigDefinition)
		if err != nil {
			return ctrl.Result{}, err
		}

		addonCfg := addonConfiguration{
			Values:       addonConfig.Spec.Values,
			Cluster:      clusterResources,
			Dependencies: dependencyObjects,
		}

		// TODO: dependency in ACT , find all these sub-resources and use those in templating as well

		addonConfigTemplate, err := template.New("addonConfigurationTemplate").Option("missingkey=invalid").Parse(addonConfigDefinition.Spec.Template)
		if err != nil {
			return ctrl.Result{}, errors.Wrap(err, "unable to create template")
		}

		cfg, _ := json.Marshal(addonCfg)
		addonConfigurationMap := map[string]interface{}{}
		if err := json.Unmarshal(cfg, &addonConfigurationMap); err != nil {
			return ctrl.Result{}, errors.Wrap(err, "unable to unmarshal addon config")
		}

		if err = addonConfigTemplate.Execute(os.Stdout, addonConfigurationMap); err != nil {
			return ctrl.Result{}, errors.Wrap(err, "unable to apply the parsed addon config definition template to the addon config object")
		}

		f, err := os.CreateTemp("", addonConfig.Name+"-")
		if err != nil {
			return ctrl.Result{}, errors.Wrap(err, "unable to create output file for addon data values")
		}

		defer func() {
			f.Close()
			if err := os.Remove(f.Name()); err != nil {
				log.Error(err, fmt.Sprintf("unable to remove file %s", f.Name()))
			}
		}()

		if err = addonConfigTemplate.Execute(f, addonConfigurationMap); err != nil {
			return ctrl.Result{}, errors.Wrap(err, "unable to apply the parsed addon config definition template to the addon config object")
		}

		b, err := os.ReadFile(f.Name())
		if err != nil {
			return ctrl.Result{}, errors.Wrap(err, "unable to read file contents")
		}

		secret := &v1.Secret{ObjectMeta: metav1.ObjectMeta{Name: addonConfig.Name, Namespace: "default"}, Type: v1.SecretTypeOpaque}
		mutateFn := func() error {
			secret.StringData = make(map[string]string)
			secret.StringData["values.yaml"] = string(b)
			return nil
		}

		if _, err := controllerutil.CreateOrPatch(ctx, r.Client, secret, mutateFn); err != nil {
			return ctrl.Result{}, errors.Wrap(err, "unable to create or patch addonConfigConfig data values secret")
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

func (r *AddonConfigReconciler) gvrForGroupKind(gk schema.GroupKind) (*schema.GroupVersionResource, error) {
	apiResourceList, err := r.CachedDiscoveryClient.ServerPreferredResources()

	if err != nil {
		return nil, err
	}
	for _, apiResource := range apiResourceList {
		gv, err := schema.ParseGroupVersion(apiResource.GroupVersion)
		if err != nil {
			return nil, err
		}
		if gv.Group == gk.Group {
			for i := 0; i < len(apiResource.APIResources); i++ {
				if apiResource.APIResources[i].Kind == gk.Kind {
					return &schema.GroupVersionResource{Group: gv.Group, Resource: apiResource.APIResources[i].Name, Version: gv.Version}, nil
				}
			}
		}
	}
	return nil, fmt.Errorf("unable to find server preferred resource %s/%s", gk.Group, gk.Kind)
}

func (r *AddonConfigReconciler) getResource(ctx context.Context, groupKind schema.GroupKind, resourceName, resourceNamespace string) (*unstructured.Unstructured, error) {
	gvr, err := r.gvrForGroupKind(groupKind)
	if err != nil {
		return nil, err
	}

	provider, err := r.DynamicClient.Resource(*gvr).Namespace(resourceNamespace).Get(ctx, resourceName, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}

	return provider, nil
}

func (r *AddonConfigReconciler) getCAPIObjectsTree(ctx context.Context, cluster *clusterapiv1beta1.Cluster) (*ClusterResources, error) {
	var (
		err      error
		provider *unstructured.Unstructured
	)

	if cluster.Spec.InfrastructureRef == nil {
		return nil, fmt.Errorf("cluster %s 's infrastructure reference is not set yet", cluster.Name)
	}
	gr := schema.GroupKind{Group: cluster.Spec.InfrastructureRef.GroupVersionKind().Group, Kind: cluster.Spec.InfrastructureRef.GroupVersionKind().Kind}
	if provider, err = r.getResource(ctx, gr, cluster.Spec.InfrastructureRef.Name, cluster.Spec.InfrastructureRef.Namespace); err != nil {
		return nil, err
	}

	if cluster.Spec.ControlPlaneRef == nil {
		return nil, fmt.Errorf("cluster %s 's controlplane reference is not set yet", cluster.Name)
	}
	kcp := &controlplanev1beta1.KubeadmControlPlane{}
	if err = r.Client.Get(ctx, client.ObjectKey{Namespace: cluster.Spec.ControlPlaneRef.Namespace, Name: cluster.Spec.ControlPlaneRef.Name}, kcp); err != nil {
		return nil, err
	}

	var machineDeployments clusterapiv1beta1.MachineDeploymentList
	listOptions := []client.ListOption{
		client.InNamespace(cluster.Namespace),
		client.MatchingLabels(map[string]string{clusterapiv1beta1.ClusterNameLabel: cluster.Name}),
	}
	if err = r.Client.List(ctx, &machineDeployments, listOptions...); err != nil {
		return nil, err
	}

	var cfgTemplates []*unstructured.Unstructured
	for _, m := range machineDeployments.Items {
		obj, err := external.Get(ctx, r.Client, m.Spec.Template.Spec.Bootstrap.ConfigRef, cluster.Namespace)
		if err != nil {
			return nil, err
		}
		cfgTemplates = append(cfgTemplates, obj)
	}

	var machineTemplates []*unstructured.Unstructured
	for _, m := range machineDeployments.Items {
		obj, err := external.Get(ctx, r.Client, &m.Spec.Template.Spec.InfrastructureRef, cluster.Namespace)
		if err != nil {
			return nil, err
		}
		machineTemplates = append(machineTemplates, obj)
	}

	return &ClusterResources{
		Cluster:               cluster,
		InfrastructureCluster: provider,
		MachineDeployments:    &machineDeployments,
		KubeadmControlPlane:   kcp,
		CfgTemplates:          cfgTemplates,
		MachineTemplates:      machineTemplates,
		ClusterGroupVersion:   cluster.GroupVersionKind().GroupVersion().String(),
		ClusterKind:           cluster.GroupVersionKind().Kind,
	}, err
}

func (r *AddonConfigReconciler) getDependencyObjects(ctx context.Context, addonConfigDefinition *addonv1.AddonConfigDefinition) ([]*unstructured.Unstructured, error) {
	var (
		dependencyObjects []*unstructured.Unstructured
		objFound          bool
		err               error
	)

	dependencies := r.generateDependenciesFromTemplate(addonConfigDefinition)
	for _, m := range dependencies {
		objRef := &v1.ObjectReference{Kind: m.Kind, Namespace: m.Namespace, Name: m.Name, APIVersion: m.ApiVersion}
		obj, err := external.Get(ctx, r.Client, objRef, m.Namespace)
		if err != nil {
			return nil, fmt.Errorf("unable to find object %s/%s", m.Namespace, m.Name)
		}

		if val, ok := obj.GetLabels()[m.LabelSelector.Key]; ok {
			if val == m.LabelSelector.Value {
				objFound = true
			}
		}
		if m.LabelSelector.Key == "" || objFound {
			dependencyObjects = append(dependencyObjects, obj)
		}
	}

	return dependencyObjects, err
}

func (r *AddonConfigReconciler) generateDependenciesFromTemplate(addonConfigDefinition *addonv1.AddonConfigDefinition) []dependencySpec {
	var dependencies []dependencySpec

	for _, dependency := range addonConfigDefinition.Spec.Dependencies {
		dependencies = append(dependencies,
			dependencySpec{
				ApiVersion: dependency.ApiVersion,
				Kind:       dependency.Kind,
				Name:       dependency.Name,
				Namespace:  dependency.Namespace,
				LabelSelector: labelSelectorKeyVal{
					Key:   dependency.LabelSelector.Key,
					Value: dependency.LabelSelector.Value,
				},
			},
		)
	}

	return dependencies
}
