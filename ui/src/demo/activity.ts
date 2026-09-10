export const demoActivityRaw = {
  configured: true,
  scope_id: "scope.synthetic-home",
  since: "2026-09-08T23:21:00Z",
  as_of: "2026-09-09T23:21:00Z",
  items: [
    { id: "obs.demo-laptop-latest", kind: "observed", at: "2026-09-09T23:18:00Z", device_id: "device.demo-laptop", user_label: "Work Laptop", address_family: "ipv4", address: "192.0.2.42", hardware_address: "02:00:00:00:00:22", source: { observation_id: "obs.demo-laptop-latest", sensor_id: "sensor.demo", kind: "device-neighbor-seen", source_stream: "device-watch-neighbors", ingested_at: "2026-09-09T23:18:01Z", attribution: "device-watch:arp-cache" } },
    { id: "obs.demo-tv-change", kind: "address-changed", at: "2026-09-09T22:54:00Z", device_id: "device.demo-tv", user_label: "Living Room TV", address_family: "ipv4", address: "192.0.2.21", previous_address: "192.0.2.15", hardware_address: "02:00:00:00:00:11", source: { observation_id: "obs.demo-tv-change", sensor_id: "sensor.demo", kind: "device-neighbor-seen", source_stream: "device-watch-neighbors", ingested_at: "2026-09-09T22:54:01Z", attribution: "device-watch:arp-cache" } },
  ],
  truncated: false,
};
