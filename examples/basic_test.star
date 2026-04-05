# basic_test.star - Basic load test example (Starlark)

scenario(
    name = "basic-health-check",
    pattern = chaos(preset = "gentle"),
    thresholds = {
        "http_req_duration{p95}": "< 500ms",
        "http_req_failed": "< 0.05",
    },
)

def default(data):
    resp = http.get("http://localhost:8080/health")
    check(resp, {
        "status is 200": lambda r: r.status == 200,
    })
