#!/usr/bin/env python3
import argparse
import json
from pathlib import Path


def percentile(values, fraction):
    ordered = sorted(values)
    if not ordered:
        return 0
    return ordered[min(len(ordered) - 1, int((len(ordered) - 1) * fraction))]


def summarize(rows):
    report = {}
    for operation in sorted({row["operation"] for row in rows}):
        selected = [row for row in rows if row["operation"] == operation]
        latency = [float(row["latency_ms"]) for row in selected]
        report[operation] = {
            "samples": len(selected),
            "p50_ms": percentile(latency, 0.50),
            "p95_ms": percentile(latency, 0.95),
            "max_payload_bytes": max((int(row.get("payload_bytes", 0)) for row in selected), default=0),
            "clipping_count": sum(int(row.get("clipped", False)) for row in selected),
        }
    return report


if __name__ == "__main__":
    parser = argparse.ArgumentParser()
    parser.add_argument("samples", type=Path)
    args = parser.parse_args()
    rows = [json.loads(line) for line in args.samples.read_text().splitlines() if line]
    print(json.dumps(summarize(rows), sort_keys=True, indent=2))
