import {act,cleanup,fireEvent,render,screen} from "@testing-library/react";
import {afterEach,beforeEach,describe,expect,it,vi} from "vitest";
import {HTTPSCheckPanel} from "./HTTPSCheckPanel";
import {parseHTTPSReview,type HTTPSCheckClient,type HTTPSSelection} from "./https-check";
const start=new Date("2026-09-25T12:00:00Z");
const selection:HTTPSSelection={selection_id:`https-selection.${"a".repeat(32)}`,settings:{endpoint:"192.168.50.53:443",server_name:"private.example",request_target:"/check?test=1",family:"ipv4",method:"HEAD",expected_status:204,destination_policy:"exact-endpoint"}};
const request="HEAD /check?test=1 HTTP/1.1\r\nHost: private.example\r\n\r\n";
const policy={alpn:"http/1.1",trust_store:"system",client_authentication:false,http_version:"HTTP/1.1",min_tls_version:"TLS 1.2",max_tls_version:"TLS 1.3",verify_server_identity:true,fresh_connection:true,session_resumption:false,early_data:false,use_proxy:false,resolve_names:false,follow_redirects:false,read_response_body:false};
const budget={max_connections:1,max_requests:1,max_retries:0,max_request_bytes:new TextEncoder().encode(request).length,max_response_header_bytes:16384,max_transport_read_bytes:131072,max_transport_write_bytes:32768,max_transport_read_calls:512,max_transport_write_calls:64,connect_timeout_ms:2000,tls_handshake_timeout_ms:3000,response_header_timeout_ms:2000,total_timeout_ms:8000,max_concurrent_runs:1,min_run_interval_ms:60000};
const review=()=>parseHTTPSReview({review_id:"b".repeat(43),...selection,created_at:start.toISOString(),expires_at:new Date(start.getTime()+30000).toISOString(),source:"192.168.50.23",interface_name:"en0",interface_index:4,prefixes:["192.168.50.0/24"],outside_enrolled_prefixes:false,route_observed_at:start.toISOString(),route_fresh_until:new Date(start.getTime()+30000).toISOString(),policy,request_bytes:request,privacy:["Operator sees request","TLS name visible","Byte counts exclude IP overhead","Route is not guaranteed"],budget});
beforeEach(()=>{vi.useFakeTimers();vi.setSystemTime(start)});
afterEach(()=>{cleanup();vi.useRealTimers();vi.restoreAllMocks()});
async function prepare(client:HTTPSCheckClient){render(<HTTPSCheckPanel mode="live" client={client} loadSelections={async()=>[selection]}/>);await act(async()=>{fireEvent.click(screen.getByRole("button",{name:"Load saved HTTPS selections"}))});await act(async()=>{fireEvent.click(screen.getByRole("button",{name:"Review one-shot HTTPS check"}))})}
describe("HTTPS check consent panel",()=>{
 it("shows exact request, TLS policy, budget and privacy before approval",async()=>{
  const client={reviewHTTPS:vi.fn(async()=>review()),decideHTTPS:vi.fn(async()=>({outcome:"blocked" as const,run_id:"d".repeat(32),failure_code:"precondition_failed"}))};
  await prepare(client);expect(client.decideHTTPS).not.toHaveBeenCalled();
  expect(document.querySelector(".https-check-request code")?.textContent).toBe(JSON.stringify(request));
  expect(screen.getByText(/TLS 1.2–TLS 1.3/)).toBeTruthy();
  expect(screen.getByText(/131072 transport read bytes/)).toBeTruthy();
  expect(screen.getByText(/Operator sees request/)).toBeTruthy();
  await act(async()=>{fireEvent.click(screen.getByRole("button",{name:"Approve one HTTPS check"}))});
  expect(client.decideHTTPS).toHaveBeenCalledExactlyOnceWith("b".repeat(43),true);
  expect(screen.getByText(/Run reference:/)).toBeTruthy();
 });
 it("disables approval after expiry and allows decline",async()=>{
  const client={reviewHTTPS:vi.fn(async()=>review()),decideHTTPS:vi.fn(async()=>({outcome:"declined" as const}))};
  await prepare(client);await act(async()=>{vi.advanceTimersByTime(30000)});
  expect((screen.getByRole("button",{name:"Approve one HTTPS check"}) as HTMLButtonElement).disabled).toBe(true);
  await act(async()=>{fireEvent.click(screen.getByRole("button",{name:"Decline check"}))});expect(client.decideHTTPS).toHaveBeenCalledExactlyOnceWith("b".repeat(43),false);
 });
 it("disables approval when route evidence expires first",async()=>{
  const client={reviewHTTPS:vi.fn(async()=>({...review(),route_observed_at:new Date(start.getTime()-29000).toISOString(),route_fresh_until:new Date(start.getTime()+1000).toISOString()})),decideHTTPS:vi.fn(async()=>({outcome:"declined" as const}))};
  await prepare(client);await act(async()=>{vi.advanceTimersByTime(1000)});
  expect((screen.getByRole("button",{name:"Approve one HTTPS check"}) as HTMLButtonElement).disabled).toBe(true);
  expect(client.decideHTTPS).not.toHaveBeenCalled();
 });
 it("never retries an uncertain approval or exposes private errors",async()=>{
  const client={reviewHTTPS:vi.fn(async()=>review()),decideHTTPS:vi.fn(async()=>{throw new Error("private path")})};
  await prepare(client);await act(async()=>{fireEvent.click(screen.getByRole("button",{name:"Approve one HTTPS check"}))});
  expect(screen.getByRole("alert").textContent).toContain("HTTPS traffic may have been sent");expect(screen.queryByText(/private path/)).toBeNull();expect(client.decideHTTPS).toHaveBeenCalledTimes(1);
 });
 it("keeps demo inert",()=>{render(<HTTPSCheckPanel mode="demo" client={{reviewHTTPS:async()=>review(),decideHTTPS:async()=>({outcome:"declined"})}}/>);expect(screen.queryByRole("button")).toBeNull()});
});
