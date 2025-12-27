# Ingress Nginx Provider

The project supports translating ingress-nginx specific annotations.

**Ingress class name**

To specify the name of the Ingress class to select, use `--ingress-nginx-ingress-class=ingress-nginx` (default to 'nginx').

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

## Annotations Not Yet Supported

The following annotations require manual migration:

| Annotation | Gateway API Alternative | Notes |
|------------|------------------------|-------|
| `nginx.ingress.kubernetes.io/rewrite-target` | HTTPRoute URLRewrite filter | Regex capture groups not supported |
| `nginx.ingress.kubernetes.io/configuration-snippet` | EnvoyPatchPolicy / ExtensionRef | Implementation-specific |
| `nginx.ingress.kubernetes.io/auth-url` | SecurityPolicy.extAuth | Implementation-specific |
| `nginx.ingress.kubernetes.io/limit-rps` | BackendTrafficPolicy.rateLimit | Implementation-specific |
| `nginx.ingress.kubernetes.io/proxy-buffering` | BackendTrafficPolicy | Implementation-specific |
| `nginx.ingress.kubernetes.io/proxy-body-size` | BackendTrafficPolicy | Implementation-specific |
| `nginx.ingress.kubernetes.io/affinity` | BackendTrafficPolicy.sessionPersistence | Implementation-specific |
| `nginx.ingress.kubernetes.io/auth-tls-*` | SecurityPolicy.clientValidation | Implementation-specific |
| `nginx.ingress.kubernetes.io/server-snippet` | N/A | Move logic to application |

If you are reliant on any annotations not listed above, please open an issue. In the meantime you'll need to manually find a Gateway API equivalent.

