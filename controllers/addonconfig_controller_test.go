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
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/json"

	addonv1 "github.com/tvs/addonconfig/api/v1alpha1"
	"github.com/tvs/addonconfig/util/conditions"
)

var _ = Describe("AddonConfig controller", func() {

	const (
		AddonConfigName      = "test-addonconfig"
		AddonConfigNamespace = "test-addon-reconcile"

		AddonConfigDefinitionName = "test-addonconfigdefinition"

		timeout     = time.Second * 10
		duration    = time.Second * 10
		repetitions = 10
		interval    = time.Millisecond * 250
	)

	BeforeEach(func() {
		ns := &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: AddonConfigNamespace,
			},
		}

		err := k8sClient.Create(ctx, ns)
		if err != nil {
			// We don't care if the namespace already exists
			Expect(apierrors.IsAlreadyExists(err)).To(BeTrue())
		}
	})

	// Do all the setup ahead of each container just so we're in good shape
	var (
		ac          *addonv1.AddonConfig
		acLookupKey types.NamespacedName
		createdAc   *addonv1.AddonConfig
	)

	getConditionStatus := func(t addonv1.ConditionType) (corev1.ConditionStatus, error) {
		if err := k8sClient.Get(ctx, acLookupKey, createdAc); err != nil {
			return corev1.ConditionUnknown, err
		}

		c := conditions.Get(createdAc, t)
		if c == nil {
			return corev1.ConditionUnknown, nil
		}

		return c.Status, nil
	}

	BeforeEach(func() {
		ac = &addonv1.AddonConfig{
			ObjectMeta: metav1.ObjectMeta{
				Name:      AddonConfigName,
				Namespace: AddonConfigNamespace,
			},
			Spec: addonv1.AddonConfigSpec{
				DefinitionRef: AddonConfigDefinitionName,
				Values: apiextensionsv1.JSON{
					Raw: []byte(`{"field": "foo"}`),
				},
			},
		}

		Expect(k8sClient.Create(ctx, ac)).Should(Succeed())

		acLookupKey = types.NamespacedName{Name: AddonConfigName, Namespace: AddonConfigNamespace}
		createdAc = &addonv1.AddonConfig{}

		Eventually(func() bool {
			err := k8sClient.Get(ctx, acLookupKey, createdAc)
			if err != nil {
				return false
			}
			return true
		}, timeout, interval).Should(BeTrue())
	})

	AfterEach(func() {
		if ac != nil {
			Expect(k8sClient.Delete(ctx, ac)).Should(Succeed())
		}
	})

	Context("When AddonConfig has an existent .spec.type", func() {
		var acd *addonv1.AddonConfigDefinition

		Context("When the AddonConfigDefinition has no schema", func() {
			BeforeEach(func() {
				acd = &addonv1.AddonConfigDefinition{
					ObjectMeta: metav1.ObjectMeta{
						Name: AddonConfigDefinitionName,
					},
				}

				Expect(k8sClient.Create(ctx, acd)).Should(Succeed())
			})

			It("Should report an an invalid schema condition", func() {
				Eventually(getConditionStatus, duration, interval).
					MustPassRepeatedly(repetitions).
					WithArguments(addonv1.ValidSchemaCondition).
					Should(Equal(corev1.ConditionFalse))
			})

			It("Should report a false ready condition", func() {
				Eventually(getConditionStatus, duration, interval).
					MustPassRepeatedly(repetitions).
					WithArguments(addonv1.ReadyCondition).
					Should(Equal(corev1.ConditionFalse))
			})
		})

		Context("When the AddonConfigDefinition has a schema", func() {
			JustBeforeEach(func() {
				Expect(k8sClient.Create(ctx, acd)).Should(Succeed())
			})

			Context("and the schema has defaults missing in the AddonConfig", func() {
				BeforeEach(func() {
					acd = &addonv1.AddonConfigDefinition{
						ObjectMeta: metav1.ObjectMeta{
							Name: AddonConfigDefinitionName,
						},
						// TODO(tvs): Add a schema with default values
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
				})

				It("Should fill out missing default values", func() {
					By("Reporting a defaulting complete condition")
					Eventually(getConditionStatus, duration, interval).
						MustPassRepeatedly(repetitions).
						WithArguments(addonv1.DefaultingCompleteCondition).
						Should(Equal(corev1.ConditionTrue))

					By("Having default values explicitly filled in")
					defaultedJSON := make(map[string]interface{})
					Expect(json.Unmarshal(createdAc.Spec.Values.Raw, &defaultedJSON)).Should(Succeed())
					Expect(defaultedJSON["defaultedField"]).To(BeEquivalentTo("default"))
				})
			})

		})

		AfterEach(func() {
			if acd != nil {
				Expect(k8sClient.Delete(ctx, acd)).Should(Succeed())
			}
		})
	})

})
