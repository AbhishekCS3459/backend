# Prometheus Metrics - Complete Guide

## What You're Seeing

The `/metrics` endpoint exposes two types of metrics:

1. **Go Runtime Metrics** (always present):
   - `go_goroutines` - Number of goroutines
   - `go_memstats_*` - Memory usage
   - `go_gc_*` - Garbage collection stats
   - `process_*` - Process-level stats (CPU, memory, file descriptors)

2. **Custom HTTP Metrics** (appear after requests):
   - `http_requests_total` - Total requests per endpoint
   - `http_request_duration_seconds` - Request latency
   - `http_request_size_bytes` - Request payload size
   - `http_response_size_bytes` - Response payload size

## What to Do With Metrics

### Option 1: Manual Monitoring (Quick Check)

Just curl the endpoint to see current state:

```bash
# View all metrics
curl http://localhost:8080/metrics

# Filter for specific metrics
curl http://localhost:8080/metrics | grep http_requests_total
curl http://localhost:8080/metrics | grep http_request_duration_seconds
```

**Use case:** Quick health checks, debugging, manual monitoring

### Option 2: Prometheus + Grafana in Docker (recommended)

Nothing needs to be installed locally besides Docker. Both services are defined in
`docker-compose.yml` and configured from the `monitoring/` folder:

```text
monitoring/
├── prometheus/
│   ├── prometheus.yml   # scrape config (API at host.docker.internal:8080)
│   └── alerts.yml       # ApiDown, HighErrorRate, SlowResponses
└── grafana/
    ├── provisioning/    # Prometheus data source + dashboard provider
    └── dashboards/
        └── find-me-api.json
```

#### 1. Start the API and the monitoring stack

```bash
make run             # API on the host, port 8080
make monitoring-up   # docker compose up -d prometheus grafana
```

| Service    | URL                                            | Login           |
| ---------- | ---------------------------------------------- | --------------- |
| Prometheus | [localhost:9090](http://localhost:9090)        | -               |
| Grafana    | [localhost:3001](http://localhost:3001)        | `admin`/`admin` |

Grafana uses port 3001 because the retailer UI dev server already uses 3000.

Prometheus will:

- Scrape the API's `/metrics` every 15 seconds
- Keep 15 days of history in the `prometheus_data` volume
- Evaluate the alert rules in `monitoring/prometheus/alerts.yml`

Check that the API target is `UP` at [localhost:9090/targets](http://localhost:9090/targets).
If `PORT` in `.env` is not `8080`, change the target in `monitoring/prometheus/prometheus.yml`
and reload Prometheus:

```bash
curl -X POST http://localhost:9090/-/reload
```

Stop everything with `make monitoring-down` (data is kept in Docker volumes).

#### 2. Route labels

The `path` label is the matched chi route, not the raw URL. For example, every
`/api/stores/<uuid>/products` request is recorded as `/api/stores/{storeID}/products`,
and requests that match no route are recorded as `unmatched`. This keeps the number of
time series bounded no matter how many stores or IDs exist.

#### 3. Query Metrics in Prometheus UI

Open `http://localhost:9090` and try these queries:

##### Total requests

```text
http_requests_total
```

##### Requests per second

```text
rate(http_requests_total[5m])
```

##### Average response time

```text
rate(http_request_duration_seconds_sum[5m]) / rate(http_request_duration_seconds_count[5m])
```

##### Error rate (5xx)

```text
rate(http_requests_total{status=~"5.."}[5m])
```

##### Requests by endpoint

```text
sum by (path) (rate(http_requests_total[5m]))
```

##### 95th percentile latency

```text
histogram_quantile(0.95, sum by (le) (rate(http_request_duration_seconds_bucket[5m])))
```

### Option 3: Grafana Dashboards (Visualization)

Grafana starts with `make monitoring-up`, so there is nothing to install and no data source to add by hand.

1. Open [localhost:3001](http://localhost:3001) and log in with `admin` / `admin`.
2. The **Find Me API** dashboard (folder **Find Me**) opens as the home page. It is provisioned from
   `monitoring/grafana/dashboards/find-me-api.json` and uses the provisioned **Prometheus** data source
   (`http://prometheus:9090` inside the Docker network).

The dashboard shows:

| Panel                        | Query (simplified)                                                        |
| ---------------------------- | ------------------------------------------------------------------------- |
| API up                       | `up{job="find-me-api"}`                                                   |
| Requests / sec               | `sum(rate(http_requests_total[5m]))`                                      |
| 5xx error rate               | 5xx requests / all requests                                               |
| p95 latency                  | `histogram_quantile(0.95, sum by (le) (rate(..._bucket[5m])))`            |
| Requests / sec by route      | `sum by (method, path) (rate(http_requests_total[5m]))`                   |
| Latency percentiles          | p50, p95, p99                                                             |
| Errors by route (4xx / 5xx)  | `sum by (status, path) (rate(http_requests_total{status=~"[45].."}[5m]))` |
| Slowest routes (p95)         | `topk(10, histogram_quantile(0.95, sum by (le, path) (...)))`             |
| Goroutines, Memory           | `go_goroutines`, `go_memstats_heap_alloc_bytes`, `process_resident_memory_bytes` |

Scrapes of `/metrics` itself are excluded from the traffic panels.

To change the dashboard, edit it in Grafana, export the JSON (Share → Export), and save it over
`monitoring/grafana/dashboards/find-me-api.json` so the change is kept in git.

### Option 4: Alerting

Alert rules live in `monitoring/prometheus/alerts.yml` and are loaded automatically:

| Alert           | Fires when                                            |
| --------------- | ----------------------------------------------------- |
| `ApiDown`       | Prometheus cannot scrape the API for 1 minute         |
| `HighErrorRate` | More than 5% of requests return 5xx for 5 minutes     |
| `SlowResponses` | p95 latency is above 1 second for 5 minutes           |

See their state at [localhost:9090/alerts](http://localhost:9090/alerts). Sending notifications
(email, Slack) needs an Alertmanager, which is not part of the local stack.

## Common Use Cases

### 1. Performance Monitoring

- Track slow endpoints
- Identify bottlenecks
- Monitor response times

### 2. Capacity Planning

- Understand traffic patterns
- Plan for scaling
- Identify peak usage

### 3. Error Tracking

- Monitor error rates
- Get alerts on failures
- Track error trends

### 4. Debugging

- See which endpoints are called
- Check request/response sizes
- Monitor goroutine count

## Quick Start (Docker Compose)

```bash
make run             # API on :8080
make monitoring-up   # Prometheus on :9090, Grafana on :3001
make monitoring-down # stop both
```

## Example Queries

### Total requests in last hour

```text
sum(increase(http_requests_total[1h]))
```

### Average request size

```text
rate(http_request_size_bytes_sum[5m]) / rate(http_request_size_bytes_count[5m])
```

### Success rate

```text
sum(rate(http_requests_total{status=~"2.."}[5m])) / sum(rate(http_requests_total[5m]))
```

### Requests by method

```text
sum by (method) (rate(http_requests_total[5m]))
```

## Next Steps

1. **Development**: `make monitoring-up` and open the Grafana dashboard
2. **Production**: `/metrics` is public today; restrict it (private network, IP allow-list or auth) before pointing a hosted Prometheus or Grafana Cloud at the Azure API

## Resources

- [Prometheus Query Language](https://prometheus.io/docs/prometheus/latest/querying/basics/)
- [Grafana Dashboards](https://grafana.com/grafana/dashboards/)
- [Prometheus Best Practices](https://prometheus.io/docs/practices/naming/)
