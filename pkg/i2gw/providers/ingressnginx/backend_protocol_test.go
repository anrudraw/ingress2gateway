/*
Copyright 2024 The Kubernetes Authors.

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

package ingressnginx

import (
	"testing"

	"github.com/kubernetes-sigs/ingress2gateway/pkg/i2gw/intermediate"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

func TestBackendProtocolFeature(t *testing.T) {
	testCases := []struct {
		name                    string
		ingresses               []networkingv1.Ingress
		expectedPolicies        int
		expectedPolicyName      string
		expectedHostname        string
		expectWellKnownCACerts  bool
	}{
		{
			name: "HTTPS backend creates BackendTLSPolicy",
			ingresses: []networkingv1.Ingress{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-ingress",
						Namespace: "default",
						Annotations: map[string]string{
							"nginx.ingress.kubernetes.io/backend-protocol": "HTTPS",
							"nginx.ingress.kubernetes.io/proxy-ssl-name":   "backend.internal",
						},
					},
					Spec: networkingv1.IngressSpec{
						Rules: []networkingv1.IngressRule{
							{
								Host: "example.com",
								IngressRuleValue: networkingv1.IngressRuleValue{
									HTTP: &networkingv1.HTTPIngressRuleValue{
										Paths: []networkingv1.HTTPIngressPath{
											{
												Path: "/",
												Backend: networkingv1.IngressBackend{
													Service: &networkingv1.IngressServiceBackend{
														Name: "my-service",
														Port: networkingv1.ServiceBackendPort{
															Number: 443,
														},
													},
												},
											},
										},
									},
								},
							},
						},
					},
				},
			},
			expectedPolicies:       1,
			expectedPolicyName:     "my-service-backend-tls",
			expectedHostname:       "backend.internal",
			expectWellKnownCACerts: true,
		},
		{
			name: "HTTPS backend with proxy-ssl-secret creates BackendTLSPolicy with CA ref",
			ingresses: []networkingv1.Ingress{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-ingress",
						Namespace: "default",
						Annotations: map[string]string{
							"nginx.ingress.kubernetes.io/backend-protocol":  "HTTPS",
							"nginx.ingress.kubernetes.io/proxy-ssl-secret":  "nginx/client-cert",
							"nginx.ingress.kubernetes.io/proxy-ssl-verify":  "on",
							"nginx.ingress.kubernetes.io/proxy-ssl-name":    "backend.internal",
						},
					},
					Spec: networkingv1.IngressSpec{
						Rules: []networkingv1.IngressRule{
							{
								Host: "example.com",
								IngressRuleValue: networkingv1.IngressRuleValue{
									HTTP: &networkingv1.HTTPIngressRuleValue{
										Paths: []networkingv1.HTTPIngressPath{
											{
												Path: "/",
												Backend: networkingv1.IngressBackend{
													Service: &networkingv1.IngressServiceBackend{
														Name: "my-service",
														Port: networkingv1.ServiceBackendPort{
															Number: 443,
														},
													},
												},
											},
										},
									},
								},
							},
						},
					},
				},
			},
			expectedPolicies:       1,
			expectedPolicyName:     "my-service-backend-tls",
			expectedHostname:       "backend.internal",
			expectWellKnownCACerts: false,
		},
		{
			name: "HTTP backend does not create BackendTLSPolicy",
			ingresses: []networkingv1.Ingress{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-ingress",
						Namespace: "default",
						Annotations: map[string]string{
							"nginx.ingress.kubernetes.io/backend-protocol": "HTTP",
						},
					},
					Spec: networkingv1.IngressSpec{
						Rules: []networkingv1.IngressRule{
							{
								Host: "example.com",
								IngressRuleValue: networkingv1.IngressRuleValue{
									HTTP: &networkingv1.HTTPIngressRuleValue{
										Paths: []networkingv1.HTTPIngressPath{
											{
												Path: "/",
												Backend: networkingv1.IngressBackend{
													Service: &networkingv1.IngressServiceBackend{
														Name: "my-service",
														Port: networkingv1.ServiceBackendPort{
															Number: 80,
														},
													},
												},
											},
										},
									},
								},
							},
						},
					},
				},
			},
			expectedPolicies: 0,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ir := intermediate.IR{
				BackendTLSPolicies: make(map[types.NamespacedName]gatewayv1.BackendTLSPolicy),
			}

			errs := backendProtocolFeature(tc.ingresses, nil, &ir)
			if len(errs) > 0 {
				t.Fatalf("unexpected errors: %v", errs)
			}

			if len(ir.BackendTLSPolicies) != tc.expectedPolicies {
				t.Errorf("expected %d BackendTLSPolicies, got %d", tc.expectedPolicies, len(ir.BackendTLSPolicies))
			}

			if tc.expectedPolicies > 0 {
				policyKey := types.NamespacedName{Namespace: "default", Name: tc.expectedPolicyName}
				policy, exists := ir.BackendTLSPolicies[policyKey]
				if !exists {
					t.Fatalf("expected BackendTLSPolicy %s not found", tc.expectedPolicyName)
				}

				if string(policy.Spec.Validation.Hostname) != tc.expectedHostname {
					t.Errorf("expected hostname %s, got %s", tc.expectedHostname, policy.Spec.Validation.Hostname)
				}

				if tc.expectWellKnownCACerts {
					if policy.Spec.Validation.WellKnownCACertificates == nil {
						t.Error("expected WellKnownCACertificates to be set")
					}
				} else {
					if len(policy.Spec.Validation.CACertificateRefs) == 0 {
						t.Error("expected CACertificateRefs to be set")
					}
				}
			}
		})
	}
}

func TestParseBackendTLSConfig(t *testing.T) {
	testCases := []struct {
		name           string
		annotations    map[string]string
		expectConfig   bool
		expectedProto  string
		expectedVerify bool
	}{
		{
			name: "HTTPS backend",
			annotations: map[string]string{
				"nginx.ingress.kubernetes.io/backend-protocol": "HTTPS",
			},
			expectConfig:   true,
			expectedProto:  "HTTPS",
			expectedVerify: false,
		},
		{
			name: "GRPCS backend with verify on",
			annotations: map[string]string{
				"nginx.ingress.kubernetes.io/backend-protocol": "GRPCS",
				"nginx.ingress.kubernetes.io/proxy-ssl-verify": "on",
			},
			expectConfig:   true,
			expectedProto:  "GRPCS",
			expectedVerify: true,
		},
		{
			name: "HTTP backend - no config",
			annotations: map[string]string{
				"nginx.ingress.kubernetes.io/backend-protocol": "HTTP",
			},
			expectConfig: false,
		},
		{
			name:         "No annotations - no config",
			annotations:  map[string]string{},
			expectConfig: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ingress := &networkingv1.Ingress{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: tc.annotations,
				},
			}

			config := parseBackendTLSConfig(ingress)

			if tc.expectConfig {
				if config == nil {
					t.Fatal("expected config but got nil")
				}
				if config.protocol != tc.expectedProto {
					t.Errorf("expected protocol %s, got %s", tc.expectedProto, config.protocol)
				}
				if config.sslVerify != tc.expectedVerify {
					t.Errorf("expected verify %v, got %v", tc.expectedVerify, config.sslVerify)
				}
			} else {
				if config != nil {
					t.Errorf("expected nil config but got %+v", config)
				}
			}
		})
	}
}
