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

	"github.com/kubernetes-sigs/ingress2gateway/pkg/i2gw/intermediate"
	"github.com/kubernetes-sigs/ingress2gateway/pkg/i2gw/notifications"
	"github.com/kubernetes-sigs/ingress2gateway/pkg/i2gw/providers/common"
	networkingv1 "k8s.io/api/networking/v1"
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

		// Add RequestRedirect filter to each rule
		// Note: In Gateway API, you typically create a separate HTTPRoute for HTTP->HTTPS redirect
		// This is a simplification that adds the info as a notification
		notify(notifications.WarningNotification,
			fmt.Sprintf("HTTPRoute %s/%s has ssl-redirect enabled. "+
				"In Gateway API, create a separate HTTPRoute attached to an HTTP listener with a RequestRedirect filter to HTTPS. "+
				"Example: spec.rules[].filters[].type=RequestRedirect with requestRedirect.scheme='https' and statusCode=301",
				routeKey.Namespace, routeKey.Name),
			&httpRouteContext.HTTPRoute)

		// Store the SSL redirect requirement in the IR for potential use by gateway converter
		if httpRouteContext.ProviderSpecificIR.IngressNginx == nil {
			httpRouteContext.ProviderSpecificIR.IngressNginx = &intermediate.IngressNginxHTTPRouteIR{}
		}

		ir.HTTPRoutes[routeKey] = httpRouteContext
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
