---
doc: 42cd704a-f697-4a78-9c29-3c7235c9500f
title: Payment service crash loop
namespace: payments
status: active
---
::infrapad_block{block=1 rev=2 type=alerts_matcher author=incident_detector:123}
```yaml
LabelsMatchers:
  - name: [CrashLoopBackOff]
  - name: [KubeNodeNotReady]
```
::infrapad_block{block=2 rev=2 type=markdown author=agentic_run_analysis:456}
# Initial investigation

## Situation description

This incident started at 2026-07-12 12:42 by CrashLoopBackOff...

::infrapad_block{block=3 rev=1 type=markdown author=agentic_run_execution:543}
Get the pods in the namespace
```
$ oc get pods --namespace paymants
```
