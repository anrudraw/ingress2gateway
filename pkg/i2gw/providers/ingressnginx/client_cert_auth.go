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

	"github.com/kubernetes-sigs/ingress2gateway/pkg/i2gw/intermediate"
	"github.com/kubernetes-sigs/ingress2gateway/pkg/i2gw/notifications"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/validation/field"
)

const (
	// Client certificate authentication annotations
	authTLSSecretAnnotation            = "nginx.ingress.kubernetes.io/auth-tls-secret"
	authTLSVerifyClientAnnotation      = "nginx.ingress.kubernetes.io/auth-tls-verify-client"
	authTLSVerifyDepthAnnotation       = "nginx.ingress.kubernetes.io/auth-tls-verify-depth"
	authTLSErrorPageAnnotation         = "nginx.ingress.kubernetes.io/auth-tls-error-page"
	authTLSPassCertToUpstreamAnnotation = "nginx.ingress.kubernetes.io/auth-tls-pass-certificate-to-upstream"
)

// clientCertAuthFeature parses client certificate authentication annotations and stores them in the IR.
// These settings map to Gateway API SecurityPolicy.clientValidation (implementation-specific).
func clientCertAuthFeature(ingresses []networkingv1.Ingress, _ map[types.NamespacedName]map[string]int32, ir *intermediate.IR) field.ErrorList {
	var errs field.ErrorList

	for _, ing := range ingresses {
		config, parseErrs := parseClientCertAuthConfig(&ing)
		if len(parseErrs) > 0 {
			errs = append(errs, parseErrs...)
			continue
		}
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

		routeCtx.ProviderSpecificIR.IngressNginx.ClientCertAuth = config
		ir.HTTPRoutes[routeKey] = routeCtx

		notify(notifications.InfoNotification,
			fmt.Sprintf("Client cert auth config stored in IR (secret: %s, verify: %s). Requires SecurityPolicy to apply.", config.Secret, config.VerifyClient),
			&ing,
		)
	}

	return errs
}

// parseClientCertAuthConfig extracts client certificate auth configuration from ingress annotations
func parseClientCertAuthConfig(ing *networkingv1.Ingress) (*intermediate.ClientCertAuthConfig, field.ErrorList) {
	annotations := ing.GetAnnotations()
	if annotations == nil {
		return nil, nil
	}

	// Check if auth-tls-secret is set - this is required for client cert auth
	secret := annotations[authTLSSecretAnnotation]
	if secret == "" {
		return nil, nil
	}

	var errs field.ErrorList
	config := &intermediate.ClientCertAuthConfig{
		Secret: secret,
	}

	// Parse verify-client (default: "on")
	verifyClient := annotations[authTLSVerifyClientAnnotation]
	if verifyClient == "" {
		verifyClient = "on"
	}
	// Valid values: on, off, optional, optional_no_ca
	switch verifyClient {
	case "on", "off", "optional", "optional_no_ca":
		config.VerifyClient = verifyClient
	default:
		errs = append(errs, field.Invalid(
			field.NewPath("metadata", "annotations", authTLSVerifyClientAnnotation),
			verifyClient,
			"invalid verify-client value, must be one of: on, off, optional, optional_no_ca",
		))
		config.VerifyClient = "on" // default
	}

	// Parse verify-depth (default: 1)
	if depth := annotations[authTLSVerifyDepthAnnotation]; depth != "" {
		val, err := strconv.Atoi(depth)
		if err != nil {
			errs = append(errs, field.Invalid(
				field.NewPath("metadata", "annotations", authTLSVerifyDepthAnnotation),
				depth,
				"invalid verify-depth value",
			))
		} else {
			config.VerifyDepth = val
		}
	} else {
		config.VerifyDepth = 1 // default
	}

	// Parse error-page
	config.ErrorPage = annotations[authTLSErrorPageAnnotation]

	// Parse pass-certificate-to-upstream
	if passCert := annotations[authTLSPassCertToUpstreamAnnotation]; passCert != "" {
		config.PassCertToUpstream = passCert == "true"
	}

	return config, errs
}
