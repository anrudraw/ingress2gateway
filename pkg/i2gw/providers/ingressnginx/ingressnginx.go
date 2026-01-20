/*
Copyright 2023 The Kubernetes Authors.

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
	"context"
	"fmt"

	"github.com/kubernetes-sigs/ingress2gateway/pkg/i2gw"
	"github.com/kubernetes-sigs/ingress2gateway/pkg/i2gw/intermediate"
	"github.com/kubernetes-sigs/ingress2gateway/pkg/i2gw/notifications"
	"github.com/kubernetes-sigs/ingress2gateway/pkg/i2gw/providers/common"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/validation/field"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

// The Name of the provider.
const Name = "ingress-nginx"
const NginxIngressClass = "tag-ingress"
const NginxIngressClassFlag = "ingress-class"

// Gateway mode flags
const (
	// GatewayModeFlag specifies the gateway deployment mode
	// Options: "centralized" (default) or "per-namespace"
	GatewayModeFlag = "gateway-mode"
	
	// GatewayNamespaceFlag specifies the namespace for centralized gateway
	// Default: "istio-system"
	GatewayNamespaceFlag = "gateway-namespace"
	
	// GatewayNameFlag specifies the name of the centralized gateway
	// Default: "platform-gateway"
	GatewayNameFlag = "gateway-name"
	
	// Default values - Centralized gateway is the default
	DefaultGatewayMode      = "centralized"
	DefaultGatewayNamespace = "istio-system"
	DefaultGatewayName      = "platform-gateway"
)

func init() {
	i2gw.ProviderConstructorByName[Name] = NewProvider
	i2gw.RegisterProviderSpecificFlag(Name, i2gw.ProviderSpecificFlag{
		Name:         "ingress-class",
		Description:  "The name of the ingress class to select. Defaults to 'tag-ingress'",
		DefaultValue: NginxIngressClass,
	})
	i2gw.RegisterProviderSpecificFlag(Name, i2gw.ProviderSpecificFlag{
		Name:         GatewayModeFlag,
		Description:  "Gateway deployment mode: 'centralized' (single platform Gateway in istio-system, DEFAULT) or 'per-namespace' (each namespace gets its own Gateway)",
		DefaultValue: DefaultGatewayMode,
	})
	i2gw.RegisterProviderSpecificFlag(Name, i2gw.ProviderSpecificFlag{
		Name:         GatewayNamespaceFlag,
		Description:  "Namespace for centralized gateway (only used when gateway-mode=centralized)",
		DefaultValue: DefaultGatewayNamespace,
	})
	i2gw.RegisterProviderSpecificFlag(Name, i2gw.ProviderSpecificFlag{
		Name:         GatewayNameFlag,
		Description:  "Name of the centralized gateway (only used when gateway-mode=centralized)",
		DefaultValue: DefaultGatewayName,
	})
}

// GatewayConfig holds gateway deployment configuration
type GatewayConfig struct {
	// Mode is either "per-namespace" or "centralized"
	Mode string
	// Namespace is the gateway namespace (for centralized mode, e.g., istio-system)
	Namespace string
	// Name is the gateway name (for centralized mode, e.g., platform-gateway)
	Name string
}

// IsCentralized returns true if using centralized gateway mode
func (c GatewayConfig) IsCentralized() bool {
	return c.Mode == "centralized"
}

// GetGatewayRef returns the gateway reference for a given service namespace
// Gateway namespace patterns:
// - Centralized: single "platform-gateway" in istio-system (or configured namespace)
// - Per-namespace: dedicated "<service>-gateway" namespace with Gateway named "<service>-gateway"
//   Example: fhir service → fhir-gateway/fhir-gateway
func (c GatewayConfig) GetGatewayRef(serviceNamespace string) (namespace, name string) {
	if c.IsCentralized() {
		return c.Namespace, c.Name
	}
	// Per-namespace mode: dedicated gateway namespace
	// Namespace: <service>-gateway, Gateway name: <service>-gateway
	gatewayNS := fmt.Sprintf("%s-gateway", serviceNamespace)
	return gatewayNS, gatewayNS
}

// Provider implements the i2gw.Provider interface.
type Provider struct {
	storage                *storage
	resourceReader         *resourceReader
	resourcesToIRConverter *resourcesToIRConverter
	gatewayConfig          GatewayConfig
}

// NewProvider constructs and returns the ingress-nginx implementation of i2gw.Provider.
func NewProvider(conf *i2gw.ProviderConf) i2gw.Provider {
	gwConfig := GatewayConfig{
		Mode:      DefaultGatewayMode,
		Namespace: DefaultGatewayNamespace,
		Name:      DefaultGatewayName,
	}
	
	// Read provider-specific flags
	if conf != nil && conf.ProviderSpecificFlags != nil {
		if flags, ok := conf.ProviderSpecificFlags[Name]; ok {
			if mode, ok := flags[GatewayModeFlag]; ok && mode != "" {
				gwConfig.Mode = mode
			}
			if ns, ok := flags[GatewayNamespaceFlag]; ok && ns != "" {
				gwConfig.Namespace = ns
			}
			if name, ok := flags[GatewayNameFlag]; ok && name != "" {
				gwConfig.Name = name
			}
		}
	}
	
	return &Provider{
		storage:                newResourcesStorage(),
		resourceReader:         newResourceReader(conf),
		resourcesToIRConverter: newResourcesToIRConverter(),
		gatewayConfig:          gwConfig,
	}
}

// ToIR converts stored Ingress-Nginx API entities to intermediate.IR
// including the ingress-nginx specific features.
func (p *Provider) ToIR() (intermediate.IR, field.ErrorList) {
	return p.resourcesToIRConverter.convert(p.storage)
}

func (p *Provider) ToGatewayResources(ir intermediate.IR) (i2gw.GatewayResources, field.ErrorList) {
	gatewayResources, errs := common.ToGatewayResources(ir)
	if len(errs) != 0 {
		return i2gw.GatewayResources{}, errs
	}
	
	// Transform Gateways based on gateway mode
	p.transformGatewaysForMode(&gatewayResources, ir)
	
	// Generate SSL redirect HTTPRoutes
	buildSSLRedirectRoutes(ir, &gatewayResources, p.gatewayConfig)
	
	// Build Istio EnvoyFilters for implementation-specific features
	// buildIstioEnvoyFilters(ir, &gatewayResources, p.gatewayConfig)
	
	// Generate ReferenceGrants for cross-namespace routing
	// Always needed since Gateways are in istio-system and HTTPRoutes are in service namespaces
	buildCrossNamespaceReferenceGrants(ir, &gatewayResources, p.gatewayConfig)
	
	// Emit centralized mode warnings for auth annotations
	p.emitCentralizedModeWarnings(ir)
	
	return gatewayResources, nil
}

// emitCentralizedModeWarnings emits warnings for auth annotations when using centralized gateway mode
// In centralized mode, auth EnvoyFilters apply to the shared platform Gateway affecting ALL services
func (p *Provider) emitCentralizedModeWarnings(ir intermediate.IR) {
	if !p.gatewayConfig.IsCentralized() {
		return // No warnings needed for per-namespace mode
	}
	
	for _, routeCtx := range ir.HTTPRoutes {
		if routeCtx.ProviderSpecificIR.IngressNginx == nil {
			continue
		}
		
		nginxIR := routeCtx.ProviderSpecificIR.IngressNginx
		
		// Warn about external auth in centralized mode
		if nginxIR.ExternalAuth != nil && nginxIR.ExternalAuth.URL != "" {
			notify(notifications.WarningNotification,
				"CENTRALIZED MODE WARNING - auth-url: The ext_authz EnvoyFilter targets the shared platform Gateway "+
					"and will apply to ALL services, not just this one. Consider: "+
					"1) Switch to per-namespace mode (--ingress-nginx-gateway-mode=per-namespace) for namespace isolation, or "+
					"2) Implement auth at the application level for fine-grained control.",
				&routeCtx.HTTPRoute,
			)
		}
		
		// Warn about client cert auth in centralized mode
		if nginxIR.ClientCertAuth != nil && nginxIR.ClientCertAuth.Secret != "" {
			notify(notifications.WarningNotification,
				"CENTRALIZED MODE WARNING - auth-tls-secret: Client cert validation on the shared platform Gateway "+
					"applies to ALL services on that listener. Consider: "+
					"1) Switch to per-namespace mode for dedicated Gateway listeners, or "+
					"2) Pass client cert to app and validate there (auth-tls-pass-certificate-to-upstream).",
				&routeCtx.HTTPRoute,
			)
		}
	}
}

// transformGatewaysForMode transforms the generated Gateways based on the gateway mode.
// In per-namespace mode, each service namespace gets its own Gateway in a dedicated gateway namespace.
// In centralized mode, all routes use a single pre-provisioned platform Gateway (no Gateway generated).
func (p *Provider) transformGatewaysForMode(gatewayResources *i2gw.GatewayResources, ir intermediate.IR) {
	if p.gatewayConfig.Mode == "centralized" {
		// For centralized mode, the platform-gateway is pre-provisioned by the platform team.
		// We only need to update HTTPRoutes to reference it - do NOT generate Gateway resources.
		centralizedGatewayKey := types.NamespacedName{
			Namespace: p.gatewayConfig.Namespace,
			Name:      p.gatewayConfig.Name,
		}
		
		// Update all HTTPRoutes to reference the centralized gateway
		for oldKey := range gatewayResources.Gateways {
			p.updateHTTPRouteParentRefs(gatewayResources, oldKey, centralizedGatewayKey)
		}
		
		// Clear the Gateways map - centralized gateway is pre-provisioned, not generated
		gatewayResources.Gateways = make(map[types.NamespacedName]gatewayv1.Gateway)
		return
	}
	
	// Per-namespace mode (default): Create dedicated gateway namespaces
	// Group HTTPRoutes by their source namespace
	namespaceRoutes := make(map[string][]types.NamespacedName)
	for routeKey := range gatewayResources.HTTPRoutes {
		namespaceRoutes[routeKey.Namespace] = append(namespaceRoutes[routeKey.Namespace], routeKey)
	}
	
	// Create a new Gateway for each namespace that has routes
	newGateways := make(map[types.NamespacedName]gatewayv1.Gateway)
	oldToNewGateway := make(map[types.NamespacedName]types.NamespacedName)
	
	for namespace := range namespaceRoutes {
		// Get the gateway reference for this namespace
		gwNamespace, gwName := p.gatewayConfig.GetGatewayRef(namespace)
		newKey := types.NamespacedName{
			Namespace: gwNamespace,
			Name:      gwName,
		}
		
		// Find an existing gateway to use as template (take listeners from all gateways)
		var templateGateway *gatewayv1.Gateway
		for oldKey, gw := range gatewayResources.Gateways {
			if templateGateway == nil {
				gwCopy := gw.DeepCopy()
				templateGateway = gwCopy
				oldToNewGateway[oldKey] = newKey
			} else {
				// Merge listeners from other gateways
				templateGateway.Spec.Listeners = append(templateGateway.Spec.Listeners, gw.Spec.Listeners...)
				oldToNewGateway[oldKey] = newKey
			}
		}
		
		if templateGateway != nil {
			// Update the gateway with per-namespace naming
			templateGateway.Namespace = gwNamespace
			templateGateway.Name = gwName
			// Use istio as the gateway class for Istio deployments
			istioClass := gatewayv1.ObjectName("istio")
			templateGateway.Spec.GatewayClassName = istioClass
			newGateways[newKey] = *templateGateway
		}
	}
	
	// Update HTTPRoutes to reference the new gateways
	for oldKey, newKey := range oldToNewGateway {
		p.updateHTTPRouteParentRefs(gatewayResources, oldKey, newKey)
	}
	
	gatewayResources.Gateways = newGateways
}

// updateHTTPRouteParentRefs updates HTTPRoute parentRefs from old gateway to new gateway
func (p *Provider) updateHTTPRouteParentRefs(gatewayResources *i2gw.GatewayResources, oldGw, newGw types.NamespacedName) {
	for routeKey, route := range gatewayResources.HTTPRoutes {
		updated := false
		for i, parentRef := range route.Spec.ParentRefs {
			parentNs := route.Namespace
			if parentRef.Namespace != nil {
				parentNs = string(*parentRef.Namespace)
			}
			if parentNs == oldGw.Namespace && string(parentRef.Name) == oldGw.Name {
				newNs := gatewayv1.Namespace(newGw.Namespace)
				route.Spec.ParentRefs[i].Namespace = &newNs
				route.Spec.ParentRefs[i].Name = gatewayv1.ObjectName(newGw.Name)
				updated = true
			}
		}
		if updated {
			gatewayResources.HTTPRoutes[routeKey] = route
		}
	}
}

func (p *Provider) ReadResourcesFromCluster(ctx context.Context) error {
	storage, err := p.resourceReader.readResourcesFromCluster(ctx)
	if err != nil {
		return fmt.Errorf("failed to read resources from cluster: %w", err)
	}

	p.storage = storage
	return nil
}

func (p *Provider) ReadResourcesFromFile(_ context.Context, filename string) error {
	storage, err := p.resourceReader.readResourcesFromFile(filename)
	if err != nil {
		return fmt.Errorf("failed to read resources from file: %w", err)
	}

	p.storage = storage
	return nil
}
