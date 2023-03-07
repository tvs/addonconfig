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
	apiextensions "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// AddonConfigDefinitionSpec defines the desired state of AddonConfigDefinition
type AddonConfigDefinitionSpec struct {
	// schema describes the schema used for validation, pruning, and defaulting of this version of the custom resource.
	// +optional
	// +kubebuilder:pruning:PreserveUnknownFields
	// +kubebuilder:validation:Type="object"
	// +kubebuilder:validation:Schemaless
	Schema *apiextensions.CustomResourceValidation `json:"schema,omitempty" protobuf:"bytes,1,opt,name=schema"`

	// template describes the template used when marshalling the schema into an add-on usable format
	Template string `json:"template" protobuf:"bytes,2,opt,name=template"`
}

// AddonConfigDefinitionStatus defines the observed state of AddonConfigDefinition
type AddonConfigDefinitionStatus struct {
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "make" to regenerate code after modifying this file
}

//+kubebuilder:object:root=true
//+kubebuilder:resource:scope=Cluster
//+kubebuilder:subresource:status

// AddonConfigDefinition is the Schema for the addonconfigdefinitions API
type AddonConfigDefinition struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   AddonConfigDefinitionSpec   `json:"spec,omitempty"`
	Status AddonConfigDefinitionStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// AddonConfigDefinitionList contains a list of AddonConfigDefinition
type AddonConfigDefinitionList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []AddonConfigDefinition `json:"items"`
}

func init() {
	SchemeBuilder.Register(&AddonConfigDefinition{}, &AddonConfigDefinitionList{})
}
