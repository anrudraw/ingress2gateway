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
	"github.com/kubernetes-sigs/ingress2gateway/pkg/i2gw"
	"github.com/kubernetes-sigs/ingress2gateway/pkg/i2gw/intermediate"
	"github.com/kubernetes-sigs/ingress2gateway/pkg/i2gw/notifications"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
	gatewayv1beta1 "sigs.k8s.io/gateway-api/apis/v1beta1"
)

// buildCrossNamespaceReferenceGrants creates ReferenceGrants to allow HTTPRoutes
// in service namespaces to reference Gateways in gateway namespaces.
//
// Gateway namespace patterns:
// - Centralized: Gateway in istio-system (or configured namespace)
// - Per-namespace: Gateway in dedicated <service>-gateway namespace
//   Example: fhir → fhir-gateway/fhir-gateway
//
// HTTPRoutes live in service namespaces (fhir, dicom, etc.)
// Gateway API requires explicit permission for cross-namespace references
//
// The ReferenceGrant allows:
// - HTTPRoutes from service namespaces to reference their Gateway
func buildCrossNamespaceReferenceGrants(ir intermediate.IR, gatewayResources *i2gw.GatewayResources, gwConfig GatewayConfig) {
	// Collect unique service namespaces from HTTPRoutes
	serviceNamespaces := make(map[string]bool)
	for routeKey := range ir.HTTPRoutes {
		serviceNamespaces[routeKey.Namespace] = true
	}

	// Create a ReferenceGrant in the gateway namespace for each service namespace
	for serviceNS := range serviceNamespaces {
		// Get the gateway namespace and name for this service
		gatewayNS, gatewayName := gwConfig.GetGatewayRef(serviceNS)
		
		// Skip if route is in the same namespace as its gateway (no cross-namespace ref needed)
		if serviceNS == gatewayNS {
			continue
		}
		
		grantKey := types.NamespacedName{
			Namespace: gatewayNS,
			Name:      "allow-routes-from-" + serviceNS,
		}

		grant := gatewayv1beta1.ReferenceGrant{
			ObjectMeta: metav1.ObjectMeta{
				Name:      grantKey.Name,
				Namespace: grantKey.Namespace,
				Labels: map[string]string{
					"app.kubernetes.io/managed-by": "ingress2gateway",
					"gateway-api-migration":        "true",
				},
				Annotations: map[string]string{
					"ingress2gateway.kubernetes.io/source-namespace": serviceNS,
					"ingress2gateway.kubernetes.io/description":      "Allows HTTPRoutes from " + serviceNS + " to reference Gateway " + gatewayName,
				},
			},
			Spec: gatewayv1beta1.ReferenceGrantSpec{
				// Allow HTTPRoutes from the service namespace
				From: []gatewayv1beta1.ReferenceGrantFrom{
					{
						Group:     gatewayv1.GroupName,
						Kind:      "HTTPRoute",
						Namespace: gatewayv1.Namespace(serviceNS),
					},
					{
						Group:     gatewayv1.GroupName,
						Kind:      "GRPCRoute",
						Namespace: gatewayv1.Namespace(serviceNS),
					},
				},
				// To reference the Gateway (either centralized or per-namespace)
				To: []gatewayv1beta1.ReferenceGrantTo{
					{
						Group: gatewayv1.GroupName,
						Kind:  "Gateway",
						Name:  (*gatewayv1.ObjectName)(&gatewayName),
					},
				},
			},
		}

		// Initialize map if needed
		if gatewayResources.ReferenceGrants == nil {
			gatewayResources.ReferenceGrants = make(map[types.NamespacedName]gatewayv1beta1.ReferenceGrant)
		}

		gatewayResources.ReferenceGrants[grantKey] = grant

		// Emit notification
		notify(notifications.InfoNotification,
			"Generated ReferenceGrant '"+grantKey.Name+"' in namespace '"+grantKey.Namespace+
				"' to allow HTTPRoutes from '"+serviceNS+"' to reference Gateway '"+gatewayName+"'",
			nil,
		)
	}
}
