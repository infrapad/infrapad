---
document: 0d0423c7-1348-49a0-bb2a-439cd7371120
title: Checkout latency incident
namespace: payments
status: active
---
::infrapad_block{block=1 rev=1 type=Prometheus/AlertsMatcher author=alert-triage timestamp=2026-01-01T09:58:30.360160618Z}
```yaml infrapad
content:
  labels_matchers:
    - alertname:
        - CheckoutLatencyHigh
      service:
        - checkout
  since: "2026-01-01T09:58:00Z"
```
::infrapad_block{block=2 rev=1 type=Slack/Thread author=slack-bridge timestamp=2026-01-01T09:59:12.372291244Z}
```yaml infrapad
content:
  url: https://example.slack.com/archives/C0PAYMENTS/p1767261600000100
```
[Slack thread](https://example.slack.com/archives/C0PAYMENTS/p1767261600000100)
::infrapad_block{block=3 rev=2 type=Slack/Conversation author=slack-bridge parent=2 timestamp=2026-01-01T10:00:00.0001Z}
- **Alice** · 2026-01-01 10:00:00 UTC · [source](https://example.slack.com/archives/C0PAYMENTS/p1767261600000100)
  Checkout p99 latency is above 2s since 10:00 UTC (EU only).
::infrapad_block{block=4 rev=1 type=AIAgent/Message author=slack-bridge parent=2 timestamp=2026-01-01T10:01:00.0002Z}
```yaml infrapad
content:
  agent: triage-bot
  extras:
    slack_ts: "1767261660.000200"
    slack_user_id: U_TRIAGE
  url: https://example.slack.com/archives/C0PAYMENTS/p1767261660000200
```
Latency correlates with the 09:58 `checkout-db` failover.
::infrapad_block{block=5 rev=2 type=Slack/Conversation author=slack-bridge parent=2 timestamp=2026-01-01T10:02:00.0003Z}
- **Bob** · 2026-01-01 10:02:00 UTC · [source](https://example.slack.com/archives/C0PAYMENTS/p1767261720000300)
  Confirmed, the connection pool is still reconnecting.

  > Attachment: **[CheckoutLatencyHigh](https://alerts.example/checkout)**
  > p99 > 2s
  > **Severity:** warning
  > Updated: 2026-01-01 10:02:00 UTC
- **Alice** · 2026-01-01 10:03:00 UTC · [source](https://example.slack.com/archives/C0PAYMENTS/p1767261780000400)
  Pool recycled; latency back to normal.
::infrapad_block{block=6 rev=2 type=Core/Markdown author=jane timestamp=2026-01-01T10:05:41.39883983Z}
# Notes

Follow up on the failover runbook:

```yaml
pool:
  reconnect_backoff: 5s
```

- [ ] Add a pool reconnect alert
::infrapad_block{block=7 rev=1 type=Core/Markdown author="Jane Doe" timestamp=2026-01-01T10:20:00Z}
## Timeline summary

Latency recovered after the pool was recycled.
