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

	"github.com/kubernetes-sigs/ingress2gateway/pkg/i2gw"
	"github.com/kubernetes-sigs/ingress2gateway/pkg/i2gw/intermediate"
	"github.com/kubernetes-sigs/ingress2gateway/pkg/i2gw/notifications"
	"github.com/kubernetes-sigs/ingress2gateway/pkg/i2gw/providers/common"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/validation/field"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

const (
	// SSL redirect annotations
	sslRedirectAnnotation      = "nginx.ingress.kubernetes.io/ssl-redirect"
	forceSSLRedirectAnnotation = "nginx.ingress.kubernetes.io/force-ssl-redirect"
)

// sslRedirectFeature processes ssl-redirect and force-ssl-redirect annotations
// and adds RequestRedirect filters to HTTPRoutes
func sslRedirectFeature(ingresses []networkingv1.Ingress, _ map[types.NamespacedName]map[string]int32, ir *intermediate.IR) field.ErrorList {
	var errList field.ErrorList

	// Build a map of ingress to SSL redirect config
	ingressRedirects := make(map[types.NamespacedName]bool)
	for _, ingress := range ingresses {
		sslRedirect := ingress.Annotations[sslRedirectAnnotation]
		forceSSLRedirect := ingress.Annotations[forceSSLRedirectAnnotation]

		// Check if SSL redirect is enabled
		if sslRedirect == "true" || forceSSLRedirect == "true" {
			key := types.NamespacedName{Namespace: ingress.Namespace, Name: ingress.Name}
			ingressRedirects[key] = true
		}
	}

	if len(ingressRedirects) == 0 {
		return errList
	}

	// Apply redirects to HTTPRoutes
	ruleGroups := common.GetRuleGroups(ingresses)
	for _, rg := range ruleGroups {
		routeKey := types.NamespacedName{Namespace: rg.Namespace, Name: common.RouteName(rg.Name, rg.Host)}
		httpRouteContext, ok := ir.HTTPRoutes[routeKey]
		if !ok {
			continue
		}

		// Find if any contributing ingress has SSL redirect enabled
		hasSSLRedirect := false
		for _, ingress := range ingresses {
			ingressKey := types.NamespacedName{Namespace: ingress.Namespace, Name: ingress.Name}
			if ingressRedirects[ingressKey] && matchesRoute(&ingress, rg.Host) {
				hasSSLRedirect = true
				break
			}
		}

		if !hasSSLRedirect {
			continue
		}

		// Store the SSL redirect requirement in the IR for use by buildSSLRedirectRoutes
		if httpRouteContext.ProviderSpecificIR.IngressNginx == nil {
			httpRouteContext.ProviderSpecificIR.IngressNginx = &intermediate.IngressNginxHTTPRouteIR{}
		}
		httpRouteContext.ProviderSpecificIR.IngressNginx.SSLRedirect = true

		ir.HTTPRoutes[routeKey] = httpRouteContext
		
		// Notification now INFO since we generate the redirect route automatically
		notify(notifications.InfoNotification,
			fmt.Sprintf("HTTPRoute %s/%s has ssl-redirect enabled. A redirect HTTPRoute will be generated for HTTP→HTTPS redirect.",
				routeKey.Namespace, routeKey.Name),
			&httpRouteContext.HTTPRoute)
	}

	return errList
}

// buildSSLRedirectFilter creates a RequestRedirect filter for HTTP to HTTPS redirect
// This can be used when creating separate HTTP redirect routes
func buildSSLRedirectFilter() gatewayv1.HTTPRouteFilter {
	statusCode := 301
	scheme := "https"

	return gatewayv1.HTTPRouteFilter{
		Type: gatewayv1.HTTPRouteFilterRequestRedirect,
		RequestRedirect: &gatewayv1.HTTPRequestRedirectFilter{
			Scheme:     &scheme,
			StatusCode: &statusCode,
		},
	}
}

// buildSSLRedirectRoutes creates HTTPRoutes for HTTP→HTTPS redirect
// These routes attach to the HTTP listener and redirect to HTTPS
func buildSSLRedirectRoutes(ir intermediate.IR, gatewayResources *i2gw.GatewayResources, gwConfig GatewayConfig) {
	// Find HTTPRoutes that need SSL redirect
	for routeKey, routeCtx := range ir.HTTPRoutes {
		// Check if SSL redirect is enabled via the IR flag
		if routeCtx.ProviderSpecificIR.IngressNginx == nil || !routeCtx.ProviderSpecificIR.IngressNginx.SSLRedirect {
			continue
		}
		
		route := routeCtx.HTTPRoute
		
		// Get the gateway reference
		gwNamespace, gwName := gwConfig.GetGatewayRef(routeKey.Namespace)
		
		// Find HTTP listener name for this route's host
		for _, hostname := range route.Spec.Hostnames {
			httpListenerName := buildHTTPListenerName(string(hostname))
			
			// Create redirect HTTPRoute
			redirectRouteKey := types.NamespacedName{
				Namespace: routeKey.Namespace,
				Name:      routeKey.Name + "-redirect",
			}
			
			// Skip if redirect route already exists
			if _, exists := gatewayResources.HTTPRoutes[redirectRouteKey]; exists {
				continue
			}
			
			gwNs := gatewayv1.Namespace(gwNamespace)
			sectionName := gatewayv1.SectionName(httpListenerName)
			
			redirectRoute := gatewayv1.HTTPRoute{
				TypeMeta: metav1.TypeMeta{
					APIVersion: "gateway.networking.k8s.io/v1",
					Kind:       "HTTPRoute",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:      redirectRouteKey.Name,
					Namespace: redirectRouteKey.Namespace,
					Labels: map[string]string{
						"app.kubernetes.io/managed-by": "ingress2gateway",
						"gateway-api-migration":        "true",
					},
					Annotations: map[string]string{
						"ingress2gateway.kubernetes.io/source":      "nginx.ingress.kubernetes.io/ssl-redirect",
						"ingress2gateway.kubernetes.io/description": "HTTP to HTTPS redirect route",
					},
				},
				Spec: gatewayv1.HTTPRouteSpec{
					CommonRouteSpec: gatewayv1.CommonRouteSpec{
						ParentRefs: []gatewayv1.ParentReference{
							{
								Name:        gatewayv1.ObjectName(gwName),
								Namespace:   &gwNs,
								SectionName: &sectionName, // Attach to HTTP listener only
							},
						},
					},
					Hostnames: []gatewayv1.Hostname{hostname},
					Rules: []gatewayv1.HTTPRouteRule{
						{
							Filters: []gatewayv1.HTTPRouteFilter{
								buildSSLRedirectFilter(),
							},
						},
					},
				},
			}
			
			gatewayResources.HTTPRoutes[redirectRouteKey] = redirectRoute
			
			notify(notifications.InfoNotification,
				fmt.Sprintf("Generated SSL redirect HTTPRoute %s/%s for HTTP→HTTPS redirect on host %s",
					redirectRouteKey.Namespace, redirectRouteKey.Name, hostname),
				&redirectRoute,
			)
		}
	}
}

// buildHTTPListenerName creates the HTTP listener name for a given hostname
func buildHTTPListenerName(hostname string) string {
	// Convert hostname to listener name format (replace dots with dashes)
	// This should match how the Gateway listeners are named
	name := hostname
	for i := 0; i < len(name); i++ {
		if name[i] == '.' {
			name = name[:i] + "-" + name[i+1:]
		}
	}
	return name + "-http"
}
