# Ingress Nginx Provider

The project supports translating ingress-nginx specific annotations.

## Provider-Specific Flags

| Flag | Default | Description |
|------|---------|-------------|
| `--ingress-nginx-ingress-class` | `nginx` | The name of the Ingress class to select |
| `--ingress-nginx-gateway-mode` | `per-namespace` | Gateway deployment mode: `per-namespace` or `centralized` |
| `--ingress-nginx-gateway-namespace` | `istio-system` | Namespace for centralized gateway (only used when mode=centralized) |
| `--ingress-nginx-gateway-name` | `platform-gateway` | Name of centralized gateway (only used when mode=centralized) |

## Gateway Deployment Modes

### Per-Namespace Mode (Default)

Creates a dedicated gateway namespace for each service namespace. This provides clear ownership boundaries between platform and service teams.

```bash
# Convert ingresses from fhir namespace
ingress2gateway print --providers ingress-nginx --namespace fhir
```

**Result:**
```
fhir-gateway/                    # NEW - Platform team owned
├── Gateway: fhir-gateway
├── ReferenceGrant: allow-routes-from-fhir
└── EnvoyFilters (if applicable)

fhir/                            # Service team owned (unchanged)
├── HTTPRoute                    # Converted from Ingress
├── BackendTLSPolicy            # For backend-protocol: HTTPS
├── Services
└── Deployments
```

| Service Namespace | Gateway Namespace | Gateway Name |
|-------------------|-------------------|--------------|
| `fhir` | `fhir-gateway` | `fhir-gateway` |
| `dicom` | `dicom-gateway` | `dicom-gateway` |
| `hms` | `hms-gateway` | `hms-gateway` |

### Centralized Mode

Creates a single shared gateway in a platform namespace (e.g., `istio-system`).

```bash
ingress2gateway print --providers ingress-nginx \
  --ingress-nginx-gateway-mode=centralized \
  --ingress-nginx-gateway-namespace=istio-system \
  --ingress-nginx-gateway-name=platform-gateway \
  --all-namespaces
```

**Result:**
```
istio-system/                    # Platform team owned
├── Gateway: platform-gateway
├── ReferenceGrant: allow-routes-from-fhir
├── ReferenceGrant: allow-routes-from-dicom
└── EnvoyFilters

fhir/
├── HTTPRoute → parentRefs: istio-system/platform-gateway
└── ...

dicom/
├── HTTPRoute → parentRefs: istio-system/platform-gateway
└── ...
```

## Istio-Specific Features

When using Istio as the Gateway API implementation, the provider generates additional resources:

### EnvoyFilters

For annotations that require Envoy-level configuration, Istio EnvoyFilters are generated:

| Annotation | EnvoyFilter Type | Description |
|------------|-----------------|-------------|
| `nginx.ingress.kubernetes.io/limit-rps` | `local_ratelimit` | Request rate limiting |
| `nginx.ingress.kubernetes.io/proxy-body-size` | `buffer` | Max request body size |
| `nginx.ingress.kubernetes.io/proxy-buffering: "off"` | `circuit_breakers` | Disable buffering |

EnvoyFilters are placed in the gateway namespace and target the appropriate Gateway using `targetRefs`.

### ReferenceGrants

ReferenceGrants are automatically generated to allow HTTPRoutes in service namespaces to reference Gateways in gateway namespaces. This is required by Gateway API for cross-namespace references.

## Supported Annotations

### Canary Deployments

- `nginx.ingress.kubernetes.io/canary`: If set to true will enable weighting backends.
- `nginx.ingress.kubernetes.io/canary-by-header`: If specified, the value of this annotation is the header name that will be added as a HTTPHeaderMatch for the routes generated from this Ingress. If not specified, no HTTPHeaderMatch will be generated.
- `nginx.ingress.kubernetes.io/canary-by-header-value`: If specified, the value of this annotation is the header value to perform an HeaderMatchExact match on in the generated HTTPHeaderMatch.
- `nginx.ingress.kubernetes.io/canary-by-header-pattern`: If specified, this is the pattern to match against for the HTTPHeaderMatch, which will be of type HeaderMatchRegularExpression.
- `nginx.ingress.kubernetes.io/canary-weight`: If specified and non-zero, this value will be applied as the weight of the backends for the routes generated from this Ingress resource.
- `nginx.ingress.kubernetes.io/canary-weight-total`: The total weight for canary calculations (default 100).

### Backend Protocol & TLS (mTLS to Backend)

These annotations control how the gateway connects to backend services:

| Annotation | Gateway API Equivalent | Description |
|------------|----------------------|-------------|
| `nginx.ingress.kubernetes.io/backend-protocol` | BackendTLSPolicy | Protocol to use: HTTP, HTTPS, GRPC, GRPCS |
| `nginx.ingress.kubernetes.io/proxy-ssl-secret` | BackendTLSPolicy.caCertificateRefs | Secret containing client certificate for mTLS |
| `nginx.ingress.kubernetes.io/proxy-ssl-verify` | BackendTLSPolicy | Whether to verify backend certificate (on/off) |
| `nginx.ingress.kubernetes.io/proxy-ssl-name` | BackendTLSPolicy.validation.hostname | SNI hostname for backend TLS |
| `nginx.ingress.kubernetes.io/proxy-ssl-protocols` | BackendTLSPolicy (via options) | TLS protocols (e.g., TLSv1.3) |
| `nginx.ingress.kubernetes.io/proxy-ssl-ciphers` | BackendTLSPolicy (via options) | Cipher suites |

**Example conversion:**
```yaml
# NGINX Ingress annotation
nginx.ingress.kubernetes.io/backend-protocol: HTTPS
nginx.ingress.kubernetes.io/proxy-ssl-secret: nginx/client-cert
nginx.ingress.kubernetes.io/proxy-ssl-verify: "on"
nginx.ingress.kubernetes.io/proxy-ssl-name: backend.internal

# Converts to Gateway API BackendTLSPolicy
apiVersion: gateway.networking.k8s.io/v1
kind: BackendTLSPolicy
metadata:
  name: myservice-backend-tls
spec:
  targetRefs:
    - group: ""
      kind: Service
      name: myservice
  validation:
    hostname: backend.internal
    caCertificateRefs:
      - kind: ConfigMap
        name: client-cert-ca
```

### Timeouts

These annotations configure request and connection timeouts:

| Annotation | Gateway API Equivalent | Description |
|------------|----------------------|-------------|
| `nginx.ingress.kubernetes.io/proxy-connect-timeout` | HTTPRoute.timeouts.backendRequest | Connection timeout (seconds) |
| `nginx.ingress.kubernetes.io/proxy-read-timeout` | HTTPRoute.timeouts.request | Read timeout (seconds) |
| `nginx.ingress.kubernetes.io/proxy-send-timeout` | HTTPRoute.timeouts.request | Send timeout (seconds) |

**Example conversion:**
```yaml
# NGINX Ingress annotation
nginx.ingress.kubernetes.io/proxy-read-timeout: "7200"
nginx.ingress.kubernetes.io/proxy-send-timeout: "7200"

# Converts to HTTPRoute timeouts
apiVersion: gateway.networking.k8s.io/v1
kind: HTTPRoute
spec:
  rules:
    - backendRefs:
        - name: myservice
      timeouts:
        request: 7200s
```

### SSL Redirect

| Annotation | Gateway API Equivalent | Description |
|------------|----------------------|-------------|
| `nginx.ingress.kubernetes.io/ssl-redirect` | HTTPRoute RequestRedirect filter | Redirect HTTP to HTTPS |
| `nginx.ingress.kubernetes.io/force-ssl-redirect` | HTTPRoute RequestRedirect filter | Force redirect even without TLS |

**Note:** In Gateway API, HTTP to HTTPS redirect requires a separate HTTPRoute attached to an HTTP listener with a RequestRedirect filter.

### Proxy Settings (→ BackendTrafficPolicy)

These annotations are stored in the IR for conversion to implementation-specific BackendTrafficPolicy:

| Annotation | Description | IR Field |
|------------|-------------|----------|
| `nginx.ingress.kubernetes.io/proxy-body-size` | Max request body size (e.g., "100m", "1g") | `ProxyBodySize` |
| `nginx.ingress.kubernetes.io/proxy-buffering` | Enable/disable proxy buffering (on/off) | `ProxyBuffering` |
| `nginx.ingress.kubernetes.io/proxy-request-buffering` | Enable/disable request buffering (on/off) | `ProxyRequestBuffering` |
| `nginx.ingress.kubernetes.io/load-balance` | Load balancing algorithm (ewma, round_robin) | `LoadBalanceAlgorithm` |

**Note:** These require implementation-specific BackendTrafficPolicy (e.g., Envoy Gateway) to apply.

### Rate Limiting (→ BackendTrafficPolicy)

These annotations are stored in the IR for conversion to implementation-specific BackendTrafficPolicy.rateLimit:

| Annotation | Description | IR Field |
|------------|-------------|----------|
| `nginx.ingress.kubernetes.io/limit-rps` | Rate limit in requests per second | `RateLimitRPS` |
| `nginx.ingress.kubernetes.io/limit-rpm` | Rate limit in requests per minute | Converted to `RateLimitRPS` |
| `nginx.ingress.kubernetes.io/limit-connections` | Connection limit | `Connections` |
| `nginx.ingress.kubernetes.io/limit-burst-multiplier` | Burst multiplier | `RateLimitBurst` |
| `nginx.ingress.kubernetes.io/limit-req-zone` | Advanced rate limit zone config | Parsed for rate |

**Example IR output:**
```yaml
# Stored in IR for BackendTrafficPolicy generation
httpRoute:
  providerSpecificIR:
    ingressNginx:
      rateLimitRPS: 1000
      rateLimitBurst: 3000
```

### Client Certificate Authentication (→ SecurityPolicy)

These annotations are stored in the IR for conversion to implementation-specific SecurityPolicy.clientValidation:

| Annotation | Description | IR Field |
|------------|-------------|----------|
| `nginx.ingress.kubernetes.io/auth-tls-secret` | CA certificate secret for client verification | `ClientCertAuth.Secret` |
| `nginx.ingress.kubernetes.io/auth-tls-verify-client` | Verify mode: on, off, optional, optional_no_ca | `ClientCertAuth.VerifyClient` |
| `nginx.ingress.kubernetes.io/auth-tls-verify-depth` | Max certificate chain depth | `ClientCertAuth.VerifyDepth` |
| `nginx.ingress.kubernetes.io/auth-tls-error-page` | Redirect URL on verification failure | `ClientCertAuth.ErrorPage` |
| `nginx.ingress.kubernetes.io/auth-tls-pass-certificate-to-upstream` | Pass client cert to backend | `ClientCertAuth.PassCertToUpstream` |

**Example conversion target (Envoy Gateway):**
```yaml
apiVersion: gateway.envoyproxy.io/v1alpha1
kind: SecurityPolicy
metadata:
  name: mtls-client-auth
spec:
  targetRefs:
    - group: gateway.networking.k8s.io
      kind: HTTPRoute
      name: myroute
  tls:
    clientValidation:
      caCertificateRefs:
        - name: ca-secret
          kind: Secret
      optional: true  # when verify-client is "optional"
```

### External Authentication (→ SecurityPolicy)

These annotations are stored in the IR for conversion to implementation-specific SecurityPolicy.extAuth:

| Annotation | Description | IR Field |
|------------|-------------|----------|
| `nginx.ingress.kubernetes.io/auth-url` | External auth service URL | `ExternalAuth.URL` |
| `nginx.ingress.kubernetes.io/auth-method` | HTTP method (GET/POST) | `ExternalAuth.Method` |
| `nginx.ingress.kubernetes.io/auth-signin` | Sign-in redirect URL | `ExternalAuth.SigninURL` |
| `nginx.ingress.kubernetes.io/auth-response-headers` | Headers to copy from auth response | `ExternalAuth.ResponseHeaders` |
| `nginx.ingress.kubernetes.io/auth-request-redirect` | Post-auth redirect URL | `ExternalAuth.RequestRedirect` |
| `nginx.ingress.kubernetes.io/auth-cache-key` | Cache key for auth responses | `ExternalAuth.CacheKey` |
| `nginx.ingress.kubernetes.io/auth-cache-duration` | Cache duration | `ExternalAuth.CacheDuration` |

**Example conversion target (Envoy Gateway):**
```yaml
apiVersion: gateway.envoyproxy.io/v1alpha1
kind: SecurityPolicy
metadata:
  name: ext-auth
spec:
  targetRefs:
    - group: gateway.networking.k8s.io
      kind: HTTPRoute
      name: myroute
  extAuth:
    http:
      backendRef:
        name: auth-service
        port: 80
      headersToBackend:
        - Authorization
```

## Annotations Requiring App-Level Changes

The following annotations **cannot be translated** to Gateway API and require changes in your application code. The tool will emit warnings when these are detected:

| Annotation | Issue | Recommended Action |
|------------|-------|-------------------|
| `nginx.ingress.kubernetes.io/server-snippet` | Custom NGINX config has no Gateway API equivalent | Move logic to application middleware |
| `nginx.ingress.kubernetes.io/configuration-snippet` | Custom location config | Move to application or use EnvoyPatchPolicy |
| `nginx.ingress.kubernetes.io/use-regex` | Regex path matching is NOT GA in Gateway API | Refactor API paths to use prefix matching, or use experimental channel |
| `nginx.ingress.kubernetes.io/rewrite-target` (with `$1`, `$2`) | URLRewrite filter doesn't support capture groups | Refactor application to accept original paths |

### Meshless Istio Warnings

When using Istio without sidecars (meshless), these annotations have limitations:

| Annotation | Limitation | Options |
|------------|-----------|---------|
| `nginx.ingress.kubernetes.io/auth-url` | External auth applies to entire Gateway, not per-route | Implement auth in application, or enable Istio sidecars |
| `nginx.ingress.kubernetes.io/auth-tls-secret` | Client cert validation applies to entire listener | Use separate listeners per customer, or validate in application |

## Annotations Not Yet Supported

The following annotations require manual migration:

| Annotation | Gateway API Alternative | Notes |
|------------|------------------------|-------|
| `nginx.ingress.kubernetes.io/rewrite-target` | HTTPRoute URLRewrite filter | Regex capture groups not supported |
| `nginx.ingress.kubernetes.io/configuration-snippet` | EnvoyPatchPolicy / ExtensionRef | Implementation-specific |
| `nginx.ingress.kubernetes.io/server-snippet` | N/A | Move logic to application |
| `nginx.ingress.kubernetes.io/use-regex` | Experimental PathType | Not GA in Gateway API |
| `nginx.ingress.kubernetes.io/affinity` | BackendTrafficPolicy.sessionPersistence | Implementation-specific |

If you are reliant on any annotations not listed above, please open an issue. In the meantime you'll need to manually find a Gateway API equivalent.

