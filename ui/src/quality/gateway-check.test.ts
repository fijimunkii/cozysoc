import { describe, expect, it } from "vitest";
import { parseGatewayCheckResult, parseGatewayCheckReview } from "./gateway-check";

const review = {
  review_id: "a".repeat(43), created_at: "2026-09-25T12:00:00Z", expires_at: "2026-09-25T12:00:30Z",
  target: "192.168.50.1", source: "192.168.50.23", interface_name: "en0", interface_index: 4,
  prefixes: ["192.168.50.0/24"], budget: { max_attempts: 3, min_interval_ms: 1000, attempt_timeout_ms: 1000,
    total_timeout_ms: 5000, payload_bytes: 32, max_icmp_request_bytes: 120, max_concurrent_runs: 1, min_run_interval_ms: 60000 },
};

describe("browser gateway response boundary", () => {
  it("accepts the fixed review but rejects a widened budget or hidden approval data", () => {
    expect(parseGatewayCheckReview(review).target).toBe("192.168.50.1");
    expect(() => parseGatewayCheckReview({ ...review, budget: { ...review.budget, max_attempts: 4 } })).toThrow();
    expect(() => parseGatewayCheckReview({ ...review, challenge: "secret" })).toThrow();
    expect(() => parseGatewayCheckReview({ ...review, target: "8.8.8.8" })).toThrow();
  });

  it("keeps execution and measurement separate and rejects impossible counts", () => {
    const completed = { outcome: "completed", run_id: "b".repeat(32), measurement: {
      started_at: "2026-09-25T12:00:01Z", completed_at: "2026-09-25T12:00:04Z", send_calls: 3,
      accepted_requests: 3, replies: 2, timeouts: 1, complete: true, mean_rtt_ns: 100000000,
    } };
    expect(parseGatewayCheckResult(completed).measurement?.replies).toBe(2);
    expect(() => parseGatewayCheckResult({ ...completed, measurement: { ...completed.measurement, replies: 3 } })).toThrow();
    expect(() => parseGatewayCheckResult({ outcome: "completed", run_id: "b".repeat(32) })).toThrow();
    expect(parseGatewayCheckResult({ outcome: "declined" })).toEqual({ outcome: "declined" });
  });
});
