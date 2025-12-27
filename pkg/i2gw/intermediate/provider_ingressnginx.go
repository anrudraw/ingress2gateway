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

	// ClientCertAuth holds client certificate authentication configuration
	ClientCertAuth *ClientCertAuthConfig

	// ExternalAuth holds external authentication configuration
	ExternalAuth *ExternalAuthConfig
}

// ClientCertAuthConfig holds client certificate authentication settings
type ClientCertAuthConfig struct {
	// Secret is the name of the secret containing the CA certificate
	Secret string

	// VerifyClient specifies how client certificates are verified (on, off, optional, optional_no_ca)
	VerifyClient string

	// VerifyDepth is the maximum depth of certificate chain verification
	VerifyDepth int

	// ErrorPage is the URL to redirect to when client cert verification fails
	ErrorPage string

	// PassCertToUpstream indicates if client certificate should be passed to backend
	PassCertToUpstream bool
}

// ExternalAuthConfig holds external authentication settings
type ExternalAuthConfig struct {
	// URL is the external authentication service URL
	URL string

	// Method is the HTTP method to use for auth requests
	Method string

	// SigninURL is the URL to redirect unauthenticated requests to
	SigninURL string

	// ResponseHeaders are headers to copy from auth response to request
	ResponseHeaders []string

	// RequestRedirect is the URL to redirect after auth
	RequestRedirect string

	// CacheKey is the cache key for auth responses
	CacheKey string

	// CacheDuration is how long to cache auth responses
	CacheDuration string
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
