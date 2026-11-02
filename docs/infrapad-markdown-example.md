---
document: 42cd704a-f697-4a78-9c29-3c7235c9500f
title: Payment service crash loop
namespace: payments
status: active
---
::infrapad_block{block=1 rev=2 type=Prometheus/AlertsMatcher author=incident_detector:123}
```yaml
labels_matchers:
  - alertname: [CrashLoopBackOff]
  - alertname: [KubeNodeNotReady]
since: "2026-07-12T12:42:00Z"
```
::infrapad_block{block=2 rev=2 type=Core/Markdown author=agentic_run_analysis:456}
# Initial investigation

## Situation description

This incident started at 2026-07-12 12:42 by CrashLoopBackOff...

::infrapad_block{block=3 rev=1 type=Core/Markdown author=agentic_run_execution:543}
Get the pods in the namespace
```
$ oc get pods --namespace paymants
```
