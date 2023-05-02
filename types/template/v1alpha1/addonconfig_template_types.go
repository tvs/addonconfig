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

// +kubebuilder:skip

package internal

import (
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
)

// AddonConfigTemplateVariables defines the structure used when rendering
// an AddonConfigDefinition's template.
type AddonConfigTemplateVariables struct {
	// Default defines the default variables that will be made available for
	// templating. These variables must be available for dependency resolution
	// (e.g., {{.Default.Cluster.Metadata.Name}} must be available for templating against
	// dependency names)
	Default AddonConfigTemplateDefaults `json:"default" protobuf:"bytes,1,opt,name=default"`

	// Dependencies defines the set of resolved dependencies from the
	// AddonConfigDefinition
	Dependencies map[string]interface{} `json:"dependencies" protobuf:"bytes,2,opt,name=dependencies"`

	// Values defines the top-level interface for variables provided by the
	// spec of the AddonConfig
	Values interface{} `json:"values" protobuf:"bytes,3,opt,name=values"`
}

// AddonConfigTemplateDefaults are values that will be guaranteed for
// templating. These variables must be available for dependency resolution.
type AddonConfigTemplateDefaults struct {
	// Cluster is CAPI cluster that is targeted during template rendering.
	// TODO(tvs): Can we dodge the CAPI import by using unstructured data
	// instead?
	Cluster clusterv1.Cluster `json:"cluster" protobuf:"bytes,1,opt,name=cluster"`
}
