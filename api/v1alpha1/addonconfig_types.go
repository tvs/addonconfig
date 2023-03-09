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
)

// AddonConfigSpec defines the desired state of AddonConfig
type AddonConfigSpec struct {
	// type is the name of the AddonConfigDefinition that this AddonConfig is validated against
	Type string `json:"type,omitempty" protobuf:"bytes,1,opt,name=type"`

	// Values describes the fields to be validated and marshalled with the AddonConfigDefinition defined in type
	// +optional
	Values apiextensionsv1.JSON `json:"values,omitempty" protobuf:"bytes,2,opt,name=values"`
}

// AddonConfigStatus defines the observed state of AddonConfig
type AddonConfigStatus struct {
	// Conditions define the current state of the AddonConfig
	// +optional
	Conditions Conditions `json:"conditions,omitempty" protobuf:"bytes,1,opt,name=conditions"`

	// FieldErrors define any existing schema validation errors in the AddonConfig
	// +optional
	FieldErrors FieldErrors `json:"fieldErrors,omitempty" protobuf:"bytes,2,opt,name=fieldErrors"`

	// ObservedGeneration is the latest generation observed by the controller.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty" protobuf:"bytes,3,opt,name=observedGeneration"`

	// ObservedSchemaGeneration is the latest generation of the schema observed
	// by the controller
	// +optional
	ObservedSchemaGeneration int64 `json:"observedSchemaGeneration,omitempty" protobuf:"bytes,4,opt,name=observedSchemaGeneration"`
}

const (
	// ReadyCondition defines the Ready condition type that summarizes the
	// operational state of an Addon object
	ReadyCondition ConditionType = "Ready"

	// ValidSchemaCondition documents whether a schema exists and is valid. Must
	// be true before validating the AddonConfig against it
	ValidSchemaCondition ConditionType = "ValidSchema"

	// ValidConfigCondition documents whether the AddonConfig could be validated
	// against the schema
	ValidConfigCondition ConditionType = "ValidConfig"

	// DefaultingCompleteCondition documents whether the AddonConfig has had
	// missing defaulted values make explicit
	DefaultingCompleteCondition ConditionType = "DefaultingComplete"
)

const (
	SchemaNotFound                 string = "SchemaNotFound"
	SchemaNotFoundMessage          string = "Unable to find schema by name %q"
	InvalidConfig                  string = "InvalidConfiguration"
	InvalidConfigMessage           string = "Invalid configuration; see .status.fieldErrors for more information"
	DefaultingInternalError        string = "DefaultingInternalError"
	DefaultingInternalErrorMessage string = "Unable to render defaults due to an internal error"
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
