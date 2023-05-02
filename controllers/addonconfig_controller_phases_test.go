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
	"testing"

	. "github.com/onsi/gomega"
	"k8s.io/apiextensions-apiserver/pkg/apis/apiextensions"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	addonv1 "github.com/tvs/addonconfig/api/v1alpha1"
)

// TODO(tvs): Expand on phase testing
func TestAddonConfigReconcilePhases_validation(t *testing.T) {
	ac := &addonv1.AddonConfig{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-addonconfig",
			Namespace: "test-namespace",
		},
		Spec: addonv1.AddonConfigSpec{
			Type: "test-addonconfig-definition",
			Values: apiextensionsv1.JSON{
				Raw: []byte(`{"field": "foo"}`),
			},
		},
	}

	acd := &addonv1.AddonConfigDefinition{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-addonconfig-definition",
		},
		Spec: addonv1.AddonConfigDefinitionSpec{
			Schema: &apiextensionsv1.CustomResourceValidation{
				OpenAPIV3Schema: &apiextensionsv1.JSONSchemaProps{
					Type: "object",
					Properties: map[string]apiextensionsv1.JSONSchemaProps{
						"field": {
							Type: "string",
						},
						"defaultedField": {
							Type: "string",
							Default: &apiextensionsv1.JSON{
								Raw: []byte(`"default"`),
							},
						},
					},
				},
			},
		},
	}

	t.Run("reconcile schema", func(t *testing.T) {

		tests := []struct {
			name         string
			ac           *addonv1.AddonConfig
			acd          *addonv1.AddonConfigDefinition
			expectErr    bool
			expectResult ctrl.Result
		}{
			{
				name: "returns an error when the AddonConfigDefinition has no schema",
				// NOTE: we copy these because phase reconciliation often changes conditions
				ac: ac.DeepCopy(),
				acd: &addonv1.AddonConfigDefinition{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-addonconfig-definition-empty",
					},
				},
				expectErr: true,
			},
			{
				name:      "returns no error if the AddonConfigDefinition schema is convertible",
				ac:        ac.DeepCopy(),
				acd:       acd,
				expectErr: false,
			},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				g := NewWithT(t)

				c := fake.NewClientBuilder().
					Build()

				r := &AddonConfigReconciler{
					Client: c,
				}

				crv := &apiextensions.CustomResourceValidation{}
				res, err := r.reconcileSchema(ctx, tt.ac, tt.acd, crv)
				g.Expect(res).To(Equal(tt.expectResult))

				if tt.expectErr {
					g.Expect(err).To(HaveOccurred())
				} else {
					g.Expect(err).NotTo(HaveOccurred())
				}
			})
		}
	})

	t.Run("reconcile schema validation", func(t *testing.T) {
	})
}

func TestAddonConfigReconcilePhases(t *testing.T) {
}
