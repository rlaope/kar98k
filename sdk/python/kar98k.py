"""kar98k Python SDK — write load tests in native Python.

Usage:
    from kar98k import scenario, chaos, http, check, sleep, think_time, stage, ramp

    scenario(name="my-test", pattern=chaos(preset="aggressive"))

    def setup():
        return {"token": "abc"}

    def default(data):
        resp = http.get("http://localhost:8080/api", headers={"Authorization": f"Bearer {data['token']}"})
        check(resp, {"status 200": lambda r: r.status == 200})
        sleep(think_time("1s", "3s"))
"""

import sys
import json
import random
import time as _time
import inspect
import atexit

# --- Protocol layer (hidden from user) ---

def _send(msg):
    print(json.dumps(msg), flush=True)

def _recv():
    line = sys.stdin.readline()
    if not line:
        sys.exit(0)
    return json.loads(line.strip())


# --- Response object ---

class Response:
    """HTTP response returned by http.get/post/etc."""

    def __init__(self, status, body="", duration=0.0, error=""):
        self.status = status
        self.body = body
        self.duration = duration
        self.error = error

    def json(self):
        return json.loads(self.body) if self.body else None

    def __repr__(self):
        return f"<Response status={self.status} duration={self.duration:.3f}s>"


# --- HTTP module ---

class _HTTP:
    """HTTP client — http.get(), http.post(), etc."""

    def get(self, url, **kwargs):
        return self._request("GET", url, **kwargs)

    def post(self, url, **kwargs):
        return self._request("POST", url, **kwargs)

    def put(self, url, **kwargs):
        return self._request("PUT", url, **kwargs)

    def delete(self, url, **kwargs):
        return self._request("DELETE", url, **kwargs)

    def patch(self, url, **kwargs):
        return self._request("PATCH", url, **kwargs)

    def _request(self, method, url, headers=None, json_body=None, body=None):
        msg = {"type": "http", "method": method, "url": url}
        if headers:
            msg["headers"] = headers
        if json_body is not None:
            msg["body"] = json.dumps(json_body)
            if not headers:
                msg["headers"] = {}
            msg["headers"]["Content-Type"] = "application/json"
        elif body:
            msg["body"] = body

        _send(msg)

        # Read response from kar98k
        resp_data = _recv()
        return Response(
            status=resp_data.get("status", 0),
            body=resp_data.get("body", ""),
            duration=resp_data.get("duration", 0.0),
            error=resp_data.get("error", ""),
        )


http = _HTTP()


# --- Scenario config ---

_scenario_config = {}
_chaos_config = {}
_stages = []
_thresholds = {}


def scenario(name, pattern=None, vus=None, thresholds=None):
    """Declare test scenario configuration."""
    global _scenario_config, _chaos_config, _stages, _thresholds
    _scenario_config = {"name": name}
    if pattern:
        _chaos_config = pattern
    if vus:
        _stages = vus
    if thresholds:
        _thresholds = thresholds


def chaos(preset="moderate", spike_factor=None, noise_amplitude=None, lambda_=None):
    """Configure chaos traffic patterns."""
    cfg = {"preset": preset}
    if spike_factor is not None:
        cfg["spike_factor"] = spike_factor
    if noise_amplitude is not None:
        cfg["noise_amplitude"] = noise_amplitude
    if lambda_ is not None:
        cfg["lambda"] = lambda_
    return cfg


def stage(duration, target):
    """Define a VU ramping stage."""
    return {"duration": duration, "target": target}


def ramp(stages):
    """Wrap stages for VU ramping."""
    return stages


# --- Check ---

def check(response, checks):
    """Run assertions against a response.

    Args:
        response: Response object from http.*
        checks: dict of {"name": lambda r: bool}
    """
    all_passed = True
    for name, fn in checks.items():
        try:
            passed = bool(fn(response))
        except Exception:
            passed = False
        _send({"type": "check", "name": name, "passed": passed})
        if not passed:
            all_passed = False
    return all_passed


# --- Sleep / Think Time ---

def sleep(duration):
    """Sleep for a duration string (e.g., '1s', '500ms') or seconds (float)."""
    if isinstance(duration, str):
        _time.sleep(_parse_duration(duration))
    else:
        _time.sleep(float(duration))


def think_time(min_dur, max_dur):
    """Generate a random duration between min and max (chaos-aware)."""
    min_s = _parse_duration(min_dur)
    max_s = _parse_duration(max_dur)
    return random.uniform(min_s, max_s)


def _parse_duration(s):
    """Parse Go-style duration string to seconds."""
    if isinstance(s, (int, float)):
        return float(s)
    s = s.strip()
    if s.endswith("ms"):
        return float(s[:-2]) / 1000
    elif s.endswith("us") or s.endswith("µs"):
        return float(s[:-2]) / 1_000_000
    elif s.endswith("s") and not s.endswith("ms"):
        return float(s[:-1])
    elif s.endswith("m"):
        return float(s[:-1]) * 60
    elif s.endswith("h"):
        return float(s[:-1]) * 3600
    return float(s)


# --- Group ---

def group(name, fn):
    """Group related requests for metric labeling."""
    return fn()


# --- Main loop (auto-starts when script is run by kar98k) ---

_caller_module = None

# Capture the importing module's globals at import time
for _frame_info in inspect.stack():
    _f = _frame_info[0]
    if _f.f_globals.get("__name__") == "__main__":
        _caller_module = _f.f_globals
        break


def _main():
    global _caller_module
    if _caller_module is None:
        _caller_module = sys.modules.get("__main__").__dict__ if "__main__" in sys.modules else {}

    setup_fn = _caller_module.get("setup")
    default_fn = _caller_module.get("default") or _caller_module.get("run")
    teardown_fn = _caller_module.get("teardown")

    if default_fn is None:
        _send({"type": "error", "message": "script must define a default() or run() function"})
        sys.exit(1)

    while True:
        cmd = _recv()

        if cmd["cmd"] == "init":
            scenario_msg = {"type": "scenario", "name": _scenario_config.get("name", "")}
            if _chaos_config:
                scenario_msg["chaos"] = _chaos_config
            if _stages:
                scenario_msg["stages"] = _stages
            if _thresholds:
                scenario_msg["thresholds"] = _thresholds
            _send(scenario_msg)
            _send({"type": "done"})

        elif cmd["cmd"] == "setup":
            data = None
            if setup_fn:
                data = setup_fn()
            _send({"type": "done", "data": data or {}})

        elif cmd["cmd"] == "iterate":
            data = cmd.get("data", {})
            try:
                default_fn(data)
            except Exception as e:
                _send({"type": "error", "message": str(e)})
            _send({"type": "done"})

        elif cmd["cmd"] == "teardown":
            data = cmd.get("data", {})
            if teardown_fn:
                try:
                    teardown_fn(data)
                except Exception:
                    pass
            _send({"type": "done"})

        else:
            _send({"type": "error", "message": f"unknown command: {cmd['cmd']}"})


# Auto-start: when imported by a kar98k subprocess, block on the main loop
# after the importing module finishes defining setup/default/teardown.
def _auto_start():
    """Called via atexit — runs the protocol loop on the main thread."""
    # Only activate when stdin is a pipe (i.e., run by kar98k)
    if sys.stdin.isatty():
        return
    _main()

atexit.register(_auto_start)
