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

	"github.com/kubernetes-sigs/ingress2gateway/pkg/i2gw/intermediate"
	"github.com/kubernetes-sigs/ingress2gateway/pkg/i2gw/notifications"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/validation/field"
)

const (
	// Proxy settings annotations
	proxyBodySizeAnnotation        = "nginx.ingress.kubernetes.io/proxy-body-size"
	proxyBufferingAnnotation       = "nginx.ingress.kubernetes.io/proxy-buffering"
	proxyRequestBufferingAnnotation = "nginx.ingress.kubernetes.io/proxy-request-buffering"
	loadBalanceAnnotation          = "nginx.ingress.kubernetes.io/load-balance"
)

// proxySettingsFeature parses proxy settings annotations and stores them in the IR.
// These settings map to Gateway API BackendTrafficPolicy (implementation-specific).
func proxySettingsFeature(ingresses []networkingv1.Ingress, _ map[types.NamespacedName]map[string]int32, ir *intermediate.IR) field.ErrorList {
	var errs field.ErrorList

	for _, ing := range ingresses {
		config := parseProxySettings(&ing)
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

		// Set proxy body size
		if config.ProxyBodySize != "" {
			routeCtx.ProviderSpecificIR.IngressNginx.ProxyBodySize = config.ProxyBodySize
			notify(notifications.InfoNotification,
				fmt.Sprintf("proxy-body-size '%s' stored in IR. Requires BackendTrafficPolicy to apply.", config.ProxyBodySize),
				&ing,
			)
		}

		// Set proxy buffering
		if config.ProxyBuffering != nil {
			routeCtx.ProviderSpecificIR.IngressNginx.ProxyBuffering = config.ProxyBuffering
			notify(notifications.InfoNotification,
				fmt.Sprintf("proxy-buffering '%v' stored in IR. Requires BackendTrafficPolicy to apply.", *config.ProxyBuffering),
				&ing,
			)
		}

		// Set proxy request buffering
		if config.ProxyRequestBuffering != nil {
			routeCtx.ProviderSpecificIR.IngressNginx.ProxyRequestBuffering = config.ProxyRequestBuffering
			notify(notifications.InfoNotification,
				fmt.Sprintf("proxy-request-buffering '%v' stored in IR. Requires BackendTrafficPolicy to apply.", *config.ProxyRequestBuffering),
				&ing,
			)
		}

		ir.HTTPRoutes[routeKey] = routeCtx
	}

	// Also check for load balancing algorithm on services
	for _, ing := range ingresses {
		annotations := ing.GetAnnotations()
		if annotations == nil {
			continue
		}

		lbAlgorithm := annotations[loadBalanceAnnotation]
		if lbAlgorithm == "" {
			continue
		}

		// Apply to all services referenced by this ingress
		for _, rule := range ing.Spec.Rules {
			if rule.HTTP == nil {
				continue
			}
			for _, path := range rule.HTTP.Paths {
				if path.Backend.Service == nil {
					continue
				}

				svcKey := types.NamespacedName{
					Namespace: ing.Namespace,
					Name:      path.Backend.Service.Name,
				}

				if svcCtx, exists := ir.Services[svcKey]; exists {
					if svcCtx.IngressNginx == nil {
						svcCtx.IngressNginx = &intermediate.IngressNginxServiceIR{}
					}
					svcCtx.IngressNginx.LoadBalanceAlgorithm = lbAlgorithm
					ir.Services[svcKey] = svcCtx

					notify(notifications.InfoNotification,
						fmt.Sprintf("load-balance '%s' stored in IR for service %s. Requires BackendTrafficPolicy to apply.", lbAlgorithm, svcKey.Name),
						&ing,
					)
				}
			}
		}
	}

	return errs
}

// proxySettingsConfig holds parsed proxy settings
type proxySettingsConfig struct {
	ProxyBodySize         string
	ProxyBuffering        *bool
	ProxyRequestBuffering *bool
}

// parseProxySettings extracts proxy settings from ingress annotations
func parseProxySettings(ing *networkingv1.Ingress) *proxySettingsConfig {
	annotations := ing.GetAnnotations()
	if annotations == nil {
		return nil
	}

	config := &proxySettingsConfig{}
	hasConfig := false

	// Parse proxy-body-size
	if bodySize := annotations[proxyBodySizeAnnotation]; bodySize != "" {
		config.ProxyBodySize = bodySize
		hasConfig = true
	}

	// Parse proxy-buffering
	if buffering := annotations[proxyBufferingAnnotation]; buffering != "" {
		val := parseOnOff(buffering)
		config.ProxyBuffering = &val
		hasConfig = true
	}

	// Parse proxy-request-buffering
	if reqBuffering := annotations[proxyRequestBufferingAnnotation]; reqBuffering != "" {
		val := parseOnOff(reqBuffering)
		config.ProxyRequestBuffering = &val
		hasConfig = true
	}

	if !hasConfig {
		return nil
	}

	return config
}

// parseOnOff parses "on"/"off" or "true"/"false" strings to bool
func parseOnOff(value string) bool {
	v := strings.ToLower(strings.TrimSpace(value))
	return v == "on" || v == "true" || v == "1"
}

// findHTTPRouteKey finds the HTTPRoute key for a given ingress
func findHTTPRouteKey(ir *intermediate.IR, ingressKey types.NamespacedName) types.NamespacedName {
	// The common converter creates HTTPRoutes with names based on the ingress host
	// We need to find the route that corresponds to this ingress
	for routeKey := range ir.HTTPRoutes {
		if routeKey.Namespace == ingressKey.Namespace {
			// For simplicity, return the first route in the same namespace
			// In practice, this might need more sophisticated matching
			return routeKey
		}
	}
	return types.NamespacedName{}
}

// ParseBodySize parses a body size string like "100m" or "1g" and returns bytes
func ParseBodySize(size string) (int64, error) {
	if size == "" || size == "0" {
		return 0, nil
	}

	size = strings.ToLower(strings.TrimSpace(size))
	
	var multiplier int64 = 1
	var numStr string

	switch {
	case strings.HasSuffix(size, "g"):
		multiplier = 1024 * 1024 * 1024
		numStr = strings.TrimSuffix(size, "g")
	case strings.HasSuffix(size, "m"):
		multiplier = 1024 * 1024
		numStr = strings.TrimSuffix(size, "m")
	case strings.HasSuffix(size, "k"):
		multiplier = 1024
		numStr = strings.TrimSuffix(size, "k")
	default:
		numStr = size
	}

	num, err := strconv.ParseInt(numStr, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid body size: %s", size)
	}

	return num * multiplier, nil
}
