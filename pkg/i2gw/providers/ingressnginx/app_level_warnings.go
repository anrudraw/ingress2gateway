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
	"strings"

	"github.com/kubernetes-sigs/ingress2gateway/pkg/i2gw/intermediate"
	"github.com/kubernetes-sigs/ingress2gateway/pkg/i2gw/notifications"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/validation/field"
)

// Annotations that CANNOT be translated and require app-level changes
var appLevelAnnotations = map[string]string{
	"nginx.ingress.kubernetes.io/server-snippet": `
SERVER-SNIPPET REQUIRES APP CHANGES:
This annotation contains custom NGINX configuration that has no Gateway API equivalent.
The logic must be moved to your application code.
Common patterns to refactor:
- Custom headers: Use application middleware
- Redirects: Implement in application routing
- Access control: Use application-level auth`,

	"nginx.ingress.kubernetes.io/configuration-snippet": `
CONFIGURATION-SNIPPET REQUIRES APP CHANGES:
This annotation contains custom NGINX location configuration.
The logic must be moved to your application or handled differently:
- Custom response headers: Use application middleware
- Proxy modifications: May need EnvoyFilter or app changes
- Lua scripts: Must be rewritten for Envoy or moved to app`,

	"nginx.ingress.kubernetes.io/use-regex": `
REGEX PATH MATCHING NOT GA IN GATEWAY API:
The 'use-regex' annotation enables regex path matching which is NOT GA in Gateway API.
Options:
1. REFACTOR PATHS: Change your API to use prefix-based paths (recommended)
2. EXPERIMENTAL: Enable RegularExpression PathType (requires experimental channel)
3. ISTIO SPECIFIC: Use VirtualService with regex matching (not portable)
This requires coordination with your service team to update API paths.`,

	"nginx.ingress.kubernetes.io/rewrite-target": `
REWRITE-TARGET WITH CAPTURE GROUPS REQUIRES APP CHANGES:
Gateway API URLRewrite filter does NOT support regex capture groups.
If your rewrite uses $1, $2, etc., you must:
1. Refactor your application to accept the original paths
2. Or implement path rewriting in your application/reverse proxy
Simple rewrites (without capture groups) can use HTTPRoute URLRewrite filter.`,
}

// Annotations that require special handling in meshless Istio
var meshlessWarningAnnotations = map[string]string{
	"nginx.ingress.kubernetes.io/auth-url": `
EXTERNAL AUTH IN MESHLESS ISTIO:
With meshless Istio (no sidecars), external auth cannot be applied per-service.
Options:
1. GATEWAY-LEVEL: Configure ext_authz on the Gateway (applies to all routes)
2. APP-LEVEL: Implement auth check in your application (recommended for per-route control)
3. ADD MESH: Enable Istio sidecars for this service to use AuthorizationPolicy
EnvoyFilter for Gateway-level auth has been generated, but consider app-level auth for fine-grained control.`,

	"nginx.ingress.kubernetes.io/auth-tls-secret": `
CLIENT CERT AUTH IN MESHLESS ISTIO:
With per-namespace Gateways, client cert validation can be configured on the Gateway listener.
However, this applies to ALL routes on that listener.
For per-customer client cert validation:
1. SEPARATE LISTENERS: Create separate Gateway listeners per customer
2. APP-LEVEL: Pass client cert to app and validate there (auth-tls-pass-certificate-to-upstream)
3. ADD MESH: Enable sidecars to use PeerAuthentication per-service`,
}

// appLevelWarningsFeature checks for annotations that require app-level changes
// and emits warnings to help teams understand what needs manual work
func appLevelWarningsFeature(ingresses []networkingv1.Ingress, _ map[types.NamespacedName]map[string]int32, _ *intermediate.IR) field.ErrorList {
	var errs field.ErrorList

	for _, ing := range ingresses {
		annotations := ing.GetAnnotations()
		if annotations == nil {
			continue
		}

		// Check for annotations requiring app-level changes
		for annotation, warningMsg := range appLevelAnnotations {
			if value, exists := annotations[annotation]; exists {
				notify(notifications.ErrorNotification,
					fmt.Sprintf("MIGRATION BLOCKER - %s\n%s\nCurrent value: %s",
						annotation, strings.TrimSpace(warningMsg), truncateValue(value)),
					&ing,
				)
			}
		}

		// Check for meshless-specific warnings
		for annotation, warningMsg := range meshlessWarningAnnotations {
			if _, exists := annotations[annotation]; exists {
				notify(notifications.WarningNotification,
					fmt.Sprintf("MESHLESS ISTIO LIMITATION - %s\n%s",
						annotation, strings.TrimSpace(warningMsg)),
					&ing,
				)
			}
		}

		// Check for rewrite-target with capture groups
		if rewrite := annotations["nginx.ingress.kubernetes.io/rewrite-target"]; rewrite != "" {
			if strings.Contains(rewrite, "$") {
				notify(notifications.ErrorNotification,
					fmt.Sprintf("MIGRATION BLOCKER - rewrite-target with capture groups\n%s\nCurrent value: %s",
						strings.TrimSpace(appLevelAnnotations["nginx.ingress.kubernetes.io/rewrite-target"]),
						rewrite),
					&ing,
				)
			}
		}
	}

	return errs
}

// truncateValue truncates long annotation values for display
func truncateValue(value string) string {
	if len(value) > 200 {
		return value[:200] + "... (truncated)"
	}
	return value
}
