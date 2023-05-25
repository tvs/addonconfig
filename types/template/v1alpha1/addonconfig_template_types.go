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
	"k8s.io/apimachinery/pkg/runtime/schema"
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
	// AddonConfigDefinition using the dependency's name as a key.
	Dependencies map[string]interface{} `json:"dependencies" protobuf:"bytes,2,opt,name=dependencies"`

	// Values defines the top-level interface for variables provided by the
	// spec of the AddonConfig.
	Values map[string]interface{} `json:"values" protobuf:"bytes,3,opt,name=values"`
}

var (
	InfrastructureRefVSphere    = schema.GroupKind{Group: "infrastructure.cluster.x-k8s.io", Kind: "VSphereCluster"}
	InfrastructureRefSupervisor = schema.GroupKind{Group: "vmware.infrastructure.cluster.x-k8s.io", Kind: "VSphereCluster"}
	InfrastructureRefAWS        = schema.GroupKind{Group: "infrastructure.cluster.x-k8s.io", Kind: "AWSCluster"}
	InfrastructureRefAzure      = schema.GroupKind{Group: "infrastructure.cluster.x-k8s.io", Kind: "AzureCluster"}
	InfrastructureRefDocker     = schema.GroupKind{Group: "infrastructure.cluster.x-k8s.io", Kind: "DockerCluster"}
	InfrastructureRefOCI        = schema.GroupKind{Group: "infrastructure.cluster.x-k8s.io", Kind: "OCICluster"}
)

// InfrastructureProviderType is a supported form of infrastructure provider
type InfrastructureProvider string

const (
	InfrastructureProviderVSphere = InfrastructureProvider("vsphere")
	// TODO(tvs): Supervisor should probably be its own distinct type...
	InfrastructureProviderSupervisor = InfrastructureProvider("vsphere")
	InfrastructureProviderAWS        = InfrastructureProvider("aws")
	InfrastructureProviderAzure      = InfrastructureProvider("azure")
	InfrastructureProviderDocker     = InfrastructureProvider("docker")
	InfrastructureProviderOCI        = InfrastructureProvider("oci")
	InfrastructureProviderUnknown    = InfrastructureProvider("unknown")
)

// AddonConfigTemplateDefaults are values that will be guaranteed for
// templating. These variables must be available for dependency resolution.
type AddonConfigTemplateDefaults struct {
	// Cluster is a CAPI cluster that is targeted during template rendering.
	// TODO(tvs): Can we dodge the CAPI import by using unstructured data
	// instead?
	Cluster map[string]interface{} `json:"cluster" protobuf:"bytes,1,opt,name=cluster"`

	// Infrastructure is a simplified infrastructure name representing the type
	// of InfrastructureRef provided in the Cluster
	Infrastructure InfrastructureProvider `json:"infrastructure" protobuf:"bytes,2,opt,name=infrastructure"`
}
