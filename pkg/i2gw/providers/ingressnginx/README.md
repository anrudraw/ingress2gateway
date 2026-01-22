# Ingress Nginx Provider

The project supports translating ingress-nginx specific annotations to Gateway API resources for Istio (meshless/OSS).

## Provider-Specific Flags

| Flag | Default | Description |
|------|---------|-------------|
| `--ingress-nginx-ingress-class` | `tag-ingress` | The name of the Ingress class to select |
| `--ingress-nginx-gateway-mode` | `centralized` | Gateway deployment mode: `centralized` (DEFAULT) or `per-namespace` |
| `--ingress-nginx-gateway-namespace` | `ionianshared` | Namespace for centralized gateway |
| `--ingress-nginx-gateway-name` | `platform-gateway` | Name of centralized gateway |

## Gateway Deployment Modes

### Centralized Mode (Default)

Creates a single shared gateway (`platform-gateway`) in `ionianshared`. **This is the recommended default for most teams.**

```bash
# Default: Centralized mode
ingress2gateway print --providers ingress-nginx --all-namespaces

# Explicit centralized mode with custom settings
ingress2gateway print --providers ingress-nginx \
  --ingress-nginx-gateway-mode=centralized \
  --ingress-nginx-gateway-namespace=ionianshared \
  --ingress-nginx-gateway-name=platform-gateway \
  --all-namespaces
```

**Result:**
```
ionianshared/                    # Platform team owned
    Gateway: platform-gateway
    ReferenceGrant: allow-routes-from-backend-service-1
    ReferenceGrant: allow-routes-from-backend-service-2
    EnvoyFilters

backend-service-1/
    HTTPRoute (parentRefs: ionianshared/platform-gateway)

backend-service-2/
    HTTPRoute (parentRefs: ionianshared/platform-gateway)
```

### Per-Namespace Mode (Exceptional Cases)

Creates a dedicated gateway namespace for each service namespace. **Only use when:**
- Regulatory requirements mandate traffic isolation (HIPAA, SOC2)
- Dedicated gateway resources are needed for performance SLAs
- Auth configuration must be gateway-scoped (ext_authz, mTLS)

```bash
# Per-namespace mode (not recommended for most teams)
ingress2gateway print --providers ingress-nginx \
  --ingress-nginx-gateway-mode=per-namespace \
  --namespace backend-service-1
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

## Istio Meshless Features

When using Istio without sidecars (meshless), the provider generates:

### Auto-Generated Resources Summary

| Annotation | Generated Resource | Notes |
|------------|-------------------|-------|
| `backend-protocol: HTTPS` | BackendTLSPolicy | mTLS to backend |
| `ssl-redirect: "true"` | HTTPRoute (redirect) | HTTP→HTTPS redirect |
| `limit-rps` | EnvoyFilter (local_ratelimit) | Rate limiting |
| `proxy-body-size` | EnvoyFilter (buffer) | Max body size |
| `proxy-buffering: "off"` | EnvoyFilter (circuit_breakers) | Disable buffering |
| `auth-url` | EnvoyFilter (ext_authz) | External authentication |
| Cross-namespace refs | ReferenceGrant | Allows HTTPRoute→Gateway |

### EnvoyFilters

For annotations that require Envoy-level configuration, Istio EnvoyFilters are auto-generated:

| Annotation | EnvoyFilter Type | Description |
|------------|-----------------|-------------|
| `nginx.ingress.kubernetes.io/limit-rps` | `local_ratelimit` | Request rate limiting |
| `nginx.ingress.kubernetes.io/proxy-body-size` | `buffer` | Max request body size |
| `nginx.ingress.kubernetes.io/proxy-buffering: "off"` | `circuit_breakers` | Disable buffering |
| `nginx.ingress.kubernetes.io/auth-url` | `ext_authz` | External authentication |

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

### Proxy Settings (Auto-Generated EnvoyFilters)

| Annotation | Istio Support | Description |
|------------|---------------|-------------|
| `nginx.ingress.kubernetes.io/proxy-body-size` | EnvoyFilter (auto-generated) | Max request body size (e.g., "100m") |
| `nginx.ingress.kubernetes.io/proxy-buffering` | EnvoyFilter (auto-generated) | Enable/disable proxy buffering |
| `nginx.ingress.kubernetes.io/proxy-request-buffering` | Manual config required | Request buffering |

### Rate Limiting (Auto-Generated EnvoyFilters)

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

### SSL Redirect (Auto-Generated HTTPRoutes)

When `ssl-redirect: "true"` is detected, the tool automatically generates redirect HTTPRoutes:

| Annotation | Gateway API Equivalent | Description |
|------------|----------------------|-------------|
| `nginx.ingress.kubernetes.io/ssl-redirect` | HTTPRoute with RequestRedirect filter | Redirect HTTP to HTTPS |
| `nginx.ingress.kubernetes.io/force-ssl-redirect` | HTTPRoute with RequestRedirect filter | Force redirect even without TLS |

**Example output:**
```yaml
apiVersion: gateway.networking.k8s.io/v1
kind: HTTPRoute
metadata:
  name: myservice-redirect
  annotations:
    ingress2gateway.kubernetes.io/source: nginx.ingress.kubernetes.io/ssl-redirect
spec:
  parentRefs:
    - name: myservice-gateway
      sectionName: myhost-http  # Targets HTTP listener only
  hostnames:
    - myhost.example.com
  rules:
    - filters:
        - type: RequestRedirect
          requestRedirect:
            scheme: https
            statusCode: 301
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

**Centralized Mode Warning:** In centralized mode, a WARNING is emitted because client cert validation on the shared platform Gateway affects ALL services on that listener.

### External Authentication (Auto-Generated EnvoyFilter)

When `auth-url` is detected, an ext_authz EnvoyFilter is automatically generated:

| Annotation | Description |
|------------|-------------|
| `nginx.ingress.kubernetes.io/auth-url` | External auth service URL |
| `nginx.ingress.kubernetes.io/auth-method` | HTTP method (GET/POST) |
| `nginx.ingress.kubernetes.io/auth-signin` | Sign-in redirect URL |
| `nginx.ingress.kubernetes.io/auth-response-headers` | Headers to copy from auth response |

**Example EnvoyFilter output:**
```yaml
apiVersion: networking.istio.io/v1alpha3
kind: EnvoyFilter
metadata:
  name: myservice-extauthz
  namespace: myservice-gateway
  annotations:
    ingress2gateway.kubernetes.io/auth-url: https://auth.example.com/verify
spec:
  targetRefs:
    - kind: Gateway
      name: myservice-gateway
  configPatches:
    - applyTo: HTTP_FILTER
      patch:
        operation: INSERT_BEFORE
        value:
          name: envoy.filters.http.ext_authz
          typed_config:
            "@type": type.googleapis.com/envoy.extensions.filters.http.ext_authz.v3.ExtAuthz
            http_service:
              server_uri:
                uri: https://auth.example.com/verify
                cluster: outbound|80||ext-authz-service
                timeout: 5s
```

**Meshless Istio Limitation:** External auth (ext_authz) can only be configured at the Gateway level, not per-route. For per-route auth, implement auth checks in your application or enable Istio sidecars.

**Centralized Mode Warning:** In centralized mode, a WARNING is emitted because the ext_authz EnvoyFilter targets the shared platform Gateway and applies to ALL services.

## Notification Types

The tool emits three types of notifications to help you understand the migration:

| Type | Meaning | Action Required |
|------|---------|----------------|
| **INFO** | Feature auto-generated or handled | Review generated resources |
| **WARNING** | Feature has caveats or blast radius concerns | Review and validate |
| **ERROR** | Migration blocker requiring app changes | Must fix before migration |

**Examples:**
- `INFO`: BackendTLSPolicy created, EnvoyFilter generated, HTTPRoute redirect created
- `WARNING`: Centralized mode auth affects all services
- `ERROR`: server-snippet, use-regex, rewrite-target with capture groups

## Annotations Requiring App-Level Changes

The following annotations cannot be translated to Gateway API and require application changes. The tool emits **ERROR** notifications when these are detected:

| Annotation | Issue | Recommended Action |
|------------|-------|-------------------|
| `nginx.ingress.kubernetes.io/server-snippet` | Custom NGINX config has no Gateway API equivalent | Move logic to application middleware |
| `nginx.ingress.kubernetes.io/configuration-snippet` | Custom location config | Move to application or use EnvoyFilter |
| `nginx.ingress.kubernetes.io/use-regex` | Regex path matching is not GA in Gateway API | Refactor API paths to use prefix matching |
| `nginx.ingress.kubernetes.io/rewrite-target` (with `$1`, `$2`) | URLRewrite filter does not support capture groups | Refactor application to accept original paths |

## Annotations Not Yet Supported

| Annotation | Notes |
|------------|-------|
| `nginx.ingress.kubernetes.io/rewrite-target` | Regex capture groups not supported in Gateway API |
| `nginx.ingress.kubernetes.io/affinity` | Requires DestinationRule for session affinity |

If you are reliant on any annotations not listed above, please open an issue.

