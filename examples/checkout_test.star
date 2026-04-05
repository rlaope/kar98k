# checkout_test.star - Multi-step user flow with chaos patterns

scenario(
    name = "checkout-flow",
    pattern = chaos(
        preset = "aggressive",
        spike_factor = 3.0,
    ),
    vus = ramp([
        stage("10s", 5),
        stage("30s", 20),
        stage("10s", 0),
    ]),
    thresholds = {
        "http_req_duration{p95}": "< 1000ms",
        "http_req_failed": "< 0.1",
        "checks": "> 0.9",
    },
)

def setup():
    resp = http.post("http://localhost:8080/api/echo", json = {
        "action": "auth",
        "user": "loadtest",
    })
    return {"session": "test-session-id"}

def default(data):
    headers = {"X-Session": data["session"]}

    # Step 1: List products
    resp = http.get("http://localhost:8080/api/users", headers = headers)
    check(resp, {
        "list ok": lambda r: r.status == 200,
    })

    # Think time — compresses during chaos spikes
    sleep(think_time("500ms", "2s"))

    # Step 2: Get stats
    resp = http.get("http://localhost:8080/api/stats", headers = headers)
    check(resp, {
        "stats ok": lambda r: r.status == 200,
    })

def teardown(data):
    http.post("http://localhost:8080/api/echo", json = {"action": "logout"})
