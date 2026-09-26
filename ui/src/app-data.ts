import { loadDeviceActivityFromWeb, type DeviceActivityList } from "./activity/activity";
import { loadCoverageFromWeb, type CoverageBundle } from "./coverage/bundle";
import { loadDevicesFromWeb, type DeviceList } from "./devices/devices";
import { loadNetworksFromWeb, type NetworkList } from "./setup/setup";
import { loadToolsFromWeb, type ToolsSnapshot } from "./tools/tools";
import { loadStorageOverviewFromWeb, type StorageOverview } from "./tools/storage";

export interface AppData {
  coverage: CoverageBundle;
  devices: DeviceList | null;
  devices_error?: string;
  activity: DeviceActivityList | null;
  activity_error?: string;
  tools: ToolsSnapshot | null;
  tools_error?: string;
  storage?: StorageOverview | null;
  storage_error?: string;
  networks: NetworkList;
  network_error?: string;
}

export async function loadAppDataFromWeb(): Promise<AppData> {
  // Coverage establishes the one-time browser session from the URL fragment.
  // Core evidence reads stay authoritative even when secondary setup/activity/tools
  // projections are temporarily unavailable.
  const coverage = await loadCoverageFromWeb();
  const devicesPromise = loadDevicesFromWeb()
    .then((devices) => ({ devices }))
    .catch(() => ({ devices: null, devices_error: "Device evidence is temporarily unavailable." }));
  const activityPromise = loadDeviceActivityFromWeb()
    .then((activity) => ({ activity }))
    .catch(() => ({
      activity: null,
      activity_error: "Device activity is temporarily unavailable. Retry the read or reopen the authenticated local URL if the session expired.",
    }));
  const toolsPromise = loadToolsFromWeb()
    .then((tools) => ({ tools }))
    .catch(() => ({
      tools: null,
      tools_error: "Tool information is temporarily unavailable. Retry the read or reopen the authenticated local URL if the session expired.",
    }));
  const storagePromise = loadStorageOverviewFromWeb()
    .then((storage) => ({ storage }))
    .catch(() => ({ storage: null, storage_error: "Storage information is temporarily unavailable." }));
  const networksPromise = loadNetworksFromWeb()
    .then((networks) => ({ networks }))
    .catch(() => ({
      networks: { candidates: [], candidates_truncated: false } as NetworkList,
      network_error: "Network setup information is temporarily unavailable. Retry the read or reopen the authenticated local URL if the session expired.",
    }));
  const [deviceState, activityState, toolsState, storageState, networkState] = await Promise.all([devicesPromise, activityPromise, toolsPromise, storagePromise, networksPromise]);
  return { coverage, ...deviceState, ...activityState, ...toolsState, ...storageState, ...networkState };
}
