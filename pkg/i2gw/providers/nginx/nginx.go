/*
Copyright 2025 The Kubernetes Authors.

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

package nginx

import (
	"context"
	"fmt"

	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/validation/field"

	"github.com/kubernetes-sigs/ingress2gateway/pkg/i2gw"
	"github.com/kubernetes-sigs/ingress2gateway/pkg/i2gw/intermediate"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

const Name = "nginx"

// Ingress class constants
const (
	NginxIngressClass     = "tag-ingress"
	NginxIngressClassFlag = "ingress-class"
)

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
		Name:         NginxIngressClassFlag,
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
	// Mode is either "centralized" or "per-namespace"
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
func (c GatewayConfig) GetGatewayRef(serviceNamespace string) (namespace, name string) {
	if c.IsCentralized() {
		return c.Namespace, c.Name
	}
	// Per-namespace mode: dedicated gateway namespace
	gatewayNS := fmt.Sprintf("%s-gateway", serviceNamespace)
	return gatewayNS, gatewayNS
}

type Provider struct {
	*storage
	*resourceReader
	*resourcesToIRConverter
	*gatewayResourcesConverter
	gatewayConfig GatewayConfig
}

// NewProvider constructs and returns the nginx implementation of i2gw.Provider
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
		resourceReader:            newResourceReader(conf),
		resourcesToIRConverter:    newResourcesToIRConverter(),
		gatewayResourcesConverter: newGatewayResourcesConverter(),
		gatewayConfig:             gwConfig,
	}
}

// ReadResourcesFromCluster reads resources from the Kubernetes cluster
func (p *Provider) ReadResourcesFromCluster(ctx context.Context) error {
	storage, err := p.readResourcesFromCluster(ctx)
	if err != nil {
		return err
	}
	p.storage = storage
	return nil
}

// ReadResourcesFromFile reads resources from a YAML file
func (p *Provider) ReadResourcesFromFile(_ context.Context, filename string) error {
	storage, err := p.readResourcesFromFile(filename)
	if err != nil {
		return err
	}
	p.storage = storage
	return nil
}

// ToIR converts the provider resources to intermediate representation
func (p *Provider) ToIR() (intermediate.IR, field.ErrorList) {
	return p.resourcesToIRConverter.convert(p.storage)
}

// ToGatewayResources converts the IR to Gateway API resources
func (p *Provider) ToGatewayResources(ir intermediate.IR) (i2gw.GatewayResources, field.ErrorList) {
	gatewayResources, errs := p.gatewayResourcesConverter.convert(ir)
	if len(errs) != 0 {
		return i2gw.GatewayResources{}, errs
	}
	
	// Transform Gateways based on gateway mode
	p.transformGatewaysForMode(&gatewayResources, ir)
	
	return gatewayResources, nil
}

// transformGatewaysForMode transforms the generated Gateways based on the gateway mode.
// In centralized mode (default), all routes use a single platform Gateway in istio-system.
// In per-namespace mode, each service namespace gets its own Gateway in a dedicated gateway namespace.
func (p *Provider) transformGatewaysForMode(gatewayResources *i2gw.GatewayResources, ir intermediate.IR) {
	if p.gatewayConfig.Mode == "centralized" {
		// For centralized mode, update Gateway to use the configured namespace/name
		newGateways := make(map[types.NamespacedName]gatewayv1.Gateway)
		for oldKey, gw := range gatewayResources.Gateways {
			// Create new gateway with centralized naming
			newKey := types.NamespacedName{
				Namespace: p.gatewayConfig.Namespace,
				Name:      p.gatewayConfig.Name,
			}
			gw.Namespace = p.gatewayConfig.Namespace
			gw.Name = p.gatewayConfig.Name
			// Use istio as the gateway class for Istio deployments
			istioClass := gatewayv1.ObjectName("istio")
			gw.Spec.GatewayClassName = istioClass
			newGateways[newKey] = gw
			
			// Update HTTPRoutes to reference the new gateway
			p.updateHTTPRouteParentRefs(gatewayResources, oldKey, newKey)
		}
		gatewayResources.Gateways = newGateways
		return
	}
	
	// Per-namespace mode: Create dedicated gateway namespaces
	namespaceRoutes := make(map[string][]types.NamespacedName)
	for routeKey := range gatewayResources.HTTPRoutes {
		namespaceRoutes[routeKey.Namespace] = append(namespaceRoutes[routeKey.Namespace], routeKey)
	}
	
	newGateways := make(map[types.NamespacedName]gatewayv1.Gateway)
	oldToNewGateway := make(map[types.NamespacedName]types.NamespacedName)
	
	for namespace := range namespaceRoutes {
		gwNamespace, gwName := p.gatewayConfig.GetGatewayRef(namespace)
		newKey := types.NamespacedName{
			Namespace: gwNamespace,
			Name:      gwName,
		}
		
		var templateGateway *gatewayv1.Gateway
		for oldKey, gw := range gatewayResources.Gateways {
			if templateGateway == nil {
				gwCopy := gw.DeepCopy()
				templateGateway = gwCopy
				oldToNewGateway[oldKey] = newKey
			} else {
				templateGateway.Spec.Listeners = append(templateGateway.Spec.Listeners, gw.Spec.Listeners...)
				oldToNewGateway[oldKey] = newKey
			}
		}
		
		if templateGateway != nil {
			templateGateway.Namespace = gwNamespace
			templateGateway.Name = gwName
			istioClass := gatewayv1.ObjectName("istio")
			templateGateway.Spec.GatewayClassName = istioClass
			newGateways[newKey] = *templateGateway
		}
	}
	
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
