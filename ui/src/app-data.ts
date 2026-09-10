import { loadCoverageFromWeb, type CoverageBundle } from "./coverage/bundle";
import { loadDevicesFromWeb, type DeviceList } from "./devices/devices";

export interface AppData {
  coverage: CoverageBundle;
  devices: DeviceList;
}

export async function loadAppDataFromWeb(): Promise<AppData> {
  const coverage = await loadCoverageFromWeb();
  const devices = await loadDevicesFromWeb();
  return { coverage, devices };
}
