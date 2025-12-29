# Ingress Nginx Provider

The project supports translating ingress-nginx specific annotations to Gateway API resources for Istio (meshless/OSS).

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
# Convert ingresses from backend-service-1 namespace
ingress2gateway print --providers ingress-nginx --namespace backend-service-1
```

**Result:**
```
backend-service-1-gateway/       # Platform team owned
    Gateway: backend-service-1-gateway
    ReferenceGrant: allow-routes-from-backend-service-1
    EnvoyFilters (if applicable)

backend-service-1/               # Service team owned (unchanged)
    HTTPRoute                    # Converted from Ingress
    BackendTLSPolicy             # For backend-protocol: HTTPS
    Services
    Deployments
```

| Service Namespace | Gateway Namespace | Gateway Name |
|-------------------|-------------------|--------------|
| `backend-service-1` | `backend-service-1-gateway` | `backend-service-1-gateway` |
| `backend-service-2` | `backend-service-2-gateway` | `backend-service-2-gateway` |
| `backend-service-3` | `backend-service-3-gateway` | `backend-service-3-gateway` |

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
    Gateway: platform-gateway
    ReferenceGrant: allow-routes-from-backend-service-1
    ReferenceGrant: allow-routes-from-backend-service-2
    EnvoyFilters

backend-service-1/
    HTTPRoute (parentRefs: istio-system/platform-gateway)

backend-service-2/
    HTTPRoute (parentRefs: istio-system/platform-gateway)
```

## Istio Meshless Features

When using Istio without sidecars (meshless), the provider generates:

### EnvoyFilters

For annotations that require Envoy-level configuration, Istio EnvoyFilters are auto-generated:

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
- `nginx.ingress.kubernetes.io/canary-by-header`: Header name for HTTPHeaderMatch.
- `nginx.ingress.kubernetes.io/canary-by-header-value`: Header value for HeaderMatchExact.
- `nginx.ingress.kubernetes.io/canary-by-header-pattern`: Pattern for HeaderMatchRegularExpression.
- `nginx.ingress.kubernetes.io/canary-weight`: Weight of backends for routes.
- `nginx.ingress.kubernetes.io/canary-weight-total`: Total weight for canary calculations (default 100).

### Backend Protocol and TLS (mTLS to Backend)

These annotations control how the gateway connects to backend services:

| Annotation | Gateway API Equivalent | Description |
|------------|----------------------|-------------|
| `nginx.ingress.kubernetes.io/backend-protocol` | BackendTLSPolicy | Protocol: HTTP, HTTPS, GRPC, GRPCS |
| `nginx.ingress.kubernetes.io/proxy-ssl-secret` | BackendTLSPolicy.caCertificateRefs | Client certificate for mTLS |
| `nginx.ingress.kubernetes.io/proxy-ssl-verify` | BackendTLSPolicy | Verify backend certificate (on/off) |
| `nginx.ingress.kubernetes.io/proxy-ssl-name` | BackendTLSPolicy.validation.hostname | SNI hostname for backend TLS |

**Example conversion:**
```yaml
# NGINX Ingress annotation
nginx.ingress.kubernetes.io/backend-protocol: HTTPS
nginx.ingress.kubernetes.io/proxy-ssl-secret: nginx/client-cert
nginx.ingress.kubernetes.io/proxy-ssl-verify: "on"

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

| Annotation | Gateway API Equivalent | Description |
|------------|----------------------|-------------|
| `nginx.ingress.kubernetes.io/proxy-connect-timeout` | HTTPRoute.timeouts.backendRequest | Connection timeout (seconds) |
| `nginx.ingress.kubernetes.io/proxy-read-timeout` | HTTPRoute.timeouts.request | Read timeout (seconds) |
| `nginx.ingress.kubernetes.io/proxy-send-timeout` | HTTPRoute.timeouts.request | Send timeout (seconds) |

**Example conversion:**
```yaml
# NGINX Ingress annotation
nginx.ingress.kubernetes.io/proxy-read-timeout: "7200"

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

### Proxy Settings

| Annotation | Istio Support | Description |
|------------|---------------|-------------|
| `nginx.ingress.kubernetes.io/proxy-body-size` | EnvoyFilter (auto-generated) | Max request body size (e.g., "100m") |
| `nginx.ingress.kubernetes.io/proxy-buffering` | EnvoyFilter (auto-generated) | Enable/disable proxy buffering |
| `nginx.ingress.kubernetes.io/proxy-request-buffering` | Manual config required | Request buffering |

### Load Balancing (EWMA)

The `load-balance: ewma` annotation requires manual configuration via Istio DestinationRule:

```yaml
apiVersion: networking.istio.io/v1beta1
kind: DestinationRule
metadata:
  name: myservice-lb
  namespace: backend-service-1
spec:
  host: myservice.backend-service-1.svc.cluster.local
  trafficPolicy:
    loadBalancer:
      simple: LEAST_REQUEST  # Closest to EWMA behavior
```

### Rate Limiting

EnvoyFilters are auto-generated for rate limiting:

| Annotation | Description |
|------------|-------------|
| `nginx.ingress.kubernetes.io/limit-rps` | Rate limit in requests per second |
| `nginx.ingress.kubernetes.io/limit-rpm` | Rate limit in requests per minute (converted to RPS) |
| `nginx.ingress.kubernetes.io/limit-burst-multiplier` | Burst multiplier |

**Example EnvoyFilter output:**
```yaml
apiVersion: networking.istio.io/v1alpha3
kind: EnvoyFilter
metadata:
  name: backend-service-1-myroute-ratelimit
  namespace: backend-service-1-gateway
spec:
  targetRefs:
    - kind: Gateway
      name: backend-service-1-gateway
  configPatches:
    - applyTo: HTTP_FILTER
      patch:
        operation: INSERT_BEFORE
        value:
          name: envoy.filters.http.local_ratelimit
          typed_config:
            "@type": type.googleapis.com/envoy.extensions.filters.http.local_ratelimit.v3.LocalRateLimit
            token_bucket:
              max_tokens: 5000
              tokens_per_fill: 1000
              fill_interval: 1s
```

### Client Certificate Authentication

These annotations are stored in the IR:

| Annotation | Description |
|------------|-------------|
| `nginx.ingress.kubernetes.io/auth-tls-secret` | CA certificate secret for client verification |
| `nginx.ingress.kubernetes.io/auth-tls-verify-client` | Verify mode: on, off, optional |
| `nginx.ingress.kubernetes.io/auth-tls-verify-depth` | Max certificate chain depth |
| `nginx.ingress.kubernetes.io/auth-tls-pass-certificate-to-upstream` | Pass client cert to backend |

**Meshless Istio Limitation:** Client cert validation applies to the entire Gateway listener, not per-route. For per-customer client certs, use separate Gateway listeners or validate in the application.

### External Authentication

These annotations are stored in the IR:

| Annotation | Description |
|------------|-------------|
| `nginx.ingress.kubernetes.io/auth-url` | External auth service URL |
| `nginx.ingress.kubernetes.io/auth-method` | HTTP method (GET/POST) |
| `nginx.ingress.kubernetes.io/auth-signin` | Sign-in redirect URL |
| `nginx.ingress.kubernetes.io/auth-response-headers` | Headers to copy from auth response |

**Meshless Istio Limitation:** External auth (ext_authz) can only be configured at the Gateway level, not per-route. For per-route auth, implement auth checks in your application or enable Istio sidecars.

## Annotations Requiring App-Level Changes

The following annotations cannot be translated to Gateway API and require application changes. The tool emits warnings when these are detected:

| Annotation | Issue | Recommended Action |
|------------|-------|-------------------|
| `nginx.ingress.kubernetes.io/server-snippet` | Custom NGINX config has no Gateway API equivalent | Move logic to application middleware |
| `nginx.ingress.kubernetes.io/configuration-snippet` | Custom location config | Move to application |
| `nginx.ingress.kubernetes.io/use-regex` | Regex path matching is not GA in Gateway API | Refactor API paths to use prefix matching |
| `nginx.ingress.kubernetes.io/rewrite-target` (with `$1`, `$2`) | URLRewrite filter does not support capture groups | Refactor application to accept original paths |

## Annotations Not Yet Supported

| Annotation | Notes |
|------------|-------|
| `nginx.ingress.kubernetes.io/rewrite-target` | Regex capture groups not supported in Gateway API |
| `nginx.ingress.kubernetes.io/affinity` | Requires DestinationRule for session affinity |

If you are reliant on any annotations not listed above, please open an issue.

