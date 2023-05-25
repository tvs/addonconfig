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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ClusterTarget defines a Target with the type explicitly scoped to a
// Cluster API Cluster resource.
// TODO(tvs): How do we handle retrieving the right credentials for a package
// install?  Is that our responsibility?
type ClusterTarget struct {
	// Explicit name of the Cluster that this addon config is targeting.
	// Mutually exclusive with the selector.
	Name string `json:"name" protobuf:"bytes,1,opt,name=name"`
}

// Target defines a form of object reference resolved either with explicit
// naming or by use of a selector.
type Target struct {
	// API version of the target referent.
	APIVersion string `json:"apiVersion" protobuf:"bytes,1,opt,name=apiVersion"`

	// Kind of the target referent.
	Kind string `json:"kind" protobuf:"bytes,2,opt,name=kind"`

	// Name of the target referent.
	// Mutually exclusive with the selector.
	// +optional
	Name string `json:"name,omitempty" protobuf:"bytes,3,opt,name=name"`

	// Label selector used to identify the referent. Must only identify one
	// resource.
	// Mutually exclusive with the explicit name.
	// +optional
	Selector *metav1.LabelSelector `json:"selector" protobuf:"bytes,4,opt,name=selector"`
}

// OutputTarget defines a target resource version, kind and name to use when
// writing a resource.
type OutputTarget struct {
	// API version of the target referent.
	APIVersion string `json:"apiVersion" protobuf:"bytes,1,opt,name=apiVersion"`

	// Kind of the target referent.
	Kind string `json:"kind" protobuf:"bytes,2,opt,name=kind"`

	// Name of the target referent.
	// Mutually exclusive with the selector.
	Name string `json:"name,omitempty" protobuf:"bytes,3,opt,name=name"`
}
