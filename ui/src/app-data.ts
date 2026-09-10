import { loadDeviceActivityFromWeb, type DeviceActivityList } from "./activity/activity";
import { loadCoverageFromWeb, type CoverageBundle } from "./coverage/bundle";
import { loadDevicesFromWeb, type DeviceList } from "./devices/devices";
import { loadNetworksFromWeb, type NetworkList } from "./setup/setup";

export interface AppData {
  coverage: CoverageBundle;
  devices: DeviceList;
  activity: DeviceActivityList | null;
  activity_error?: string;
  networks: NetworkList;
  network_error?: string;
}

export async function loadAppDataFromWeb(): Promise<AppData> {
  // Coverage establishes the one-time browser session from the URL fragment.
  // Core evidence reads stay authoritative even when secondary setup/activity
  // projections are temporarily unavailable.
  const coverage = await loadCoverageFromWeb();
  const devicesPromise = loadDevicesFromWeb();
  const activityPromise = loadDeviceActivityFromWeb()
    .then((activity) => ({ activity }))
    .catch((error: unknown) => ({
      activity: null,
      activity_error: error instanceof Error ? error.message : "Device activity is unavailable.",
    }));
  const networksPromise = loadNetworksFromWeb()
    .then((networks) => ({ networks }))
    .catch((error: unknown) => ({
      networks: { candidates: [], candidates_truncated: false } as NetworkList,
      network_error: error instanceof Error ? error.message : "Network setup information is unavailable.",
    }));
  const [devices, activityState, networkState] = await Promise.all([devicesPromise, activityPromise, networksPromise]);
  return { coverage, devices, ...activityState, ...networkState };
}
