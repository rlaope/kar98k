# kar98k Ruby SDK — write load tests in native Ruby.
#
# Usage:
#   require_relative 'kar98k'
#
#   scenario name: "my-test", pattern: chaos(preset: "aggressive")
#
#   def setup
#     { token: "abc" }
#   end
#
#   def default(data)
#     resp = Http.get("http://localhost:8080/api")
#     check resp, "status 200" => ->(r) { r.status == 200 }
#   end

require 'json'

module Kar98k
  # --- Protocol layer ---

  def self._send(msg)
    $stdout.puts(JSON.generate(msg))
    $stdout.flush
  end

  def self._recv
    line = $stdin.gets
    exit(0) if line.nil?
    JSON.parse(line.strip)
  end

  # --- Response ---

  class Response
    attr_reader :status, :body, :duration, :error

    def initialize(status:, body: "", duration: 0.0, error: "")
      @status = status
      @body = body
      @duration = duration
      @error = error
    end

    def json
      JSON.parse(@body) rescue nil
    end

    def to_s
      "<Response status=#{@status} duration=#{@duration.round(3)}s>"
    end
  end

  # --- HTTP module ---

  module Http
    def self.get(url, **opts);    _request("GET", url, **opts); end
    def self.post(url, **opts);   _request("POST", url, **opts); end
    def self.put(url, **opts);    _request("PUT", url, **opts); end
    def self.delete(url, **opts); _request("DELETE", url, **opts); end
    def self.patch(url, **opts);  _request("PATCH", url, **opts); end

    def self._request(method, url, headers: nil, json: nil, body: nil)
      msg = { type: "http", method: method, url: url }
      if headers
        msg[:headers] = headers
      end
      if json
        msg[:body] = JSON.generate(json)
        msg[:headers] = (msg[:headers] || {}).merge("Content-Type" => "application/json")
      elsif body
        msg[:body] = body
      end

      Kar98k._send(msg)

      resp_data = Kar98k._recv
      Response.new(
        status: resp_data["status"] || 0,
        body: resp_data["body"] || "",
        duration: resp_data["duration"] || 0.0,
        error: resp_data["error"] || ""
      )
    end
  end

  # --- Config state ---

  @scenario_config = {}
  @chaos_config = {}
  @stages = []
  @thresholds = {}

  def self.scenario_config; @scenario_config; end
  def self.chaos_config; @chaos_config; end
  def self.stages_config; @stages; end
  def self.thresholds_config; @thresholds; end

  # --- Main loop ---

  def self.run_loop(binding_context)
    loop do
      cmd = _recv

      case cmd["cmd"]
      when "init"
        scenario_msg = { type: "scenario", name: @scenario_config[:name] || "" }
        scenario_msg[:chaos] = @chaos_config unless @chaos_config.empty?
        scenario_msg[:stages] = @stages unless @stages.empty?
        scenario_msg[:thresholds] = @thresholds unless @thresholds.empty?
        _send(scenario_msg)
        _send({ type: "done" })

      when "setup"
        data = if binding_context.respond_to?(:setup, true)
                 binding_context.send(:setup)
               else
                 {}
               end
        _send({ type: "done", data: data || {} })

      when "iterate"
        data = cmd["data"] || {}
        begin
          if binding_context.respond_to?(:default, true)
            binding_context.send(:default, data)
          elsif binding_context.respond_to?(:run, true)
            binding_context.send(:run, data)
          end
        rescue => e
          _send({ type: "error", message: e.message })
        end
        _send({ type: "done" })

      when "teardown"
        data = cmd["data"] || {}
        binding_context.send(:teardown, data) if binding_context.respond_to?(:teardown, true)
        _send({ type: "done" })

      else
        _send({ type: "error", message: "unknown command: #{cmd['cmd']}" })
      end
    end
  end
end

# --- Top-level DSL methods (available in user scripts) ---

def scenario(name:, pattern: nil, vus: nil, thresholds: nil)
  Kar98k.scenario_config[:name] = name
  Kar98k.instance_variable_set(:@chaos_config, pattern) if pattern
  Kar98k.instance_variable_set(:@stages, vus) if vus
  Kar98k.instance_variable_set(:@thresholds, thresholds) if thresholds
end

def chaos(preset: "moderate", spike_factor: nil, noise_amplitude: nil, lambda_: nil)
  cfg = { "preset" => preset }
  cfg["spike_factor"] = spike_factor if spike_factor
  cfg["noise_amplitude"] = noise_amplitude if noise_amplitude
  cfg["lambda"] = lambda_ if lambda_
  cfg
end

def stage(duration, target)
  { "duration" => duration, "target" => target }
end

def ramp(stages)
  stages
end

def check(response, checks)
  all_passed = true
  checks.each do |name, fn|
    passed = begin
               !!fn.call(response)
             rescue
               false
             end
    Kar98k._send({ type: "check", name: name, passed: passed })
    all_passed = false unless passed
  end
  all_passed
end

def sleep_dur(duration)
  if duration.is_a?(String)
    sleep(_parse_duration(duration))
  else
    sleep(duration)
  end
end

def think_time(min_dur, max_dur)
  min_s = _parse_duration(min_dur)
  max_s = _parse_duration(max_dur)
  rand(min_s..max_s)
end

def _parse_duration(s)
  return s.to_f if s.is_a?(Numeric)
  s = s.strip
  if s.end_with?("ms")
    s[0..-3].to_f / 1000
  elsif s.end_with?("us") || s.end_with?("µs")
    s[0..-3].to_f / 1_000_000
  elsif s.end_with?("m") && !s.end_with?("ms")
    s[0..-2].to_f * 60
  elsif s.end_with?("h")
    s[0..-2].to_f * 3600
  elsif s.end_with?("s")
    s[0..-2].to_f
  else
    s.to_f
  end
end

# Alias Http to top-level
Http = Kar98k::Http

# Auto-start protocol loop when script finishes loading
at_exit do
  next if $stdin.tty?
  Kar98k.run_loop(self)
end
