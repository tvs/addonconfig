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

// AddonConfigSpec defines the desired state of AddonConfig
type AddonConfigSpec struct {
	// type is the name of the AddonConfigDefinition that this AddonConfig is validated against
	Type string `json:"type,omitempty" protobuf:"bytes,1,opt,name=type"`

	// Values describes the fields to be validated and marshalled with the AddonConfigDefinition defined in type
	// +optional
	Values apiextensions.JSON `json:"values,omitempty" protobuf:"bytes,2,opt,name=values"`
}

// AddonConfigStatus defines the observed state of AddonConfig
type AddonConfigStatus struct {
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "make" to regenerate code after modifying this file
}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status

// AddonConfig is the Schema for the addonconfigs API
type AddonConfig struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   AddonConfigSpec   `json:"spec,omitempty"`
	Status AddonConfigStatus `json:"status,omitempty"`
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
