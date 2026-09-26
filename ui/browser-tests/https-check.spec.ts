import AxeBuilder from "@axe-core/playwright";
import {expect,test} from "@playwright/test";
import {demoCapabilitiesRaw,demoStatusRaw} from "../src/demo/tools";

test("saved HTTPS check reveals exact private request only on review and needs one approval",async({page})=>{
 const decisions:unknown[]=[];let selections=0,reviews=0;
 const selected=`https-selection.${"a".repeat(32)}`;
 const settings={endpoint:"192.168.50.53:443",server_name:"private.example",request_target:"/check?test=1",family:"ipv4",method:"HEAD",expected_status:204,destination_policy:"exact-endpoint"};
 const request="HEAD /check?test=1 HTTP/1.1\r\nHost: private.example\r\n\r\n";
 const policy={alpn:"http/1.1",trust_store:"system",client_authentication:false,http_version:"HTTP/1.1",min_tls_version:"TLS 1.2",max_tls_version:"TLS 1.3",verify_server_identity:true,fresh_connection:true,session_resumption:false,early_data:false,use_proxy:false,resolve_names:false,follow_redirects:false,read_response_body:false};
 const budget={max_connections:1,max_requests:1,max_retries:0,max_request_bytes:new TextEncoder().encode(request).length,max_response_header_bytes:16384,max_transport_read_bytes:131072,max_transport_write_bytes:32768,max_transport_read_calls:512,max_transport_write_calls:64,connect_timeout_ms:2000,tls_handshake_timeout_ms:3000,response_header_timeout_ms:2000,total_timeout_ms:8000,max_concurrent_runs:1,min_run_interval_ms:60000};
 await page.route("**/api/**",(route)=>{
  const path=new URL(route.request().url()).pathname;const reply=(v:unknown,status=200)=>route.fulfill({status,contentType:"application/json",body:JSON.stringify(v)});const now=new Date();
  switch(path){
   case "/api/session":return reply({csrf_token:"c".repeat(43)});
   case "/api/coverage":return reply({as_of:now.toISOString(),reports:[]});
   case "/api/devices":return reply({configured:false,as_of:now.toISOString(),devices:[],truncated:false});
   case "/api/status":return reply(demoStatusRaw);
   case "/api/capabilities":return reply(demoCapabilitiesRaw);
   case "/api/network-quality/https/selections":selections++;return reply({items:[{selection_id:selected,settings}]});
   case "/api/network-quality/https/review":
    reviews++;expect(route.request().postDataJSON()).toEqual({selection_id:selected});expect(route.request().headers()["x-cozy-csrf"]).toBe("c".repeat(43));
    return reply({review_id:"b".repeat(43),selection_id:selected,settings,created_at:now.toISOString(),expires_at:new Date(now.getTime()+30000).toISOString(),source:"192.168.50.23",interface_name:"en0",interface_index:4,prefixes:["192.168.50.0/24"],outside_enrolled_prefixes:false,route_observed_at:now.toISOString(),route_fresh_until:new Date(now.getTime()+30000).toISOString(),policy,request_bytes:request,privacy:["Operator sees request","TLS name visible","Byte counts exclude IP overhead","Route is not guaranteed"],budget});
   case "/api/network-quality/https/run":decisions.push(route.request().postDataJSON());return reply({outcome:"blocked",run_id:"d".repeat(32),failure_code:"precondition_failed"});
   default:return reply({error:"unavailable"},503);
  }
 });
 await page.setViewportSize({width:320,height:740});await page.goto("/");await expect(page.getByRole("status",{name:"Live controller data"})).toBeVisible();
 expect(selections).toBe(0);await page.getByRole("button",{name:"Load saved HTTPS selections"}).click();expect(selections).toBe(1);expect(reviews).toBe(0);
 await page.getByRole("button",{name:"Review one-shot HTTPS check"}).click();
 const review=page.getByLabel("HTTPS check review");await expect(review).toContainText("TLS 1.2–TLS 1.3");await expect(review.locator("pre code")).toHaveText(JSON.stringify(request));
 await expect(review).toContainText("Operator sees request");expect(decisions).toHaveLength(0);
 await page.evaluate(()=>{document.documentElement.style.fontSize="200%"});
 const widths=await page.evaluate(()=>({viewport:window.innerWidth,content:document.documentElement.scrollWidth}));expect(widths.content).toBeLessThanOrEqual(widths.viewport+1);
 const a11y=await new AxeBuilder({page}).withTags(["wcag2a","wcag2aa","wcag21a","wcag21aa"]).analyze();expect(a11y.violations).toEqual([]);
 await page.getByRole("button",{name:"Approve one HTTPS check"}).click();await expect(page.getByText(/Check outcome:/)).toBeVisible();
 expect(decisions).toEqual([{review_id:"b".repeat(43),approve:true}]);
});
