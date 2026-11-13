#!/usr/bin/env python3
"""Build complete synthetic Slack payloads; no Slack connection is needed."""

import argparse
import json
from datetime import datetime, timezone
from pathlib import Path


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("stage", choices=("initial", "triage", "resolved"))
    parser.add_argument("payload_file", type=Path, help="Saved submitPayload request JSON")
    parser.add_argument("--at", required=True, help="Thread record timestamp or resolution time (RFC3339)")
    parser.add_argument("--app-url", required=True)
    parser.add_argument("--prometheus-url", required=True)
    args = parser.parse_args()
    at = int(datetime.fromisoformat(args.at.replace("Z", "+00:00")).timestamp())

    if args.stage == "initial":
        # Round up to the next whole second so the first message sorts
        # strictly after the thread's server-assigned event timestamp.
        payload = initial_payload(at + 1, args.prometheus_url.rstrip("/"))
    else:
        payload = json.loads(args.payload_file.read_text())["payload"]
        if args.stage == "triage":
            add_triage(payload, args.app_url.rstrip("/"), args.prometheus_url.rstrip("/"))
        else:
            add_resolution(payload, at)

    # This is the public SubmitBlockPayload request, not a persisted record
    # envelope. Each submission includes the entire thread, not just new replies.
    args.payload_file.write_text(json.dumps({"payload": payload}, indent=2) + "\n")


def initial_payload(at, prometheus_url):
    return {
        "agents": {"U_TRIAGE_BOT": "infrapad-triage"},
        "messages": [
            message(at, "U_ALICE", "Alice (on-call)",
                    "*Incident opened:* endpoint1 and endpoint2 are returning HTTP 500. "
                    "Starting a simulated incident in #monitored-app-incidents."),
            message(at + 1, "U_BOB", "Bob (SRE)",
                    "Acknowledged. Checking the firing alerts and application metrics.",
                    previews=[{
                        "title": "MonitoredAppEndpointDown",
                        "title_link": f"{prometheus_url}/alerts",
                        "updated_at": datetime.fromtimestamp(at + 1, timezone.utc).isoformat(),
                        "text": "Both simulated endpoints are unavailable; triage is in progress.",
                        "fields": [
                            {"label": "Severity", "value": "critical"},
                            {"label": "Endpoints", "value": "endpoint1, endpoint2"},
                        ],
                    }]),
        ],
    }


def add_triage(payload, app_url, prometheus_url):
    at = int(payload["messages"][0]["ts"].split(".")[0])
    # Edit an existing message/preview without changing its ts identity. The
    # server revises the original conversation block, demonstrating history/diff.
    payload["messages"][1]["text"] = (
        "Confirmed: both MonitoredAppEndpointDown alerts are firing. "
        "The process and /metrics endpoint are healthy; asking the triage agent for a summary."
    )
    payload["messages"][1]["previews"][0]["text"] = (
        "Both endpoint alerts are firing. Application metrics remain reachable."
    )
    payload["messages"] = payload["messages"][:2] + [
        message(at + 2, "U_TRIAGE_BOT", "InfraPad triage agent", "\n".join([
            "## Automated triage", "",
            "> This is a simulated investigation, not an external Slack or AI service.", "",
            "Both endpoints are failing together while the application still serves metrics. "
            "This points to endpoint failure state rather than an unavailable process.", "",
            "| Signal | Observation |", "| --- | --- |",
            "| endpoint1 | HTTP 500 |", "| endpoint2 | HTTP 500 |",
            "| /metrics | HTTP 200 |", "",
            "### Suggested checks", "",
            "1. Compare the alerts with the Prometheus snapshot chart in this document.",
            "2. Inspect the app's simulated failure controls before restarting anything.",
            "3. Ask the incident commander to approve recovery.", "",
            "```", f"curl -i {app_url}/endpoint1", f"curl -i {app_url}/endpoint2", "```", "",
            f"[Open alerts]({prometheus_url}/alerts) · [Application metrics]({app_url}/metrics)",
        ])),
        message(at + 3, "U_ALICE", "Alice (on-call)",
                "Agreed. This matches the demo's injected endpoint failures. "
                "No evidence of process failure; please keep the service running."),
        message(at + 4, "U_BOB", "Bob (SRE)",
                "Recovery plan: reset both endpoint controls, wait for Prometheus to clear, "
                "and verify the refreshed snapshot. Holding for approval."),
        message(at + 5, "U_TRIAGE_BOT", "InfraPad triage agent", "\n".join([
            "### Triage hand-off", "",
            "**Working diagnosis:** deliberately injected endpoint failures.", "",
            "- **Impact:** both monitored endpoints are unavailable.",
            "- **Mitigation:** reset the endpoint controls with `./simulate.sh resolve`.",
            "- **Success criteria:** HTTP 200 on both endpoints and no firing alerts.", "",
            "<em>Awaiting operator approval; no automated recovery action was taken.</em>",
        ])),
    ]


def add_resolution(payload, at):
    # Keep resolution replies stable across repeated resolve runs.
    if any(m["user"] == "U_RESOLVER" for m in payload["messages"]):
        return
    at = max(at, int(payload["messages"][-1]["ts"].split(".")[0]) + 1)
    payload["messages"].extend([
        message(at, "U_RESOLVER", "Alice (on-call)",
                "Recovery approved and completed. Both endpoints now return HTTP 200 "
                "and Prometheus has cleared the alerts."),
        message(at + 1, "U_TRIAGE_BOT", "InfraPad triage agent", "\n".join([
            "### Recovery verification", "",
            "**Resolved:** both endpoints are healthy and the alerts have cleared.", "",
            "The alert snapshot now covers the bounded incident window. The Slack conversation, "
            "triage cards, and revision history remain available for the post-incident review.",
        ])),
    ])


def message(at, user, name, text, **extra):
    return {"ts": f"{at}.000000", "user": user, "user_name": name, "text": text, **extra}


if __name__ == "__main__":
    main()
