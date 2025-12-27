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
	"github.com/kubernetes-sigs/ingress2gateway/pkg/i2gw/providers/common"
	"k8s.io/apimachinery/pkg/util/validation/field"
)

// The Name of the provider.
const Name = "ingress-nginx"
const NginxIngressClass = "nginx"
const NginxIngressClassFlag = "ingress-class"

// Gateway mode flags
const (
	// GatewayModeFlag specifies the gateway deployment mode
	// Options: "per-namespace" (default) or "centralized"
	GatewayModeFlag = "gateway-mode"
	
	// GatewayNamespaceFlag specifies the namespace for centralized gateway
	// Default: "istio-system"
	GatewayNamespaceFlag = "gateway-namespace"
	
	// GatewayNameFlag specifies the name of the centralized gateway
	// Default: "platform-gateway"
	GatewayNameFlag = "gateway-name"
	
	// Default values
	DefaultGatewayMode      = "per-namespace"
	DefaultGatewayNamespace = "istio-system"
	DefaultGatewayName      = "platform-gateway"
)

func init() {
	i2gw.ProviderConstructorByName[Name] = NewProvider
	i2gw.RegisterProviderSpecificFlag(Name, i2gw.ProviderSpecificFlag{
		Name:         "ingress-class",
		Description:  "The name of the ingress class to select. Defaults to 'nginx'",
		DefaultValue: NginxIngressClass,
	})
	i2gw.RegisterProviderSpecificFlag(Name, i2gw.ProviderSpecificFlag{
		Name:         GatewayModeFlag,
		Description:  "Gateway deployment mode: 'per-namespace' (each namespace gets its own Gateway) or 'centralized' (single platform Gateway in istio-system)",
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
	
	// Build Istio EnvoyFilters for implementation-specific features
	buildIstioEnvoyFilters(ir, &gatewayResources, p.gatewayConfig)
	
	// Generate ReferenceGrants for cross-namespace routing
	// Always needed since Gateways are in istio-system and HTTPRoutes are in service namespaces
	buildCrossNamespaceReferenceGrants(ir, &gatewayResources, p.gatewayConfig)
	
	return gatewayResources, nil
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
