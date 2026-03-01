# API Runbook

Manual testing and validation guide for the RSS API.

All examples assume the server is running locally on the default port:

```
go run ./cmd/api
```

The default base URL is `http://localhost:8080`.

---

## Healthcheck

**Endpoint:** `GET /v1/healthcheck`

Returns the current status of the application along with system info (environment and version).

### Basic request

```bash
curl -s localhost:8080/v1/healthcheck
```

**Expected response** (HTTP 200):

```json
{
  "status": "available",
  "system_info": {
    "environment": "development",
    "version": "0.1.0"
  }
}
```

### Verify response status code

```bash
curl -o /dev/null -s -w "%{http_code}\n" localhost:8080/v1/healthcheck
```

**Expected output:** `200`

### Verify Content-Type header

```bash
curl -s -I localhost:8080/v1/healthcheck
```

**Expected:** `Content-Type: application/json` is present in the response headers.

### Wrong HTTP method

The endpoint only accepts GET requests. Other methods should be rejected.

```bash
curl -s -o /dev/null -w "%{http_code}\n" -X POST localhost:8080/v1/healthcheck
```

**Expected output:** `405`

```bash
curl -s -o /dev/null -w "%{http_code}\n" -X PUT localhost:8080/v1/healthcheck
```

**Expected output:** `405`
