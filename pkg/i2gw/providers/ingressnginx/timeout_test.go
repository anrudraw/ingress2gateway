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

	"github.com/kubernetes-sigs/ingress2gateway/pkg/i2gw"
	"github.com/kubernetes-sigs/ingress2gateway/pkg/i2gw/providers/common"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestTimeoutFeature(t *testing.T) {
	testCases := []struct {
		name             string
		ingresses        []networkingv1.Ingress
		expectedTimeout  string // expected request timeout
		expectError      bool
	}{
		{
			name: "read timeout sets request timeout",
			ingresses: []networkingv1.Ingress{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-ingress",
						Namespace: "default",
						Annotations: map[string]string{
							"nginx.ingress.kubernetes.io/proxy-read-timeout": "7200",
						},
					},
					Spec: networkingv1.IngressSpec{
						IngressClassName: strPtr("nginx"),
						Rules: []networkingv1.IngressRule{
							{
								Host: "example.com",
								IngressRuleValue: networkingv1.IngressRuleValue{
									HTTP: &networkingv1.HTTPIngressRuleValue{
										Paths: []networkingv1.HTTPIngressPath{
											{
												Path:     "/",
												PathType: pathTypePtr(networkingv1.PathTypePrefix),
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
			expectedTimeout: "7200s",
		},
		{
			name: "invalid timeout value causes error",
			ingresses: []networkingv1.Ingress{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-ingress",
						Namespace: "default",
						Annotations: map[string]string{
							"nginx.ingress.kubernetes.io/proxy-read-timeout": "invalid",
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
												Path:     "/",
												PathType: pathTypePtr(networkingv1.PathTypePrefix),
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
			expectError: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// First convert to IR using common converter
			ir, errs := common.ToIR(tc.ingresses, nil, i2gw.ProviderImplementationSpecificOptions{})
			if len(errs) > 0 && !tc.expectError {
				t.Fatalf("common.ToIR failed: %v", errs)
			}

			// Apply timeout feature
			errs = timeoutFeature(tc.ingresses, nil, &ir)

			if tc.expectError {
				if len(errs) == 0 {
					t.Error("expected error but got none")
				}
				return
			}

			if len(errs) > 0 {
				t.Fatalf("unexpected errors: %v", errs)
			}

			if tc.expectedTimeout != "" {
				// Find the HTTPRoute and check timeouts
				for _, routeCtx := range ir.HTTPRoutes {
					for _, rule := range routeCtx.HTTPRoute.Spec.Rules {
						if rule.Timeouts == nil {
							t.Error("expected timeouts to be set")
							continue
						}
						if rule.Timeouts.Request == nil {
							t.Error("expected request timeout to be set")
							continue
						}
						if string(*rule.Timeouts.Request) != tc.expectedTimeout {
							t.Errorf("expected timeout %s, got %s", tc.expectedTimeout, *rule.Timeouts.Request)
						}
					}
				}
			}
		})
	}
}

func TestParseTimeoutConfig(t *testing.T) {
	testCases := []struct {
		name            string
		annotations     map[string]string
		expectConfig    bool
		expectedRead    int
		expectedSend    int
		expectedConnect int
		expectError     bool
	}{
		{
			name: "all timeouts set",
			annotations: map[string]string{
				"nginx.ingress.kubernetes.io/proxy-connect-timeout": "10",
				"nginx.ingress.kubernetes.io/proxy-read-timeout":    "60",
				"nginx.ingress.kubernetes.io/proxy-send-timeout":    "30",
			},
			expectConfig:    true,
			expectedConnect: 10,
			expectedRead:    60,
			expectedSend:    30,
		},
		{
			name: "only read timeout",
			annotations: map[string]string{
				"nginx.ingress.kubernetes.io/proxy-read-timeout": "7200",
			},
			expectConfig: true,
			expectedRead: 7200,
		},
		{
			name:         "no timeout annotations",
			annotations:  map[string]string{},
			expectConfig: false,
		},
		{
			name: "invalid timeout value",
			annotations: map[string]string{
				"nginx.ingress.kubernetes.io/proxy-read-timeout": "not-a-number",
			},
			expectError: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ingress := &networkingv1.Ingress{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: tc.annotations,
				},
			}

			config, err := parseTimeoutConfig(ingress)

			if tc.expectError {
				if err == nil {
					t.Error("expected error but got none")
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if tc.expectConfig {
				if config == nil {
					t.Fatal("expected config but got nil")
				}
				if config.readTimeout != tc.expectedRead {
					t.Errorf("expected read timeout %d, got %d", tc.expectedRead, config.readTimeout)
				}
				if config.sendTimeout != tc.expectedSend {
					t.Errorf("expected send timeout %d, got %d", tc.expectedSend, config.sendTimeout)
				}
				if config.connectTimeout != tc.expectedConnect {
					t.Errorf("expected connect timeout %d, got %d", tc.expectedConnect, config.connectTimeout)
				}
			} else {
				if config != nil {
					t.Errorf("expected nil config but got %+v", config)
				}
			}
		})
	}
}

func strPtr(s string) *string {
	return &s
}

func pathTypePtr(pt networkingv1.PathType) *networkingv1.PathType {
	return &pt
}
