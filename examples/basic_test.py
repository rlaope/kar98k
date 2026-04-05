#!/usr/bin/env python3
"""basic_test.py - kar98k external process protocol example (Python)"""

import sys
import json


def send(msg):
    print(json.dumps(msg), flush=True)


def recv():
    line = sys.stdin.readline()
    if not line:
        sys.exit(0)
    return json.loads(line.strip())


def main():
    while True:
        cmd = recv()

        if cmd["cmd"] == "init":
            send({
                "type": "scenario",
                "name": "python-health-check",
                "chaos": {
                    "preset": "moderate",
                    "spike_factor": 2.0,
                    "noise_amplitude": 0.10,
                    "lambda": 0.005,
                },
            })
            send({"type": "done"})

        elif cmd["cmd"] == "setup":
            send({"type": "done", "data": {"token": "py-session-123"}})

        elif cmd["cmd"] == "iterate":
            # Step 1: Health check
            send({
                "type": "http",
                "method": "GET",
                "url": "http://localhost:8080/health",
            })

            # Step 2: Check
            send({
                "type": "check",
                "name": "health ok",
                "passed": True,
            })

            send({"type": "done"})

        elif cmd["cmd"] == "teardown":
            send({"type": "done"})

        else:
            send({"type": "error", "message": f"unknown cmd: {cmd['cmd']}"})


if __name__ == "__main__":
    main()
