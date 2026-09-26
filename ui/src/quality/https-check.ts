export interface HTTPSSettings {
  endpoint: string; server_name: string; request_target: string;
  family: "ipv4" | "ipv6"; method: "HEAD" | "GET"; expected_status: number;
  destination_policy: "exact-endpoint";
}
export interface HTTPSSelection { selection_id: string; settings: HTTPSSettings }
export interface HTTPSCheckReview extends HTTPSSelection {
  review_id: string; created_at: string; expires_at: string; source: string;
  interface_name: string; interface_index: number; prefixes: string[];
  outside_enrolled_prefixes: boolean; route_observed_at: string; route_fresh_until: string;
  policy: HTTPSPolicy; request_bytes: string; privacy: string[]; budget: HTTPSBudget;
}
export interface HTTPSPolicy {
  alpn: string; trust_store: string; client_authentication: boolean; http_version: string;
  min_tls_version: string; max_tls_version: string; verify_server_identity: boolean;
  fresh_connection: boolean; session_resumption: boolean; early_data: boolean;
  use_proxy: boolean; resolve_names: boolean; follow_redirects: boolean; read_response_body: boolean;
}
export interface HTTPSBudget {
  max_connections:number; max_requests:number; max_retries:number; max_request_bytes:number;
  max_response_header_bytes:number; max_transport_read_bytes:number; max_transport_write_bytes:number;
  max_transport_read_calls:number; max_transport_write_calls:number;
  connect_timeout_ms:number; tls_handshake_timeout_ms:number; response_header_timeout_ms:number;
  total_timeout_ms:number; max_concurrent_runs:number; min_run_interval_ms:number;
}
export interface HTTPSCheckResult { outcome:"declined"|"completed"|"failed"|"canceled"|"indeterminate"|"blocked"; run_id?:string; failure_code?:string }
export interface HTTPSCheckClient { reviewHTTPS(selectionID:string):Promise<HTTPSCheckReview>; decideHTTPS(reviewID:string,approve:boolean):Promise<HTTPSCheckResult> }
const selectionID=/^https-selection\.[0-9a-f]{32}$/;
function invalid():never {throw new Error("HTTPS check response is invalid.")}
function exact(raw:unknown,required:string[],optional:string[]=[]):Record<string,unknown>{
  if(raw===null||typeof raw!=="object"||Array.isArray(raw))return invalid();
  const v=raw as Record<string,unknown>;
  if(required.some((k)=>!Object.hasOwn(v,k))||Object.keys(v).some((k)=>!required.includes(k)&&!optional.includes(k)))return invalid();
  return v;
}
function int(raw:unknown,low:number,high:number):number {if(typeof raw!=="number"||!Number.isSafeInteger(raw)||raw<low||raw>high)return invalid();return raw}
function time(raw:unknown):string {if(typeof raw!=="string"||!/^\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d(?:\.\d{1,9})?Z$/.test(raw)||!Number.isFinite(Date.parse(raw)))return invalid();return raw}
function settings(raw:unknown):HTTPSSettings{
  const v=exact(raw,["endpoint","server_name","request_target","family","method","expected_status","destination_policy"]);
  if(typeof v.endpoint!=="string"||v.endpoint.length>64||!/^(?:\[[0-9a-f:]+\]|[0-9.]+):443$/.test(v.endpoint)||
    typeof v.server_name!=="string"||v.server_name.length>253||!/^[-a-z0-9.]+$/.test(v.server_name)||
    typeof v.request_target!=="string"||v.request_target.length<1||v.request_target.length>1024||!v.request_target.startsWith("/")||v.request_target.startsWith("//")||
    /[\u0000-\u001f\u007f\\]/.test(v.request_target)||
    (v.family!=="ipv4"&&v.family!=="ipv6")||(v.method!=="HEAD"&&v.method!=="GET")||int(v.expected_status,100,599)<100||v.destination_policy!=="exact-endpoint")return invalid();
  return v as unknown as HTTPSSettings;
}
function selection(raw:unknown):HTTPSSelection{
  const v=exact(raw,["selection_id","settings"]);
  if(typeof v.selection_id!=="string"||!selectionID.test(v.selection_id))return invalid();
  return {selection_id:v.selection_id,settings:settings(v.settings)};
}
export function parseHTTPSSelections(raw:unknown):HTTPSSelection[]{
  const v=exact(raw,["items"]);if(!Array.isArray(v.items)||v.items.length>16)return invalid();
  const items=v.items.map(selection);if(new Set(items.map((i)=>i.selection_id)).size!==items.length)return invalid();return items;
}
function policy(raw:unknown):HTTPSPolicy{
  const keys=["alpn","trust_store","client_authentication","http_version","min_tls_version","max_tls_version","verify_server_identity","fresh_connection","session_resumption","early_data","use_proxy","resolve_names","follow_redirects","read_response_body"];
  const v=exact(raw,keys);
  const expected:HTTPSPolicy={alpn:"http/1.1",trust_store:"system",client_authentication:false,http_version:"HTTP/1.1",min_tls_version:"TLS 1.2",max_tls_version:"TLS 1.3",verify_server_identity:true,fresh_connection:true,session_resumption:false,early_data:false,use_proxy:false,resolve_names:false,follow_redirects:false,read_response_body:false};
  if(keys.some((k)=>v[k]!==expected[k as keyof HTTPSPolicy]))return invalid();return expected;
}
function budget(raw:unknown):HTTPSBudget{
  const keys=["max_connections","max_requests","max_retries","max_request_bytes","max_response_header_bytes","max_transport_read_bytes","max_transport_write_bytes","max_transport_read_calls","max_transport_write_calls","connect_timeout_ms","tls_handshake_timeout_ms","response_header_timeout_ms","total_timeout_ms","max_concurrent_runs","min_run_interval_ms"];
  const v=exact(raw,keys);
  const fixed:Omit<HTTPSBudget,"max_request_bytes">={max_connections:1,max_requests:1,max_retries:0,max_response_header_bytes:16384,max_transport_read_bytes:131072,max_transport_write_bytes:32768,max_transport_read_calls:512,max_transport_write_calls:64,connect_timeout_ms:2000,tls_handshake_timeout_ms:3000,response_header_timeout_ms:2000,total_timeout_ms:8000,max_concurrent_runs:1,min_run_interval_ms:60000};
  if(Object.keys(fixed).some((k)=>v[k]!==fixed[k as keyof typeof fixed]))return invalid();
  return {...fixed,max_request_bytes:int(v.max_request_bytes,1,4096)};
}
export function parseHTTPSReview(raw:unknown):HTTPSCheckReview{
  const v=exact(raw,["review_id","selection_id","settings","created_at","expires_at","source","interface_name","interface_index","prefixes","outside_enrolled_prefixes","route_observed_at","route_fresh_until","policy","request_bytes","privacy","budget"]);
  if(typeof v.review_id!=="string"||!/^[A-Za-z0-9_-]{43}$/.test(v.review_id)||typeof v.selection_id!=="string"||!selectionID.test(v.selection_id)||
    typeof v.source!=="string"||v.source.length>64||!/^[0-9a-f:.]+$/.test(v.source)||typeof v.interface_name!=="string"||!/^[A-Za-z0-9][A-Za-z0-9_.:-]{0,63}$/.test(v.interface_name)||
    !Array.isArray(v.prefixes)||v.prefixes.length<1||v.prefixes.length>64||v.prefixes.some((p)=>typeof p!=="string"||p.length>128||!/^[0-9a-f:./]+$/.test(p))||
    typeof v.outside_enrolled_prefixes!=="boolean"||typeof v.request_bytes!=="string"||v.request_bytes.length>4096||!Array.isArray(v.privacy)||v.privacy.length!==4||v.privacy.some((p)=>typeof p!=="string"||p.length<1||p.length>600))return invalid();
  const created=time(v.created_at),expires=time(v.expires_at),observed=time(v.route_observed_at),fresh=time(v.route_fresh_until);
  if(Date.parse(expires)-Date.parse(created)!==30000||Date.parse(fresh)-Date.parse(observed)!==30000||Date.parse(observed)>Date.parse(created))return invalid();
  const s=settings(v.settings),b=budget(v.budget);
  if(!v.request_bytes.startsWith(`${s.method} ${s.request_target} HTTP/1.1\r\n`)||!v.request_bytes.includes(`\r\nHost: ${s.server_name}\r\n`)||b.max_request_bytes!==new TextEncoder().encode(v.request_bytes).length)return invalid();
  return {review_id:v.review_id,selection_id:v.selection_id,settings:s,created_at:created,expires_at:expires,source:v.source,interface_name:v.interface_name,interface_index:int(v.interface_index,1,2147483647),
    prefixes:v.prefixes as string[],outside_enrolled_prefixes:v.outside_enrolled_prefixes,route_observed_at:observed,route_fresh_until:fresh,policy:policy(v.policy),request_bytes:v.request_bytes,privacy:v.privacy as string[],budget:b};
}
export function parseHTTPSResult(raw:unknown):HTTPSCheckResult{
  const v=exact(raw,["outcome"],["run_id","failure_code"]);
  if(v.outcome==="declined"){if(v.run_id!==undefined||v.failure_code!==undefined)return invalid();return {outcome:"declined"}}
  if(!["completed","failed","canceled","indeterminate","blocked"].includes(String(v.outcome))||typeof v.run_id!=="string"||!/^[0-9a-f]{32}$/.test(v.run_id))return invalid();
  const expected:Record<string,string[]>={completed:[""],failed:["execution_failed"],canceled:["canceled"],indeterminate:["execution_failed"],blocked:["precondition_failed","review_expired"]};
  const failure=v.failure_code===undefined?"":v.failure_code;
  if(typeof failure!=="string"||!expected[String(v.outcome)]?.includes(failure))return invalid();
  return {outcome:v.outcome as HTTPSCheckResult["outcome"],run_id:v.run_id,...(failure?{failure_code:failure}:{})};
}
