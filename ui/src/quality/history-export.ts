import { parseGatewayHistory, type GatewayHistory } from "./gateway-history";
import { parseResolverHistory, type ResolverHistory } from "./resolver-history";
import { parseHTTPSHistory, type HTTPSHistory } from "./https-history";

function encode(format: string, snapshot: GatewayHistory | ResolverHistory | HTTPSHistory): string {
  return `${JSON.stringify({ format, version: 1, snapshot }, null, 2)}\n`;
}

// Revalidate at the export boundary so the file contains only the browser history contract.
export function gatewayHistoryExportJSON(history: GatewayHistory): string {
  return encode("cozysoc-gateway-history", parseGatewayHistory(history));
}

export function resolverHistoryExportJSON(history: ResolverHistory): string {
  return encode("cozysoc-resolver-history", parseResolverHistory(history));
}

export function httpsHistoryExportJSON(history: HTTPSHistory): string {
  return encode("cozysoc-https-history", parseHTTPSHistory(history));
}
