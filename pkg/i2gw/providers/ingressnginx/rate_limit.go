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
	"regexp"
	"strconv"

	"github.com/kubernetes-sigs/ingress2gateway/pkg/i2gw/intermediate"
	"github.com/kubernetes-sigs/ingress2gateway/pkg/i2gw/notifications"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/validation/field"
)

const (
	// Rate limiting annotations
	limitRPSAnnotation      = "nginx.ingress.kubernetes.io/limit-rps"
	limitRPMAnnotation      = "nginx.ingress.kubernetes.io/limit-rpm"
	limitConnectionsAnnotation = "nginx.ingress.kubernetes.io/limit-connections"
	limitBurstAnnotation    = "nginx.ingress.kubernetes.io/limit-burst-multiplier"
	limitReqZoneAnnotation  = "nginx.ingress.kubernetes.io/limit-req-zone"
)

// rateLimitFeature parses rate limiting annotations and stores them in the IR.
// These settings map to Gateway API BackendTrafficPolicy.rateLimit (implementation-specific).
func rateLimitFeature(ingresses []networkingv1.Ingress, _ map[types.NamespacedName]map[string]int32, ir *intermediate.IR) field.ErrorList {
	var errs field.ErrorList

	for _, ing := range ingresses {
		config, parseErrs := parseRateLimitConfig(&ing)
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

		// Set rate limiting config
		if config.RPS > 0 {
			routeCtx.ProviderSpecificIR.IngressNginx.RateLimitRPS = config.RPS
		}
		if config.Burst > 0 {
			routeCtx.ProviderSpecificIR.IngressNginx.RateLimitBurst = config.Burst
		}

		ir.HTTPRoutes[routeKey] = routeCtx

		notify(notifications.InfoNotification,
			fmt.Sprintf("Rate limiting config (RPS: %d, Burst: %d) stored in IR. Requires BackendTrafficPolicy to apply.", config.RPS, config.Burst),
			&ing,
		)
	}

	return errs
}

// rateLimitConfig holds parsed rate limiting configuration
type rateLimitConfig struct {
	RPS   int
	RPM   int
	Connections int
	Burst int
	Zone  string
}

// parseRateLimitConfig extracts rate limiting configuration from ingress annotations
func parseRateLimitConfig(ing *networkingv1.Ingress) (*rateLimitConfig, field.ErrorList) {
	annotations := ing.GetAnnotations()
	if annotations == nil {
		return nil, nil
	}

	var errs field.ErrorList
	config := &rateLimitConfig{}
	hasConfig := false

	// Parse limit-rps
	if rps := annotations[limitRPSAnnotation]; rps != "" {
		val, err := strconv.Atoi(rps)
		if err != nil {
			errs = append(errs, field.Invalid(
				field.NewPath("metadata", "annotations", limitRPSAnnotation),
				rps,
				"invalid rate limit RPS value",
			))
		} else {
			config.RPS = val
			hasConfig = true
		}
	}

	// Parse limit-rpm (convert to RPS for consistency)
	if rpm := annotations[limitRPMAnnotation]; rpm != "" {
		val, err := strconv.Atoi(rpm)
		if err != nil {
			errs = append(errs, field.Invalid(
				field.NewPath("metadata", "annotations", limitRPMAnnotation),
				rpm,
				"invalid rate limit RPM value",
			))
		} else {
			config.RPM = val
			// Convert RPM to RPS if RPS not already set
			if config.RPS == 0 {
				config.RPS = val / 60
				if config.RPS == 0 && val > 0 {
					config.RPS = 1 // minimum 1 RPS
				}
			}
			hasConfig = true
		}
	}

	// Parse limit-connections
	if conn := annotations[limitConnectionsAnnotation]; conn != "" {
		val, err := strconv.Atoi(conn)
		if err != nil {
			errs = append(errs, field.Invalid(
				field.NewPath("metadata", "annotations", limitConnectionsAnnotation),
				conn,
				"invalid connection limit value",
			))
		} else {
			config.Connections = val
			hasConfig = true
		}
	}

	// Parse limit-burst-multiplier
	if burst := annotations[limitBurstAnnotation]; burst != "" {
		val, err := strconv.Atoi(burst)
		if err != nil {
			errs = append(errs, field.Invalid(
				field.NewPath("metadata", "annotations", limitBurstAnnotation),
				burst,
				"invalid burst multiplier value",
			))
		} else {
			config.Burst = config.RPS * val
			hasConfig = true
		}
	}

	// Parse limit-req-zone (complex format)
	if zone := annotations[limitReqZoneAnnotation]; zone != "" {
		config.Zone = zone
		hasConfig = true
		
		// Try to extract rate from zone definition
		// Format: "$binary_remote_addr zone=rate_limit:10m rate=1000r/s"
		rateConfig, parseErr := parseZoneRate(zone)
		if parseErr == nil && rateConfig.RPS > 0 {
			if config.RPS == 0 {
				config.RPS = rateConfig.RPS
			}
			if config.Burst == 0 && rateConfig.Burst > 0 {
				config.Burst = rateConfig.Burst
			}
		}
	}

	if !hasConfig {
		return nil, nil
	}

	return config, errs
}

// parseZoneRate extracts rate and burst from a limit-req-zone annotation
// Format: "$binary_remote_addr zone=rate_limit:10m rate=1000r/s"
func parseZoneRate(zone string) (*rateLimitConfig, error) {
	config := &rateLimitConfig{}

	// Extract rate using regex
	rateRegex := regexp.MustCompile(`rate=(\d+)r/s`)
	if matches := rateRegex.FindStringSubmatch(zone); len(matches) > 1 {
		rate, err := strconv.Atoi(matches[1])
		if err == nil {
			config.RPS = rate
		}
	}

	// Try per-minute rate
	rpmRegex := regexp.MustCompile(`rate=(\d+)r/m`)
	if matches := rpmRegex.FindStringSubmatch(zone); len(matches) > 1 {
		rpm, err := strconv.Atoi(matches[1])
		if err == nil {
			config.RPS = rpm / 60
			if config.RPS == 0 && rpm > 0 {
				config.RPS = 1
			}
		}
	}

	return config, nil
}
