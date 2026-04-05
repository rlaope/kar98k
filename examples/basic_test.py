#!/usr/bin/env python3
"""k6-style load test written in Python."""

import sys, os
sys.path.insert(0, os.path.join(os.path.dirname(__file__), "..", "sdk", "python"))

from kar98k import scenario, chaos, http, check, sleep, think_time

# Configure scenario
scenario(
    name="python-api-test",
    pattern=chaos(preset="moderate", spike_factor=2.5),
    thresholds={
        "http_req_duration{p95}": "< 500ms",
        "http_req_failed": "< 0.05",
    },
)

# Setup — runs once
def setup():
    return {"session": "py-session-abc"}

# Main iteration — runs per VU
def default(data):
    # GET health
    resp = http.get("http://localhost:8080/health")
    check(resp, {
        "health status 200": lambda r: r.status == 200,
        "has status field": lambda r: "status" in r.json(),
    })

    sleep(think_time("100ms", "500ms"))

    # GET users
    resp = http.get("http://localhost:8080/api/users")
    check(resp, {
        "users status 200": lambda r: r.status == 200,
    })

# Teardown — runs once at end
def teardown(data):
    pass
