import AxeBuilder from "@axe-core/playwright";
import { expect, test } from "@playwright/test";
import { demoCapabilitiesRaw, demoStatusRaw } from "../src/demo/tools";

test("saved DNS check reveals private selection on request and needs a separate approval", async ({ page }) => {
  const decisions: unknown[] = [];
  let selections = 0, reviews = 0;
  const selected = `selection.${"a".repeat(32)}`;
  const settings = { endpoint:"192.168.50.53:53", name:"example.invalid.", family:"ipv4", transport:"udp", query_type:"A", expect:"answer", destination_scope:"enrolled-prefix" };
  await page.route("**/api/**", (route) => {
    const path = new URL(route.request().url()).pathname;
    const reply = (value:unknown,status=200)=>route.fulfill({status,contentType:"application/json",body:JSON.stringify(value)});
    const now = new Date();
    switch(path) {
      case "/api/session": return reply({csrf_token:"c".repeat(43)});
      case "/api/coverage": return reply({as_of:now.toISOString(),reports:[]});
      case "/api/devices": return reply({configured:false,as_of:now.toISOString(),devices:[],truncated:false});
      case "/api/status": return reply(demoStatusRaw);
      case "/api/capabilities": return reply(demoCapabilitiesRaw);
      case "/api/network-quality/resolver/selections":
        selections++;return reply({items:[{selection_id:selected,settings}]});
      case "/api/network-quality/resolver/review":
        reviews++;expect(route.request().postDataJSON()).toEqual({selection_id:selected});
        expect(route.request().headers()["x-cozy-csrf"]).toBe("c".repeat(43));
        return reply({review_id:"b".repeat(43),selection_id:selected,settings,created_at:now.toISOString(),expires_at:new Date(now.getTime()+30000).toISOString(),
          source:"192.168.50.23",interface_name:"en0",interface_index:4,prefixes:["192.168.50.0/24"],outside_enrolled_prefixes:false,may_forward_upstream:true,
          budget:{max_send_calls:1,max_request_bytes:33,max_reply_bytes:512,max_received_datagrams:16,max_receive_calls:512,exchange_timeout_ms:2000,total_timeout_ms:5000,max_concurrent_runs:1,min_run_interval_ms:60000}});
      case "/api/network-quality/resolver/run": decisions.push(route.request().postDataJSON());return reply({outcome:"blocked",run_id:"d".repeat(32),failure_code:"precondition_failed"});
      default: return reply({error:"unavailable"},503);
    }
  });
  await page.setViewportSize({width:320,height:740});await page.goto("/");
  await expect(page.getByRole("status",{name:"Live controller data"})).toBeVisible();
  expect(selections).toBe(0);
  await page.getByRole("button",{name:"Load saved resolver selections"}).click();
  expect(selections).toBe(1);expect(reviews).toBe(0);
  await page.getByRole("button",{name:"Review one-shot DNS check"}).click();
  await expect(page.getByLabel("DNS resolver check review")).toContainText("A example.invalid.; expected answer");
  await expect(page.getByLabel("DNS resolver check review")).toContainText("may forward this exact query upstream");
  expect(decisions).toHaveLength(0);
  await page.evaluate(()=>{document.documentElement.style.fontSize="200%"});
  const widths=await page.evaluate(()=>({viewport:window.innerWidth,content:document.documentElement.scrollWidth}));
  expect(widths.content).toBeLessThanOrEqual(widths.viewport+1);
  const a11y=await new AxeBuilder({page}).withTags(["wcag2a","wcag2aa","wcag21a","wcag21aa"]).analyze();
  expect(a11y.violations).toEqual([]);
  await page.getByRole("button",{name:"Approve one DNS check"}).click();
  await expect(page.getByText(/Check outcome:/)).toBeVisible();
  expect(decisions).toEqual([{review_id:"b".repeat(43),approve:true}]);
});
