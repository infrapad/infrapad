#!/usr/bin/env python3
"""Fetch firing demo alerts from Prometheus and save a complete
Prometheus/AlertsMatcher submitPayload request (query_results)."""

import argparse
import json
import math
import os
import sys
import tempfile
import time
import urllib.error
import urllib.parse
import urllib.request
from datetime import datetime, timezone
from pathlib import Path

# Demo-specific selector, deliberately independent of the matcher's label
# alternatives: a label-free matcher still receives the demo's results.
QUERY = 'ALERTS{alertname="MonitoredAppEndpointDown",alertstate="firing",endpoint=~"endpoint1|endpoint2"}'
PADDING_SECONDS = 120
BASE_STEP_SECONDS = 5
MAX_POINTS_PER_SERIES = 10_000


class SnapshotError(Exception):
    pass


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("payload_file", type=Path, help="Output submitPayload request JSON")
    parser.add_argument("--since", required=True, help="Incident start (RFC3339)")
    parser.add_argument("--prometheus-url", required=True)
    parser.add_argument("--backfill-seconds", type=int, default=0,
                        help="Demo only: extend series spanning less than this many seconds to span it")
    args = parser.parse_args()

    try:
        since = parse_time(args.since)
        start = int(since.timestamp()) - PADDING_SECONDS
        end = int(time.time())  # captured fetch time; no future padding
        if end <= start:
            raise SnapshotError(f"query end {end} is not after query start {start}")
        step = choose_step(start, end)
        series = fetch_series(args.prometheus_url.rstrip("/"), start, end, step)
        for s in series:
            backfill(s["samples"], args.backfill_seconds, step, start)
    except SnapshotError as e:
        print(f"ERROR: snapshot fetch failed: {e}", file=sys.stderr)
        sys.exit(1)

    query_results = {
        "start": rfc3339(start),
        "end": rfc3339(end),
        "step_seconds": step,
        "series": series,  # explicit [] for a successful empty matrix
    }
    write_atomically(args.payload_file, {"payload": {"query_results": query_results}})
    print(f"{len(series)} series, {rfc3339(start)} .. {rfc3339(end)}, step {step}s")


def choose_step(start, end):
    # Grow the step in base multiples to stay within the per-series point limit.
    step = BASE_STEP_SECONDS
    while (end - start) // step + 1 > MAX_POINTS_PER_SERIES:
        step += BASE_STEP_SECONDS
    return step


def fetch_series(prometheus_url, start, end, step):
    params = urllib.parse.urlencode({"query": QUERY, "start": start, "end": end, "step": step})
    url = f"{prometheus_url}/api/v1/query_range?{params}"
    try:
        with urllib.request.urlopen(url, timeout=30) as resp:
            body = json.load(resp)
    except urllib.error.HTTPError as e:
        raise SnapshotError(f"HTTP {e.code} from {url}: {e.read().decode(errors='replace')[:500]}")
    except (urllib.error.URLError, OSError, ValueError) as e:
        raise SnapshotError(f"request to {url} failed: {e}")

    if not isinstance(body, dict) or body.get("status") != "success":
        raise SnapshotError(f"Prometheus API error: {json.dumps(body)[:500]}")
    data = body.get("data")
    if not isinstance(data, dict) or data.get("resultType") != "matrix" or not isinstance(data.get("result"), list):
        raise SnapshotError("malformed response: expected a matrix result")
    return [convert_series(i, s, start, end) for i, s in enumerate(data["result"])]


def convert_series(index, raw, start, end):
    labels, values = (raw.get("metric"), raw.get("values")) if isinstance(raw, dict) else (None, None)
    if not isinstance(labels, dict) or not all(isinstance(v, str) for v in labels.values()):
        raise SnapshotError(f"malformed labels in series {index}")
    if not isinstance(values, list):
        raise SnapshotError(f"malformed samples in series {index}")
    samples, previous = [], -math.inf
    for pair in values:
        try:
            ts, value = float(pair[0]), float(pair[1])
        except (TypeError, ValueError, IndexError):
            raise SnapshotError(f"malformed sample {pair!r} in series {index}")
        if not (math.isfinite(ts) and math.isfinite(value)):
            raise SnapshotError(f"non-finite sample {pair!r} in series {index}")
        if not start <= ts <= end or ts <= previous:
            raise SnapshotError(f"sample timestamp {ts} out of bounds or order in series {index}")
        previous = ts
        samples.append([int(ts) if ts.is_integer() else ts, value])
    return {"labels": labels, "samples": samples}


def backfill(samples, seconds, step, start):
    # A freshly started demo incident has only seconds of firing data. When a
    # series spans less than `seconds`, artificially repeat its first sample
    # at earlier step intervals until it spans `seconds`, so the chart shows
    # a meaningful alert. Longer series are left untouched. Stays within the
    # query bounds.
    if not samples or seconds <= 0:
        return
    first_ts, value = samples[0]
    from_ts = max(samples[-1][0] - seconds, start)
    count = int((first_ts - from_ts) // step)
    samples[:0] = [[first_ts - k * step, value] for k in range(count, 0, -1)]


def write_atomically(path, data):
    # A failed run never replaces a previously prepared payload.
    path.parent.mkdir(parents=True, exist_ok=True)
    fd, tmp = tempfile.mkstemp(dir=path.parent, prefix=f".{path.name}.")
    with os.fdopen(fd, "w") as f:
        json.dump(data, f, indent=2)
        f.write("\n")
    os.replace(tmp, path)


def parse_time(value):
    try:
        return datetime.fromisoformat(value.replace("Z", "+00:00"))
    except ValueError:
        raise SnapshotError(f"invalid RFC3339 time {value!r}")


def rfc3339(unix_seconds):
    return datetime.fromtimestamp(unix_seconds, timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")


if __name__ == "__main__":
    main()
