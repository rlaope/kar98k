#!/usr/bin/env ruby
# k6-style load test written in Ruby.

require_relative "../sdk/ruby/kar98k"

scenario name: "ruby-api-test",
         pattern: chaos(preset: "moderate", spike_factor: 2.0),
         thresholds: {
           "http_req_duration{p95}" => "< 500ms",
           "http_req_failed" => "< 0.05"
         }

def setup
  { "session" => "rb-session-xyz" }
end

def default(data)
  # GET health
  resp = Http.get("http://localhost:8080/health")
  check resp,
    "health status 200" => ->(r) { r.status == 200 },
    "has status field"  => ->(r) { r.json&.key?("status") }

  sleep_dur think_time("100ms", "500ms")

  # GET users
  resp = Http.get("http://localhost:8080/api/users")
  check resp,
    "users status 200" => ->(r) { r.status == 200 }
end

def teardown(data)
end
