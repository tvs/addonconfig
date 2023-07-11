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

package v1alpha1

import (
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	apitypes "k8s.io/apimachinery/pkg/types"
)

// AddonConfigSpec defines the desired state of AddonConfig
type AddonConfigSpec struct {
	// DefinitionRef is the name of the AddonConfigDefinition that this
	// AddonConfig is validated against
	DefinitionRef string `json:"definitionRef,omitempty" protobuf:"bytes,1,opt,name=definitionRef"`

	// Target defines the CAPI cluster target for this instance of the
	// AddonConfig.
	Target ClusterTarget `json:"target" protobuf:"bytes:2,opt,name=target"`

	// Values describes the fields to be validated and marshalled with the
	// AddonConfigDefinition defined in type
	// +optional
	Values apiextensionsv1.JSON `json:"values,omitempty" protobuf:"bytes,3,opt,name=values"`
}

// AddonConfigStatus defines the observed state of AddonConfig
type AddonConfigStatus struct {
	// Conditions define the current state of the AddonConfig
	// +optional
	Conditions Conditions `json:"conditions,omitempty" protobuf:"bytes,1,opt,name=conditions"`

	// FieldErrors define any existing schema validation errors in the
	// AddonConfig
	// +optional
	FieldErrors FieldErrors `json:"fieldErrors,omitempty" protobuf:"bytes,2,opt,name=fieldErrors"`

	// ObservedGeneration is the latest generation observed by the controller.
	// +optional
	ObservedGeneration *int64 `json:"observedGeneration,omitempty" protobuf:"bytes,3,opt,name=observedGeneration"`

	// ObservedSchemaUID is the latest UID of the schema observed by the
	// TODO(tvs): Add the UID of the latest resolved AddonConfigDefinition to
	// ensure we've reconciled the correct AddonConfigDefinition after a recreate
	// controller +optional
	ObservedSchemaUID *apitypes.UID `json:"observedSchemaUID,omitempty" protobuf:"bytes,5,opt,name=observedSchemaUID"`

	// OutputRef describes where the rendered template is persisted.
	OutputRef *OutputResource `json:"outputRef,omitempty" protobuf:"bytes,6,opt,name=outputRef"`
}

const (
	// ReadyCondition defines the Ready condition type that summarizes the
	// operational state of an Addon object
	ReadyCondition ConditionType = "Ready"

	// ValidSchemaCondition documents whether a schema exists and is valid. Must
	// be true before validating the AddonConfig against it
	// TODO(tvs): Move this validation to an ACD controller.
	ValidSchemaCondition ConditionType = "ValidSchema"

	// ValidConfigCondition documents whether the AddonConfig could be validated
	// against the schema
	ValidConfigCondition ConditionType = "ValidConfig"

	// DefaultingCompleteCondition documents whether the AddonConfig has had
	// missing defaulted values make explicit
	DefaultingCompleteCondition ConditionType = "DefaultingComplete"

	// ValidTargetCondition documents whether the AddonConfig has a valid target
	// cluster
	ValidTargetCondition ConditionType = "ValidTarget"

	// ValidTemplateCondition documents whether the AddonConfigDefinition has a
	// valid template for go templating.
	// TODO(tvs): Move this validation in to an ACD controller
	ValidTemplateCondition ConditionType = "ValidTemplate"

	// ValidDependencyCondition documents whether the AddonConfigDefinition has a valid dependency
	ValidDependencyCondition ConditionType = "ValidDependency"

	// OutputResourceUpdatedCondition documents whether the rendered tempalte has been persisted into the output resource
	OutputResourceUpdatedCondition ConditionType = "OutputResourceUpdated"
)

const (
	SchemaNotFound                                string = "SchemaNotFound"
	SchemaNotFoundMessage                         string = "Unable to find AddonConfigDefinition by name %q"
	InvalidSchema                                 string = "InvalidSchema"
	InvalidSchemaMessage                          string = "Schema is invalid"
	SchemaUndefinedMessage                        string = "Schema is undefined"
	InvalidConfig                                 string = "InvalidConfiguration"
	InvalidConfigMessage                          string = "Invalid configuration; see .status.fieldErrors for more information"
	DefaultingInternalError                       string = "DefaultingInternalError"
	DefaultingInternalErrorMessage                string = "Unable to render defaults due to an internal error"
	TargetUndefined                               string = "TargetUndefined"
	TargetUndefinedMessage                        string = "No target name has been defined"
	TargetNotFound                                string = "TargetNotFound"
	TargetNotFoundMessage                         string = "No target has been found"
	InvalidTemplate                               string = "InvalidTemplate"
	FailedRendering                               string = "FailedRendering"
	FailedWritingRenderedTemplate                 string = "FailedWritingRenderedTemplate"
	DependencyNotFound                            string = "DependencyNotFound"
	DependencyNotFoundByNameMessage               string = "Unable to find dependency by name %q"
	DependencyNotFoundBySelectorMessage           string = "Unable to find dependency by using label selectors"
	DependencyUniquenessNotSatisfied              string = "DependencyUniquenessNotSatisfied"
	DependencyUniquenessNotSatisfiedMessage       string = "Multiple dependencies found while expecting a single one"
	DependencyTemplatingFailed                    string = "DependencyTemplatingFailed"
	DependencyNameTemplatingFailedMessage         string = "Failed to template dependency name: %q"
	DependencySelectorTemplatingFailedMessage     string = "Failed to template dependency label selector"
	DependencyConstraintFailed                    string = "DependencyConstraintFailed"
	DependencyIncorrectSetOfDependenciesMessage   string = "incorrect set of dependency constraints"
	OutputResourceUndefined                       string = "OutputResourceUndefined"
	OutputResourceUndefinedMessage                string = "No output resource has been defined"
	MultipleOutputResourceDefined                 string = "MultipleOutputResourceDefined"
	MultipleOutputResourceDefinedMessage          string = "Multiple output resources has been defined"
	RemoteOutputResourceNoNamespaceDefined        string = "RemoteOutputResourceNoNamespaceDefined"
	RemoteOutputResourceNoNamespaceDefinedMessage string = "No namespace defined for the remote output resource"

	NameTemplatingFailed                   string = "NameTemplatingFailed"
	UpdateFailed                           string = "UpdateFailed"
	UnableToPersistRenderedTemplateMessage string = "Unable to persist the rendered template into resource %q"

	// TODO(tvs): More detailed error messages for why they're invalid
	TemplateParseErrorMessage                  string = "Unable to parse the template"
	TemplateDefinesNestedTemplatesErrorMessage string = "Unable to define nested templates in the template"
	TemplateWriteErrorMessage                  string = "Unable to write the rendered template into bytes"
)

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status

// AddonConfig is the Schema for the addonconfigs API
type AddonConfig struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   AddonConfigSpec   `json:"spec,omitempty"`
	Status AddonConfigStatus `json:"status,omitempty"`
}

func (a *AddonConfig) SetOutputRef(localOutput *LocalOutput, remoteOutput *RemoteOutput) {
	a.Status.OutputRef = &OutputResource{
		LocalOutput:  localOutput,
		RemoteOutput: remoteOutput,
	}
}

func (a *AddonConfig) GetConditions() Conditions {
	return a.Status.Conditions
}

func (a *AddonConfig) SetConditions(conditions Conditions) {
	a.Status.Conditions = conditions
}

//+kubebuilder:object:root=true

// AddonConfigList contains a list of AddonConfig
type AddonConfigList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []AddonConfig `json:"items"`
}

func init() {
	SchemeBuilder.Register(&AddonConfig{}, &AddonConfigList{})
}
