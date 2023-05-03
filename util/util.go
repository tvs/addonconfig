/*
Copyright 2017 The Kubernetes Authors.

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

// Package util implements utilities.
package util

// TODO(tvs): Stolen from CAPI for other utils; should get more utils into
// controller-runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"math/rand"
	"reflect"
	"strings"
	"time"

	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	k8sversion "k8s.io/apimachinery/pkg/version"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	addonv1 "github.com/tvs/addonconfig/api/v1alpha1"
	templatev1 "github.com/tvs/addonconfig/types/template/v1alpha1"
)

const (
	// CharSet defines the alphanumeric set for random string generation.
	CharSet = "0123456789abcdefghijklmnopqrstuvwxyz"
)

var (
	rnd = rand.New(rand.NewSource(time.Now().UnixNano())) //nolint:gosec

	// ErrUnstructuredFieldNotFound determines that a field
	// in an unstructured object could not be found.
	ErrUnstructuredFieldNotFound = fmt.Errorf("field not found")
)

// RandomString returns a random alphanumeric string.
func RandomString(n int) string {
	result := make([]byte, n)
	for i := range result {
		result[i] = CharSet[rnd.Intn(len(CharSet))]
	}
	return string(result)
}

// Ordinalize takes an int and returns the ordinalized version of it.
// Eg. 1 --> 1st, 103 --> 103rd.
func Ordinalize(n int) string {
	m := map[int]string{
		0: "th",
		1: "st",
		2: "nd",
		3: "rd",
		4: "th",
		5: "th",
		6: "th",
		7: "th",
		8: "th",
		9: "th",
	}

	an := int(math.Abs(float64(n)))
	if an < 10 {
		return fmt.Sprintf("%d%s", n, m[an])
	}
	return fmt.Sprintf("%d%s", n, m[an%10])
}

func GetAddonConfigsByType(ctx context.Context, c client.Client, name string) ([]addonv1.AddonConfig, error) {
	// TODO(tvs): Leverage field selectors once they're able to be added to CRDs
	// so that the server does this list filtering for us
	fullList := &addonv1.AddonConfigList{}
	if err := c.List(ctx, fullList); err != nil {
		return nil, errors.Wrapf(err, "failed to list AddonConfigs")
	}

	list := make([]addonv1.AddonConfig, 0, len(fullList.Items))
	for _, a := range fullList.Items {
		if a.Spec.Type == name {
			list = append(list, a)
		}
	}

	return list, nil
}

// ObjectKey returns client.ObjectKey for the object.
func ObjectKey(object metav1.Object) client.ObjectKey {
	return client.ObjectKey{
		Namespace: object.GetNamespace(),
		Name:      object.GetName(),
	}
}

// HasOwnerRef returns true if the OwnerReference is already in the slice.
func HasOwnerRef(ownerReferences []metav1.OwnerReference, ref metav1.OwnerReference) bool {
	return indexOwnerRef(ownerReferences, ref) > -1
}

// EnsureOwnerRef makes sure the slice contains the OwnerReference.
func EnsureOwnerRef(ownerReferences []metav1.OwnerReference, ref metav1.OwnerReference) []metav1.OwnerReference {
	idx := indexOwnerRef(ownerReferences, ref)
	if idx == -1 {
		return append(ownerReferences, ref)
	}
	ownerReferences[idx] = ref
	return ownerReferences
}

// ReplaceOwnerRef re-parents an object from one OwnerReference to another
// It compares strictly based on UID to avoid reparenting across an intentional deletion: if an object is deleted
// and re-created with the same name and namespace, the only way to tell there was an in-progress deletion
// is by comparing the UIDs.
func ReplaceOwnerRef(ownerReferences []metav1.OwnerReference, source metav1.Object, target metav1.OwnerReference) []metav1.OwnerReference {
	fi := -1
	for index, r := range ownerReferences {
		if r.UID == source.GetUID() {
			fi = index
			ownerReferences[index] = target
			break
		}
	}
	if fi < 0 {
		ownerReferences = append(ownerReferences, target)
	}
	return ownerReferences
}

// RemoveOwnerRef returns the slice of owner references after removing the supplied owner ref.
func RemoveOwnerRef(ownerReferences []metav1.OwnerReference, inputRef metav1.OwnerReference) []metav1.OwnerReference {
	if index := indexOwnerRef(ownerReferences, inputRef); index != -1 {
		return append(ownerReferences[:index], ownerReferences[index+1:]...)
	}
	return ownerReferences
}

// indexOwnerRef returns the index of the owner reference in the slice if found, or -1.
func indexOwnerRef(ownerReferences []metav1.OwnerReference, ref metav1.OwnerReference) int {
	for index, r := range ownerReferences {
		if referSameObject(r, ref) {
			return index
		}
	}
	return -1
}

// IsOwnedByObject returns true if any of the owner references point to the given target.
func IsOwnedByObject(obj metav1.Object, target client.Object) bool {
	for _, ref := range obj.GetOwnerReferences() {
		ref := ref
		if refersTo(&ref, target) {
			return true
		}
	}
	return false
}

// IsControlledBy differs from metav1.IsControlledBy in that it checks the group (but not version), kind, and name vs uid.
func IsControlledBy(obj metav1.Object, owner client.Object) bool {
	controllerRef := metav1.GetControllerOfNoCopy(obj)
	if controllerRef == nil {
		return false
	}
	return refersTo(controllerRef, owner)
}

// Returns true if a and b point to the same object.
func referSameObject(a, b metav1.OwnerReference) bool {
	aGV, err := schema.ParseGroupVersion(a.APIVersion)
	if err != nil {
		return false
	}

	bGV, err := schema.ParseGroupVersion(b.APIVersion)
	if err != nil {
		return false
	}

	return aGV.Group == bGV.Group && a.Kind == b.Kind && a.Name == b.Name
}

// Returns true if ref refers to obj.
func refersTo(ref *metav1.OwnerReference, obj client.Object) bool {
	refGv, err := schema.ParseGroupVersion(ref.APIVersion)
	if err != nil {
		return false
	}

	gvk := obj.GetObjectKind().GroupVersionKind()
	return refGv.Group == gvk.Group && ref.Kind == gvk.Kind && ref.Name == obj.GetName()
}

// UnstructuredUnmarshalField is a wrapper around json and unstructured objects to decode and copy a specific field
// value into an object.
func UnstructuredUnmarshalField(obj *unstructured.Unstructured, v interface{}, fields ...string) error {
	if obj == nil || obj.Object == nil {
		return errors.Errorf("failed to unmarshal unstructured object: object is nil")
	}

	value, found, err := unstructured.NestedFieldNoCopy(obj.Object, fields...)
	if err != nil {
		return errors.Wrapf(err, "failed to retrieve field %q from %q", strings.Join(fields, "."), obj.GroupVersionKind())
	}
	if !found || value == nil {
		return ErrUnstructuredFieldNotFound
	}
	valueBytes, err := json.Marshal(value)
	if err != nil {
		return errors.Wrapf(err, "failed to json-encode field %q value from %q", strings.Join(fields, "."), obj.GroupVersionKind())
	}
	if err := json.Unmarshal(valueBytes, v); err != nil {
		return errors.Wrapf(err, "failed to json-decode field %q value from %q", strings.Join(fields, "."), obj.GroupVersionKind())
	}
	return nil
}

// HasOwner checks if any of the references in the passed list match the given group from apiVersion and one of the given kinds.
func HasOwner(refList []metav1.OwnerReference, apiVersion string, kinds []string) bool {
	gv, err := schema.ParseGroupVersion(apiVersion)
	if err != nil {
		return false
	}

	kindMap := make(map[string]bool)
	for _, kind := range kinds {
		kindMap[kind] = true
	}

	for _, mr := range refList {
		mrGroupVersion, err := schema.ParseGroupVersion(mr.APIVersion)
		if err != nil {
			return false
		}

		if mrGroupVersion.Group == gv.Group && kindMap[mr.Kind] {
			return true
		}
	}

	return false
}

// KubeAwareAPIVersions is a sortable slice of kube-like version strings.
//
// Kube-like version strings are starting with a v, followed by a major version,
// optional "alpha" or "beta" strings followed by a minor version (e.g. v1, v2beta1).
// Versions will be sorted based on GA/alpha/beta first and then major and minor
// versions. e.g. v2, v1, v1beta2, v1beta1, v1alpha1.
type KubeAwareAPIVersions []string

func (k KubeAwareAPIVersions) Len() int      { return len(k) }
func (k KubeAwareAPIVersions) Swap(i, j int) { k[i], k[j] = k[j], k[i] }
func (k KubeAwareAPIVersions) Less(i, j int) bool {
	return k8sversion.CompareKubeAwareVersionStrings(k[i], k[j]) < 0
}

// isAPINamespaced detects if a GroupVersionKind is namespaced.
func isAPINamespaced(gk schema.GroupVersionKind, restmapper meta.RESTMapper) (bool, error) {
	restMapping, err := restmapper.RESTMapping(schema.GroupKind{Group: gk.Group, Kind: gk.Kind})
	if err != nil {
		return false, fmt.Errorf("failed to get restmapping: %w", err)
	}

	switch restMapping.Scope.Name() {
	case "":
		return false, errors.New("Scope cannot be identified. Empty scope returned")
	case meta.RESTScopeNameRoot:
		return false, nil
	default:
		return true, nil
	}
}

// ObjectReferenceToUnstructured converts an object reference to an unstructured object.
func ObjectReferenceToUnstructured(in corev1.ObjectReference) *unstructured.Unstructured {
	out := &unstructured.Unstructured{}
	out.SetKind(in.Kind)
	out.SetAPIVersion(in.APIVersion)
	out.SetNamespace(in.Namespace)
	out.SetName(in.Name)
	return out
}

// LowestNonZeroResult compares two reconciliation results
// and returns the one with lowest requeue time.
func LowestNonZeroResult(i, j ctrl.Result) ctrl.Result {
	switch {
	case i.IsZero():
		return j
	case j.IsZero():
		return i
	case i.Requeue:
		return i
	case j.Requeue:
		return j
	case i.RequeueAfter < j.RequeueAfter:
		return i
	default:
		return j
	}
}

// LowestNonZeroInt32 returns the lowest non-zero value of the two provided values.
func LowestNonZeroInt32(i, j int32) int32 {
	if i == 0 {
		return j
	}
	if j == 0 {
		return i
	}
	if i < j {
		return i
	}
	return j
}

// IsNil returns an error if the passed interface is equal to nil or if it has an interface value of nil.
func IsNil(i interface{}) bool {
	if i == nil {
		return true
	}
	switch reflect.TypeOf(i).Kind() {
	case reflect.Ptr, reflect.Map, reflect.Chan, reflect.Slice, reflect.Interface, reflect.UnsafePointer, reflect.Func:
		return reflect.ValueOf(i).IsValid() && reflect.ValueOf(i).IsNil()
	}
	return false
}

// GetInfrastructureProvider fetches the template InfrastructureProvider type
// from a given API version and kind
func GetInfrastructureProvider(apiVersion, kind string) templatev1.InfrastructureProvider {
	gvk := schema.FromAPIVersionAndKind(apiVersion, kind)

	switch gvk.GroupKind() {
	case templatev1.InfrastructureRefVSphere:
		return templatev1.InfrastructureProviderVSphere
	case templatev1.InfrastructureRefSupervisor:
		return templatev1.InfrastructureProviderSupervisor
	case templatev1.InfrastructureRefAWS:
		return templatev1.InfrastructureProviderAWS
	case templatev1.InfrastructureRefAzure:
		return templatev1.InfrastructureProviderAzure
	case templatev1.InfrastructureRefDocker:
		return templatev1.InfrastructureProviderDocker
	case templatev1.InfrastructureRefOCI:
		return templatev1.InfrastructureProviderOCI
	}

	return templatev1.InfrastructureProviderUnknown
}
