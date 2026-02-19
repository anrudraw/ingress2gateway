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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/validation/field"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

const (
	// Backend protocol annotations
	backendProtocolAnnotation = "nginx.ingress.kubernetes.io/backend-protocol"

	// Proxy SSL annotations for mTLS to backend
	proxySSLSecretAnnotation    = "nginx.ingress.kubernetes.io/proxy-ssl-secret"
	proxySSLVerifyAnnotation    = "nginx.ingress.kubernetes.io/proxy-ssl-verify"
	proxySSLNameAnnotation      = "nginx.ingress.kubernetes.io/proxy-ssl-name"
	proxySSLProtocolsAnnotation = "nginx.ingress.kubernetes.io/proxy-ssl-protocols"
	proxySSLCiphersAnnotation   = "nginx.ingress.kubernetes.io/proxy-ssl-ciphers"
)

// backendTLSConfig holds the parsed backend TLS configuration from an Ingress
type backendTLSConfig struct {
	protocol      string // HTTPS, GRPC, GRPCS, HTTP
	sslSecret     string // namespace/secretName for client cert
	sslVerify     bool   // whether to verify backend cert
	sslName       string // SNI hostname
	sslProtocols  string // e.g., TLSv1.3
	sslCiphers    string // cipher list
	backendName   string // service name
	backendPort   int32  // service port
	namespace     string
}

// parseBackendTLSConfig extracts backend TLS configuration from an Ingress
func parseBackendTLSConfig(ingress *networkingv1.Ingress) *backendTLSConfig {
	protocol := ingress.Annotations[backendProtocolAnnotation]
	if protocol == "" {
		protocol = "HTTP" // default
	}

	// Only create config if we have HTTPS/GRPCS backend or explicit SSL settings
	if protocol != "HTTPS" && protocol != "GRPCS" {
		sslSecret := ingress.Annotations[proxySSLSecretAnnotation]
		if sslSecret == "" {
			return nil // No backend TLS needed
		}
	}

	config := &backendTLSConfig{
		protocol:     strings.ToUpper(protocol),
		sslSecret:    ingress.Annotations[proxySSLSecretAnnotation],
		sslName:      ingress.Annotations[proxySSLNameAnnotation],
		sslProtocols: ingress.Annotations[proxySSLProtocolsAnnotation],
		sslCiphers:   ingress.Annotations[proxySSLCiphersAnnotation],
		namespace:    ingress.Namespace,
	}

	// Parse ssl-verify (defaults to "off")
	verifyStr := ingress.Annotations[proxySSLVerifyAnnotation]
	config.sslVerify = verifyStr == "on" || verifyStr == "true"

	return config
}

// backendProtocolFeature processes backend-protocol and proxy-ssl-* annotations
// and creates BackendTLSPolicy resources for HTTPS/GRPCS backends
func backendProtocolFeature(ingresses []networkingv1.Ingress, servicePorts map[types.NamespacedName]map[string]int32, ir *intermediate.IR) field.ErrorList {
	var errList field.ErrorList

	// Initialize BackendTLSPolicies map if needed
	if ir.BackendTLSPolicies == nil {
		ir.BackendTLSPolicies = make(map[types.NamespacedName]gatewayv1.BackendTLSPolicy)
	}

	for _, ingress := range ingresses {
		config := parseBackendTLSConfig(&ingress)
		if config == nil {
			continue // No backend TLS needed
		}

		// Get all backend services from this ingress
		backends := extractBackendServices(&ingress)
		if len(backends) == 0 {
			continue
		}

		for _, backend := range backends {
			policyName := fmt.Sprintf("%s-backend-tls", backend.serviceName)
			policyKey := types.NamespacedName{
				Namespace: ingress.Namespace,
				Name:      policyName,
			}

			// Skip if policy already exists
			if _, exists := ir.BackendTLSPolicies[policyKey]; exists {
				continue
			}

			policy := buildBackendTLSPolicy(policyName, ingress.Namespace, backend.serviceName, config)
			if policy != nil {
				ir.BackendTLSPolicies[policyKey] = *policy

				notify(notifications.InfoNotification,
					fmt.Sprintf("created BackendTLSPolicy %s/%s for service %s (protocol: %s, verify: %v)",
						ingress.Namespace, policyName, backend.serviceName, config.protocol, config.sslVerify),
					&ingress)
			}
		}
	}

	return errList
}

type backendService struct {
	serviceName string
	servicePort int32
}

// extractBackendServices gets all backend services from an Ingress
func extractBackendServices(ingress *networkingv1.Ingress) []backendService {
	var backends []backendService
	seen := make(map[string]bool)

	// Check default backend
	if ingress.Spec.DefaultBackend != nil && ingress.Spec.DefaultBackend.Service != nil {
		svc := ingress.Spec.DefaultBackend.Service
		key := fmt.Sprintf("%s:%d", svc.Name, svc.Port.Number)
		if !seen[key] {
			backends = append(backends, backendService{
				serviceName: svc.Name,
				servicePort: svc.Port.Number,
			})
			seen[key] = true
		}
	}

	// Check rule backends
	for _, rule := range ingress.Spec.Rules {
		if rule.HTTP == nil {
			continue
		}
		for _, path := range rule.HTTP.Paths {
			if path.Backend.Service == nil {
				continue
			}
			svc := path.Backend.Service
			key := fmt.Sprintf("%s:%d", svc.Name, svc.Port.Number)
			if !seen[key] {
				backends = append(backends, backendService{
					serviceName: svc.Name,
					servicePort: svc.Port.Number,
				})
				seen[key] = true
			}
		}
	}

	return backends
}

// buildBackendTLSPolicy creates a BackendTLSPolicy for mTLS to backend
func buildBackendTLSPolicy(name, namespace, serviceName string, config *backendTLSConfig) *gatewayv1.BackendTLSPolicy {
	if config == nil {
		return nil
	}

	// Determine the hostname for TLS validation
	hostname := config.sslName
	if hostname == "" {
		// Use service name as default hostname
		hostname = serviceName
	}

	policy := &gatewayv1.BackendTLSPolicy{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "gateway.networking.k8s.io/v1",
			Kind:       "BackendTLSPolicy",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: gatewayv1.BackendTLSPolicySpec{
			TargetRefs: []gatewayv1.LocalPolicyTargetReferenceWithSectionName{
				{
					LocalPolicyTargetReference: gatewayv1.LocalPolicyTargetReference{
						Group: "",
						Kind:  "Service",
						Name:  gatewayv1.ObjectName(serviceName),
					},
				},
			},
			Validation: gatewayv1.BackendTLSPolicyValidation{
				Hostname: gatewayv1.PreciseHostname(hostname),
			},
		},
	}

	// Add CA certificate reference if proxy-ssl-secret is specified
	if config.sslSecret != "" {
		caConfigMapName := "ca-ame-nginx"
		
		policy.Spec.Validation.CACertificateRefs = []gatewayv1.LocalObjectReference{
			{
				Group: "",
				Kind:  "ConfigMap",
				Name:  gatewayv1.ObjectName(caConfigMapName),
			},
		}
	} else {
		// If no explicit CA cert, use system certs
		wellKnown := gatewayv1.WellKnownCACertificatesSystem
		policy.Spec.Validation.WellKnownCACertificates = &wellKnown
	}

	return policy
}
