// basic_test.js - Basic load test example (JavaScript)

scenario({
    name: "basic-health-check",
    pattern: chaos({ preset: "gentle" }),
    thresholds: {
        "http_req_duration{p95}": "< 500ms",
        "http_req_failed": "< 0.05",
    },
});

// Main iteration function — called per VU
function run(data) {
    var resp = http.get("http://localhost:8080/health");
    check(resp, {
        "status is 200": function(r) { return r.status === 200; },
    });
}
