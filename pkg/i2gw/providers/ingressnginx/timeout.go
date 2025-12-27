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
	"github.com/kubernetes-sigs/ingress2gateway/pkg/i2gw/providers/common"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/validation/field"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

const (
	// Timeout annotations
	proxyConnectTimeoutAnnotation = "nginx.ingress.kubernetes.io/proxy-connect-timeout"
	proxyReadTimeoutAnnotation    = "nginx.ingress.kubernetes.io/proxy-read-timeout"
	proxySendTimeoutAnnotation    = "nginx.ingress.kubernetes.io/proxy-send-timeout"
)

// timeoutConfig holds the parsed timeout configuration from an Ingress
type timeoutConfig struct {
	connectTimeout int // in seconds
	readTimeout    int // in seconds
	sendTimeout    int // in seconds
}

// parseTimeoutConfig extracts timeout configuration from an Ingress
func parseTimeoutConfig(ingress *networkingv1.Ingress) (*timeoutConfig, error) {
	config := &timeoutConfig{}
	hasTimeout := false

	if val := ingress.Annotations[proxyConnectTimeoutAnnotation]; val != "" {
		timeout, err := strconv.Atoi(val)
		if err != nil {
			return nil, fmt.Errorf("invalid proxy-connect-timeout %q: %w", val, err)
		}
		config.connectTimeout = timeout
		hasTimeout = true
	}

	if val := ingress.Annotations[proxyReadTimeoutAnnotation]; val != "" {
		timeout, err := strconv.Atoi(val)
		if err != nil {
			return nil, fmt.Errorf("invalid proxy-read-timeout %q: %w", val, err)
		}
		config.readTimeout = timeout
		hasTimeout = true
	}

	if val := ingress.Annotations[proxySendTimeoutAnnotation]; val != "" {
		timeout, err := strconv.Atoi(val)
		if err != nil {
			return nil, fmt.Errorf("invalid proxy-send-timeout %q: %w", val, err)
		}
		config.sendTimeout = timeout
		hasTimeout = true
	}

	if !hasTimeout {
		return nil, nil
	}

	return config, nil
}

// timeoutFeature processes proxy-*-timeout annotations and sets HTTPRoute timeouts
func timeoutFeature(ingresses []networkingv1.Ingress, _ map[types.NamespacedName]map[string]int32, ir *intermediate.IR) field.ErrorList {
	var errList field.ErrorList

	// Build a map of ingress to timeout config
	ingressTimeouts := make(map[types.NamespacedName]*timeoutConfig)
	for _, ingress := range ingresses {
		config, err := parseTimeoutConfig(&ingress)
		if err != nil {
			errList = append(errList, field.Invalid(
				field.NewPath("ingress", ingress.Namespace, ingress.Name, "metadata", "annotations"),
				ingress.Annotations,
				err.Error(),
			))
			continue
		}
		if config != nil {
			key := types.NamespacedName{Namespace: ingress.Namespace, Name: ingress.Name}
			ingressTimeouts[key] = config
		}
	}

	if len(ingressTimeouts) == 0 {
		return errList
	}

	// Apply timeouts to HTTPRoutes
	ruleGroups := common.GetRuleGroups(ingresses)
	for _, rg := range ruleGroups {
		routeKey := types.NamespacedName{Namespace: rg.Namespace, Name: common.RouteName(rg.Name, rg.Host)}
		httpRouteContext, ok := ir.HTTPRoutes[routeKey]
		if !ok {
			continue
		}

		// Find the timeout config for this route (from any contributing ingress)
		var timeoutCfg *timeoutConfig
		for _, ingress := range ingresses {
			ingressKey := types.NamespacedName{Namespace: ingress.Namespace, Name: ingress.Name}
			if cfg, exists := ingressTimeouts[ingressKey]; exists {
				// Check if this ingress contributes to this route
				if matchesRoute(&ingress, rg.Host) {
					timeoutCfg = cfg
					break
				}
			}
		}

		if timeoutCfg == nil {
			continue
		}

		// Apply timeouts to all rules in this route
		for i := range httpRouteContext.HTTPRoute.Spec.Rules {
			rule := &httpRouteContext.HTTPRoute.Spec.Rules[i]

			// Set request timeout (uses the larger of read/send timeout)
			requestTimeout := timeoutCfg.readTimeout
			if timeoutCfg.sendTimeout > requestTimeout {
				requestTimeout = timeoutCfg.sendTimeout
			}

			if requestTimeout > 0 {
				if rule.Timeouts == nil {
					rule.Timeouts = &gatewayv1.HTTPRouteTimeouts{}
				}
				// Gateway API uses Duration format (e.g., "7200s")
				timeoutDuration := gatewayv1.Duration(fmt.Sprintf("%ds", requestTimeout))
				rule.Timeouts.Request = &timeoutDuration
			}

			// Set backend request timeout (connect timeout maps more closely to this)
			if timeoutCfg.connectTimeout > 0 {
				if rule.Timeouts == nil {
					rule.Timeouts = &gatewayv1.HTTPRouteTimeouts{}
				}
				backendTimeout := gatewayv1.Duration(fmt.Sprintf("%ds", timeoutCfg.connectTimeout))
				rule.Timeouts.BackendRequest = &backendTimeout
			}
		}

		// Update the route in IR
		ir.HTTPRoutes[routeKey] = httpRouteContext

		notify(notifications.InfoNotification,
			fmt.Sprintf("applied timeout configuration to HTTPRoute %s/%s (request: %ds, connect: %ds)",
				routeKey.Namespace, routeKey.Name, timeoutCfg.readTimeout, timeoutCfg.connectTimeout),
			&httpRouteContext.HTTPRoute)
	}

	return errList
}

// matchesRoute checks if an ingress contributes to a route with the given host
func matchesRoute(ingress *networkingv1.Ingress, host string) bool {
	for _, rule := range ingress.Spec.Rules {
		if rule.Host == host {
			return true
		}
	}
	// Also match if host is empty (default backend case)
	if host == "" && ingress.Spec.DefaultBackend != nil {
		return true
	}
	return false
}
