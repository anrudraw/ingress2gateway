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

package intermediate

// IngressNginxGatewayIR holds ingress-nginx specific Gateway configuration
type IngressNginxGatewayIR struct {
	// EnableSSLRedirect indicates if HTTP to HTTPS redirect should be enabled
	EnableSSLRedirect bool
}

// IngressNginxHTTPRouteIR holds ingress-nginx specific HTTPRoute configuration
type IngressNginxHTTPRouteIR struct {
	// SSLRedirect indicates if this route should redirect HTTP to HTTPS
	SSLRedirect bool

	// ProxyBuffering indicates if proxy buffering is enabled
	ProxyBuffering *bool

	// ProxyRequestBuffering indicates if request buffering is enabled
	ProxyRequestBuffering *bool

	// ProxyBodySize is the max body size (e.g., "100m")
	ProxyBodySize string

	// RateLimitRPS is the rate limit in requests per second
	RateLimitRPS int

	// RateLimitBurst is the burst limit for rate limiting
	RateLimitBurst int
}

// IngressNginxServiceIR holds ingress-nginx specific Service configuration
type IngressNginxServiceIR struct {
	// BackendProtocol is the protocol to use when connecting to the backend (HTTP, HTTPS, GRPC, GRPCS)
	BackendProtocol string

	// ProxySSLSecret is the secret containing client certificate for mTLS to backend
	ProxySSLSecret string

	// ProxySSLVerify indicates if backend certificate should be verified
	ProxySSLVerify bool

	// ProxySSLName is the SNI hostname to use when connecting to the backend
	ProxySSLName string

	// ProxySSLProtocols is the list of TLS protocols to use (e.g., "TLSv1.3")
	ProxySSLProtocols string

	// ProxySSLCiphers is the list of ciphers to use
	ProxySSLCiphers string

	// LoadBalanceAlgorithm is the load balancing algorithm (e.g., "ewma", "round_robin")
	LoadBalanceAlgorithm string
}
