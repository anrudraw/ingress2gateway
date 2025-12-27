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

const (
	// External authentication annotations
	authURLAnnotation             = "nginx.ingress.kubernetes.io/auth-url"
	authMethodAnnotation          = "nginx.ingress.kubernetes.io/auth-method"
	authSigninAnnotation          = "nginx.ingress.kubernetes.io/auth-signin"
	authResponseHeadersAnnotation = "nginx.ingress.kubernetes.io/auth-response-headers"
	authRequestRedirectAnnotation = "nginx.ingress.kubernetes.io/auth-request-redirect"
	authCacheKeyAnnotation        = "nginx.ingress.kubernetes.io/auth-cache-key"
	authCacheDurationAnnotation   = "nginx.ingress.kubernetes.io/auth-cache-duration"
	authSnippetAnnotation         = "nginx.ingress.kubernetes.io/auth-snippet"
)

// externalAuthFeature parses external authentication annotations and stores them in the IR.
// These settings map to Gateway API SecurityPolicy.extAuth (implementation-specific).
func externalAuthFeature(ingresses []networkingv1.Ingress, _ map[types.NamespacedName]map[string]int32, ir *intermediate.IR) field.ErrorList {
	var errs field.ErrorList

	for _, ing := range ingresses {
		config := parseExternalAuthConfig(&ing)
		if config == nil {
			continue
		}

		// Get or create the ingress-nginx IR for this route
		key := types.NamespacedName{
			Namespace: ing.Namespace,
			Name:      ing.Name,
		}

		routeKey := findHTTPRouteKey(ir, key)
		if routeKey == (types.NamespacedName{}) {
			continue
		}

		routeCtx := ir.HTTPRoutes[routeKey]
		if routeCtx.ProviderSpecificIR.IngressNginx == nil {
			routeCtx.ProviderSpecificIR.IngressNginx = &intermediate.IngressNginxHTTPRouteIR{}
		}

		routeCtx.ProviderSpecificIR.IngressNginx.ExternalAuth = config
		ir.HTTPRoutes[routeKey] = routeCtx

		notify(notifications.InfoNotification,
			fmt.Sprintf("External auth config stored in IR (URL: %s). Requires SecurityPolicy to apply.", config.URL),
			&ing,
		)
	}

	return errs
}

// parseExternalAuthConfig extracts external auth configuration from ingress annotations
func parseExternalAuthConfig(ing *networkingv1.Ingress) *intermediate.ExternalAuthConfig {
	annotations := ing.GetAnnotations()
	if annotations == nil {
		return nil
	}

	// Check if auth-url is set - this is required for external auth
	authURL := annotations[authURLAnnotation]
	if authURL == "" {
		return nil
	}

	config := &intermediate.ExternalAuthConfig{
		URL: authURL,
	}

	// Parse auth-method (default: GET)
	method := annotations[authMethodAnnotation]
	if method == "" {
		method = "GET"
	}
	config.Method = strings.ToUpper(method)

	// Parse auth-signin
	config.SigninURL = annotations[authSigninAnnotation]

	// Parse auth-response-headers (comma-separated list)
	if headers := annotations[authResponseHeadersAnnotation]; headers != "" {
		headerList := strings.Split(headers, ",")
		for i, h := range headerList {
			headerList[i] = strings.TrimSpace(h)
		}
		config.ResponseHeaders = headerList
	}

	// Parse auth-request-redirect
	config.RequestRedirect = annotations[authRequestRedirectAnnotation]

	// Parse auth-cache-key
	config.CacheKey = annotations[authCacheKeyAnnotation]

	// Parse auth-cache-duration
	config.CacheDuration = annotations[authCacheDurationAnnotation]

	// Check for auth-snippet (not directly supported, just note it)
	if snippet := annotations[authSnippetAnnotation]; snippet != "" {
		notify(notifications.WarningNotification,
			"auth-snippet annotation detected. Custom auth snippets are not supported in Gateway API and may require EnvoyPatchPolicy.",
			ing,
		)
	}

	return config
}
