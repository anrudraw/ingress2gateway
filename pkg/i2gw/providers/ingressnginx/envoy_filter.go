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
	"fmt"
	"strconv"
	"strings"

	"github.com/kubernetes-sigs/ingress2gateway/pkg/i2gw"
	"github.com/kubernetes-sigs/ingress2gateway/pkg/i2gw/intermediate"
	"github.com/kubernetes-sigs/ingress2gateway/pkg/i2gw/notifications"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/validation/field"
)

// envoyFilterFeature is a no-op feature parser - the actual EnvoyFilter generation
// happens in buildIstioEnvoyFilters during ToGatewayResources.
// This parser exists to emit notifications about EnvoyFilter generation.
func envoyFilterFeature(ingresses []networkingv1.Ingress, _ map[types.NamespacedName]map[string]int32, ir *intermediate.IR) field.ErrorList {
	var errs field.ErrorList

	// Count EnvoyFilter-related annotations for notification purposes
	for _, ing := range ingresses {
		annotations := ing.GetAnnotations()
		if annotations == nil {
			continue
		}

		hasEnvoyConfig := false
		details := []string{}

		if _, ok := annotations["nginx.ingress.kubernetes.io/limit-rps"]; ok {
			hasEnvoyConfig = true
			details = append(details, "rate-limiting")
		}
		if _, ok := annotations["nginx.ingress.kubernetes.io/proxy-body-size"]; ok {
			hasEnvoyConfig = true
			details = append(details, "body-size")
		}
		if v, ok := annotations["nginx.ingress.kubernetes.io/proxy-buffering"]; ok && v == "off" {
			hasEnvoyConfig = true
			details = append(details, "no-buffering")
		}

		if hasEnvoyConfig {
			notify(notifications.InfoNotification,
				fmt.Sprintf("EnvoyFilter will be generated for: %s (see --ingress-nginx-gateway-mode flag for targeting)",
					strings.Join(details, ", ")),
				&ing,
			)
		}
	}

	return errs
}

// buildIstioEnvoyFilters creates Istio EnvoyFilter resources from IR
// and adds them to GatewayResources.GatewayExtensions
func buildIstioEnvoyFilters(ir intermediate.IR, gatewayResources *i2gw.GatewayResources, gwConfig GatewayConfig) {
	generator := &EnvoyFilterGenerator{GatewayConfig: gwConfig}
	filters := generator.GenerateEnvoyFilters(ir)

	for _, filter := range filters {
		if filter != nil {
			gatewayResources.GatewayExtensions = append(gatewayResources.GatewayExtensions, *filter)
		}
	}
}

// EnvoyFilterGenerator generates Istio EnvoyFilter resources from IR
// for annotations that require Envoy-level configuration.
//
// Gateway modes:
// - per-namespace: Gateway name is <namespace>-gateway in each namespace
// - centralized: Single Gateway (e.g., platform-gateway) in istio-system
type EnvoyFilterGenerator struct {
	GatewayConfig GatewayConfig
}

// GenerateEnvoyFilters creates EnvoyFilter resources for the given IR
// Returns a map of NamespacedName to unstructured EnvoyFilter
func (g *EnvoyFilterGenerator) GenerateEnvoyFilters(ir intermediate.IR) map[types.NamespacedName]*unstructured.Unstructured {
	filters := make(map[types.NamespacedName]*unstructured.Unstructured)

	// Process HTTPRoutes for rate limiting, body size, buffering configs
	for routeKey, routeCtx := range ir.HTTPRoutes {
		if routeCtx.ProviderSpecificIR.IngressNginx == nil {
			continue
		}

		nginxIR := routeCtx.ProviderSpecificIR.IngressNginx
		
		// Get the gateway reference based on mode
		gwNamespace, gwName := g.GatewayConfig.GetGatewayRef(routeKey.Namespace)
		
		// For centralized mode, EnvoyFilters go in the gateway namespace
		filterNamespace := routeKey.Namespace
		if g.GatewayConfig.IsCentralized() {
			filterNamespace = gwNamespace
		}

		// Generate rate limit EnvoyFilter if configured
		if nginxIR.RateLimitRPS > 0 {
			filterKey := types.NamespacedName{
				Namespace: filterNamespace,
				Name:      fmt.Sprintf("%s-%s-ratelimit", routeKey.Namespace, routeKey.Name),
			}
			filters[filterKey] = g.buildRateLimitEnvoyFilter(
				filterKey,
				gwNamespace,
				gwName,
				nginxIR.RateLimitRPS,
				nginxIR.RateLimitBurst,
			)
		}

		// Generate body size EnvoyFilter if configured
		if nginxIR.ProxyBodySize != "" {
			filterKey := types.NamespacedName{
				Namespace: filterNamespace,
				Name:      fmt.Sprintf("%s-%s-bodysize", routeKey.Namespace, routeKey.Name),
			}
			filters[filterKey] = g.buildBodySizeEnvoyFilter(
				filterKey,
				gwNamespace,
				gwName,
				nginxIR.ProxyBodySize,
			)
		}

		// Generate buffering EnvoyFilter if configured
		if nginxIR.ProxyBuffering != nil && !*nginxIR.ProxyBuffering {
			filterKey := types.NamespacedName{
				Namespace: filterNamespace,
				Name:      fmt.Sprintf("%s-%s-nobuffer", routeKey.Namespace, routeKey.Name),
			}
			filters[filterKey] = g.buildNoBufferingEnvoyFilter(
				filterKey,
				gwNamespace,
				gwName,
			)
		}

		// Generate ext_authz EnvoyFilter if configured (per-namespace mode only)
		// In per-namespace mode, the Gateway is namespace-scoped so ext_authz applies only to that namespace
		if nginxIR.ExternalAuth != nil && nginxIR.ExternalAuth.URL != "" {
			filterKey := types.NamespacedName{
				Namespace: filterNamespace,
				Name:      fmt.Sprintf("%s-%s-extauthz", routeKey.Namespace, routeKey.Name),
			}
			filters[filterKey] = g.buildExtAuthzEnvoyFilter(
				filterKey,
				gwNamespace,
				gwName,
				nginxIR.ExternalAuth,
			)
		}
	}

	return filters
}

// buildRateLimitEnvoyFilter creates an EnvoyFilter for local rate limiting
func (g *EnvoyFilterGenerator) buildRateLimitEnvoyFilter(
	key types.NamespacedName,
	gatewayNamespace string,
	gatewayName string,
	rps int,
	burst int,
) *unstructured.Unstructured {
	if burst == 0 {
		burst = rps * 5 // Default burst to 5x RPS if not specified
	}

	filter := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "networking.istio.io/v1alpha3",
			"kind":       "EnvoyFilter",
			"metadata": map[string]interface{}{
				"name":      key.Name,
				"namespace": key.Namespace,
				"labels": map[string]interface{}{
					"app.kubernetes.io/managed-by": "ingress2gateway",
					"gateway-api-migration":        "true",
				},
				"annotations": map[string]interface{}{
					"ingress2gateway.kubernetes.io/source": "nginx.ingress.kubernetes.io/limit-rps",
				},
			},
			"spec": map[string]interface{}{
				// Target the Gateway (either per-namespace or centralized)
				"targetRefs": []interface{}{
					map[string]interface{}{
						"kind":      "Gateway",
						"group":     "gateway.networking.k8s.io",
						"name":      gatewayName,
						"namespace": gatewayNamespace,
					},
				},
				"configPatches": []interface{}{
					map[string]interface{}{
						"applyTo": "HTTP_FILTER",
						"match": map[string]interface{}{
							"context": "GATEWAY",
							"listener": map[string]interface{}{
								"filterChain": map[string]interface{}{
									"filter": map[string]interface{}{
										"name": "envoy.filters.network.http_connection_manager",
										"subFilter": map[string]interface{}{
											"name": "envoy.filters.http.router",
										},
									},
								},
							},
						},
						"patch": map[string]interface{}{
							"operation": "INSERT_BEFORE",
							"value": map[string]interface{}{
								"name": "envoy.filters.http.local_ratelimit",
								"typed_config": map[string]interface{}{
									"@type":       "type.googleapis.com/envoy.extensions.filters.http.local_ratelimit.v3.LocalRateLimit",
									"stat_prefix": "http_local_rate_limiter",
									"token_bucket": map[string]interface{}{
										"max_tokens":     burst,
										"tokens_per_fill": rps,
										"fill_interval":  "1s",
									},
									"filter_enabled": map[string]interface{}{
										"runtime_key": "local_rate_limit_enabled",
										"default_value": map[string]interface{}{
											"numerator":   100,
											"denominator": "HUNDRED",
										},
									},
									"filter_enforced": map[string]interface{}{
										"runtime_key": "local_rate_limit_enforced",
										"default_value": map[string]interface{}{
											"numerator":   100,
											"denominator": "HUNDRED",
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}

	return filter
}

// buildBodySizeEnvoyFilter creates an EnvoyFilter to set max request body size
func (g *EnvoyFilterGenerator) buildBodySizeEnvoyFilter(
	key types.NamespacedName,
	gatewayNamespace string,
	gatewayName string,
	bodySize string,
) *unstructured.Unstructured {

	// Parse body size to bytes
	bytes, err := ParseBodySize(bodySize)
	if err != nil || bytes == 0 {
		bytes = 1024 * 1024 * 1024 // Default 1GB if parsing fails
	}

	filter := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "networking.istio.io/v1alpha3",
			"kind":       "EnvoyFilter",
			"metadata": map[string]interface{}{
				"name":      key.Name,
				"namespace": key.Namespace,
				"labels": map[string]interface{}{
					"app.kubernetes.io/managed-by": "ingress2gateway",
					"gateway-api-migration":        "true",
				},
				"annotations": map[string]interface{}{
					"ingress2gateway.kubernetes.io/source":     "nginx.ingress.kubernetes.io/proxy-body-size",
					"ingress2gateway.kubernetes.io/body-size":  bodySize,
				},
			},
			"spec": map[string]interface{}{
				"targetRefs": []interface{}{
					map[string]interface{}{
						"kind":      "Gateway",
						"group":     "gateway.networking.k8s.io",
						"name":      gatewayName,
						"namespace": gatewayNamespace,
					},
				},
				"configPatches": []interface{}{
					map[string]interface{}{
						"applyTo": "NETWORK_FILTER",
						"match": map[string]interface{}{
							"context": "GATEWAY",
							"listener": map[string]interface{}{
								"filterChain": map[string]interface{}{
									"filter": map[string]interface{}{
										"name": "envoy.filters.network.http_connection_manager",
									},
								},
							},
						},
						"patch": map[string]interface{}{
							"operation": "MERGE",
							"value": map[string]interface{}{
								"typed_config": map[string]interface{}{
									"@type": "type.googleapis.com/envoy.extensions.filters.network.http_connection_manager.v3.HttpConnectionManager",
									"route_config": map[string]interface{}{
										"max_direct_response_body_size_bytes": bytes,
									},
								},
							},
						},
					},
					// Also set on the route level for request body
					map[string]interface{}{
						"applyTo": "HTTP_FILTER",
						"match": map[string]interface{}{
							"context": "GATEWAY",
							"listener": map[string]interface{}{
								"filterChain": map[string]interface{}{
									"filter": map[string]interface{}{
										"name": "envoy.filters.network.http_connection_manager",
										"subFilter": map[string]interface{}{
											"name": "envoy.filters.http.router",
										},
									},
								},
							},
						},
						"patch": map[string]interface{}{
							"operation": "INSERT_BEFORE",
							"value": map[string]interface{}{
								"name": "envoy.filters.http.buffer",
								"typed_config": map[string]interface{}{
									"@type":             "type.googleapis.com/envoy.extensions.filters.http.buffer.v3.Buffer",
									"max_request_bytes": bytes,
								},
							},
						},
					},
				},
			},
		},
	}

	return filter
}

// buildNoBufferingEnvoyFilter creates an EnvoyFilter to disable proxy buffering
func (g *EnvoyFilterGenerator) buildNoBufferingEnvoyFilter(
	key types.NamespacedName,
	gatewayNamespace string,
	gatewayName string,
) *unstructured.Unstructured {

	filter := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "networking.istio.io/v1alpha3",
			"kind":       "EnvoyFilter",
			"metadata": map[string]interface{}{
				"name":      key.Name,
				"namespace": key.Namespace,
				"labels": map[string]interface{}{
					"app.kubernetes.io/managed-by": "ingress2gateway",
					"gateway-api-migration":        "true",
				},
				"annotations": map[string]interface{}{
					"ingress2gateway.kubernetes.io/source": "nginx.ingress.kubernetes.io/proxy-buffering",
				},
			},
			"spec": map[string]interface{}{
				"targetRefs": []interface{}{
					map[string]interface{}{
						"kind":      "Gateway",
						"group":     "gateway.networking.k8s.io",
						"name":      gatewayName,
						"namespace": gatewayNamespace,
					},
				},
				"configPatches": []interface{}{
					map[string]interface{}{
						"applyTo": "CLUSTER",
						"match": map[string]interface{}{
							"context": "GATEWAY",
						},
						"patch": map[string]interface{}{
							"operation": "MERGE",
							"value": map[string]interface{}{
								// Disable circuit breaker buffering
								"circuit_breakers": map[string]interface{}{
									"thresholds": []interface{}{
										map[string]interface{}{
											"max_pending_requests": 100000,
											"max_requests":         100000,
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}

	return filter
}

// buildExtAuthzEnvoyFilter creates an EnvoyFilter for external authorization
// This is generated in per-namespace mode where the Gateway is namespace-scoped
func (g *EnvoyFilterGenerator) buildExtAuthzEnvoyFilter(
	key types.NamespacedName,
	gatewayNamespace string,
	gatewayName string,
	authConfig *intermediate.ExternalAuthConfig,
) *unstructured.Unstructured {

	// Build headers to pass to auth service
	headersToUpstream := []interface{}{}
	for _, header := range authConfig.ResponseHeaders {
		headersToUpstream = append(headersToUpstream, map[string]interface{}{
			"exact_match": header,
		})
	}

	// Default headers if none specified
	if len(headersToUpstream) == 0 {
		headersToUpstream = []interface{}{
			map[string]interface{}{"exact_match": "authorization"},
			map[string]interface{}{"exact_match": "x-forwarded-user"},
			map[string]interface{}{"exact_match": "x-forwarded-email"},
		}
	}

	filter := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "networking.istio.io/v1alpha3",
			"kind":       "EnvoyFilter",
			"metadata": map[string]interface{}{
				"name":      key.Name,
				"namespace": key.Namespace,
				"labels": map[string]interface{}{
					"app.kubernetes.io/managed-by": "ingress2gateway",
					"gateway-api-migration":        "true",
				},
				"annotations": map[string]interface{}{
					"ingress2gateway.kubernetes.io/source":   "nginx.ingress.kubernetes.io/auth-url",
					"ingress2gateway.kubernetes.io/auth-url": authConfig.URL,
				},
			},
			"spec": map[string]interface{}{
				"targetRefs": []interface{}{
					map[string]interface{}{
						"kind":      "Gateway",
						"group":     "gateway.networking.k8s.io",
						"name":      gatewayName,
						"namespace": gatewayNamespace,
					},
				},
				"configPatches": []interface{}{
					map[string]interface{}{
						"applyTo": "HTTP_FILTER",
						"match": map[string]interface{}{
							"context": "GATEWAY",
							"listener": map[string]interface{}{
								"filterChain": map[string]interface{}{
									"filter": map[string]interface{}{
										"name": "envoy.filters.network.http_connection_manager",
										"subFilter": map[string]interface{}{
											"name": "envoy.filters.http.router",
										},
									},
								},
							},
						},
						"patch": map[string]interface{}{
							"operation": "INSERT_BEFORE",
							"value": map[string]interface{}{
								"name": "envoy.filters.http.ext_authz",
								"typed_config": map[string]interface{}{
									"@type": "type.googleapis.com/envoy.extensions.filters.http.ext_authz.v3.ExtAuthz",
									"http_service": map[string]interface{}{
										"server_uri": map[string]interface{}{
											"uri":     authConfig.URL,
											"cluster": "outbound|80||ext-authz-service", // This may need adjustment based on actual service
											"timeout": "5s",
										},
										"authorization_request": map[string]interface{}{
											"allowed_headers": map[string]interface{}{
												"patterns": []interface{}{
													map[string]interface{}{"exact": "authorization"},
													map[string]interface{}{"exact": "cookie"},
													map[string]interface{}{"prefix": "x-"},
												},
											},
										},
										"authorization_response": map[string]interface{}{
											"allowed_upstream_headers": map[string]interface{}{
												"patterns": headersToUpstream,
											},
										},
									},
									"failure_mode_allow": false,
								},
							},
						},
					},
				},
			},
		},
	}

	return filter
}

// GetEnvoyFilterGVK returns the GroupVersionKind for EnvoyFilter
func GetEnvoyFilterGVK() metav1.GroupVersionKind {
	return metav1.GroupVersionKind{
		Group:   "networking.istio.io",
		Version: "v1alpha3",
		Kind:    "EnvoyFilter",
	}
}

// ParseBodySize parses a body size string (e.g., "100m", "1g", "512k") to bytes
func ParseBodySize(size string) (int64, error) {
	if size == "" || size == "0" {
		return 0, nil
	}

	size = strings.ToLower(strings.TrimSpace(size))

	// Handle "unlimited" or similar
	if size == "unlimited" || size == "-1" {
		return 0, nil // 0 means unlimited in Envoy
	}

	var multiplier int64 = 1
	numStr := size

	if strings.HasSuffix(size, "k") {
		multiplier = 1024
		numStr = size[:len(size)-1]
	} else if strings.HasSuffix(size, "m") {
		multiplier = 1024 * 1024
		numStr = size[:len(size)-1]
	} else if strings.HasSuffix(size, "g") {
		multiplier = 1024 * 1024 * 1024
		numStr = size[:len(size)-1]
	}

	num, err := strconv.ParseInt(numStr, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid body size: %s", size)
	}

	return num * multiplier, nil
}
