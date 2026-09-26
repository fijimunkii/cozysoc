import { act, cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { ResolverCheckPanel } from "./ResolverCheckPanel";
import { parseResolverReview, type ResolverCheckClient, type ResolverSelection } from "./resolver-check";

const start = new Date("2026-09-25T12:00:00Z");
const selection: ResolverSelection = { selection_id:`selection.${"a".repeat(32)}`, settings:{ endpoint:"192.168.50.53:53", name:"example.invalid.", family:"ipv4", transport:"udp", query_type:"A", expect:"answer", destination_scope:"enrolled-prefix" } };
const review=()=>parseResolverReview({ review_id:"b".repeat(43), ...selection, created_at:start.toISOString(), expires_at:new Date(start.getTime()+30000).toISOString(),
  source:"192.168.50.23",interface_name:"en0",interface_index:4,prefixes:["192.168.50.0/24"],outside_enrolled_prefixes:false,may_forward_upstream:true,
  budget:{max_send_calls:1,max_request_bytes:33,max_reply_bytes:512,max_received_datagrams:16,max_receive_calls:512,exchange_timeout_ms:2000,total_timeout_ms:5000,max_concurrent_runs:1,min_run_interval_ms:60000} });
beforeEach(()=>{vi.useFakeTimers();vi.setSystemTime(start)});
afterEach(()=>{cleanup();vi.useRealTimers();vi.restoreAllMocks()});
async function prepare(client: ResolverCheckClient) {
  render(<ResolverCheckPanel mode="live" client={client} loadSelections={async()=>[selection]}/>);
  await act(async()=>{fireEvent.click(screen.getByRole("button",{name:"Load saved resolver selections"}))});
  await act(async()=>{fireEvent.click(screen.getByRole("button",{name:"Review one-shot DNS check"}))});
}
describe("resolver check consent panel",()=>{
  it("shows the exact question, source, budget and forwarding caveat before separate approval",async()=>{
    const client={reviewResolver:vi.fn(async()=>review()),decideResolver:vi.fn(async()=>({outcome:"failed" as const,run_id:"d".repeat(32),failure_code:"execution_failed"}))};
    await prepare(client);
    expect(client.decideResolver).not.toHaveBeenCalled();
    expect(screen.getByText(/A example.invalid.; expected answer/)).toBeTruthy();
    expect(screen.getByText(/192.168.50.23 via en0/)).toBeTruthy();
    expect(screen.getByText(/may forward this exact query upstream/)).toBeTruthy();
    expect(screen.getByText(/At most 1 UDP DNS question/)).toBeTruthy();
    await act(async()=>{fireEvent.click(screen.getByRole("button",{name:"Approve one DNS check"}))});
    expect(client.decideResolver).toHaveBeenCalledExactlyOnceWith("b".repeat(43),true);
    expect(screen.getByText(/Run reference:/)).toBeTruthy();
  });
  it("disables stale approval and allows a decline",async()=>{
    const client={reviewResolver:vi.fn(async()=>review()),decideResolver:vi.fn(async()=>({outcome:"declined" as const}))};
    await prepare(client);
    await act(async()=>{vi.advanceTimersByTime(30000)});
    expect((screen.getByRole("button",{name:"Approve one DNS check"}) as HTMLButtonElement).disabled).toBe(true);
    await act(async()=>{fireEvent.click(screen.getByRole("button",{name:"Decline check"}))});
    expect(client.decideResolver).toHaveBeenCalledExactlyOnceWith("b".repeat(43),false);
  });
  it("never retries an uncertain approval or exposes private errors",async()=>{
    const client={reviewResolver:vi.fn(async()=>review()),decideResolver:vi.fn(async()=>{throw new Error("private path")})};
    await prepare(client);
    await act(async()=>{fireEvent.click(screen.getByRole("button",{name:"Approve one DNS check"}))});
    expect(screen.getByRole("alert").textContent).toContain("DNS traffic may have been sent");
    expect(screen.queryByText(/private path/)).toBeNull();
    expect(client.decideResolver).toHaveBeenCalledTimes(1);
  });
  it("keeps demo inert",()=>{
    render(<ResolverCheckPanel mode="demo" client={{reviewResolver:async()=>review(),decideResolver:async()=>({outcome:"declined"})}}/>);
    expect(screen.queryByRole("button")).toBeNull();
  });
});
