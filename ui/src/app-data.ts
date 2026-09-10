import { loadCoverageFromWeb, type CoverageBundle } from "./coverage/bundle";
import { loadDevicesFromWeb, type DeviceList } from "./devices/devices";
import { loadNetworksFromWeb, type NetworkList } from "./setup/setup";

export interface AppData {
  coverage: CoverageBundle;
  devices: DeviceList;
  networks: NetworkList;
  network_error?: string;
}

export async function loadAppDataFromWeb(): Promise<AppData> {
  // Coverage establishes the one-time browser session from the URL fragment.
  // Core evidence reads stay authoritative even when setup-specific network
  // enumeration is temporarily unavailable.
  const coverage = await loadCoverageFromWeb();
  const devicesPromise = loadDevicesFromWeb();
  const networksPromise = loadNetworksFromWeb()
    .then((networks) => ({ networks }))
    .catch((error: unknown) => ({
      networks: { candidates: [], candidates_truncated: false } as NetworkList,
      network_error: error instanceof Error ? error.message : "Network setup information is unavailable.",
    }));
  const [devices, networkState] = await Promise.all([devicesPromise, networksPromise]);
  return { coverage, devices, ...networkState };
}
