# monitored-app

A minimal Go HTTP service with controllable endpoint health, Prometheus metrics, and alerting.

## Endpoints

The app exposes two endpoints (`/endpoint1`, `/endpoint2`) that start healthy and can be toggled to a failure state.

### Check status

```bash
curl http://localhost:8080/endpoint1   # 200 "ok"
curl http://localhost:8080/endpoint2   # 200 "ok"
```

### Set an endpoint to fail

```bash
curl -X POST -d 'fail' http://localhost:8080/endpoint1
curl http://localhost:8080/endpoint1   # 500 "fail"
curl http://localhost:8080/endpoint2   # 200 "ok" (unaffected)
```

### Recover an endpoint

```bash
curl -X POST -d 'ok' http://localhost:8080/endpoint1
curl http://localhost:8080/endpoint1   # 200 "ok"
```

## Metrics

`GET /metrics` exposes a `monitored_app_up` gauge per endpoint:

```
monitored_app_up{endpoint="endpoint1"} 1
monitored_app_up{endpoint="endpoint2"} 1
```

The value is `1` when the endpoint returns "ok" and `0` when set to "fail".

## Alerting

When deployed via `task pd:monitored-app`, Prometheus scrapes the metrics and evaluates the `MonitoredAppEndpointDown` alert rule — it fires whenever `monitored_app_up == 0` for any endpoint. Alerts are forwarded to Alertmanager.

- Prometheus UI: http://localhost:9090
- Alertmanager UI: http://localhost:9093
