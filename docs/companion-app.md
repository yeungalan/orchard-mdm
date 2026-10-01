# Wi-Fi and location reporting with a companion app

Apple's MDM protocol lets Orchard read a device's battery level, storage, carrier, roaming state,
MAC addresses and installed apps, and Orchard records each check-in's public IP address. It does
**not** expose the Wi-Fi network a device is connected to, and it only shares GPS location while a
supervised device is in Lost Mode.

To record the current Wi-Fi network (SSID/BSSID) and location continuously, deploy a small app of
your own that reports to Orchard's agent endpoint. Orchard stores the reports alongside the MDM data:
they show up on the device's **Battery & network** and **Location** tabs, in webhooks
(`device.telemetry`, `device.location`) and in the API.

## 1. Give the app its device token

Add the app to the catalog (in-house `.ipa` or App Store), open it, and set its **Managed app
configuration** to:

```json
{
  "OrchardReportURL": "{{server.agent_url}}",
  "OrchardDeviceToken": "{{device.agent_token}}"
}
```

Orchard fills in the variables per device when it installs the app, so every device gets its own
token. Assign the app as **Required** to the groups you want to monitor.

## 2. Report from the app

`POST {{server.agent_url}}` (that is, `https://mdm.example.com/agent/v1/report`) with
`Authorization: Bearer <OrchardDeviceToken>` and a JSON body. Every field is optional:

```json
{
  "battery": 0.82,
  "battery_state": "charging",
  "ssid": "Acme-Office",
  "bssid": "aa:bb:cc:dd:ee:ff",
  "local_ip": "10.0.4.17",
  "latitude": 35.6812,
  "longitude": 139.7671,
  "accuracy": 12,
  "altitude": 40,
  "speed": 0,
  "course": -1,
  "timestamp": 1790000000,
  "extra": { "app_version": "1.2" }
}
```

The response is `{"ok": true, "next_report_seconds": 900}`. The interval is set under
Settings → Schedule & data. `GET /agent/v1/config` with the same token returns the report URL,
interval and device name.

## Reference implementation (Swift)

Reading the SSID requires the **Access Wi-Fi Information** capability
(`com.apple.developer.networking.wifi-info`) and location permission. Ask users for consent and
explain why. Most organizations limit location reporting to corporate-owned devices.

```swift
import UIKit
import CoreLocation
import NetworkExtension

enum Orchard {
    static var config: [String: Any] {
        UserDefaults.standard.dictionary(forKey: "com.apple.configuration.managed") ?? [:]
    }
    static var reportURL: URL? { (config["OrchardReportURL"] as? String).flatMap(URL.init(string:)) }
    static var token: String? { config["OrchardDeviceToken"] as? String }
}

final class OrchardReporter: NSObject, CLLocationManagerDelegate {
    private let locations = CLLocationManager()

    func start() {
        UIDevice.current.isBatteryMonitoringEnabled = true
        locations.delegate = self
        locations.requestWhenInUseAuthorization()
        locations.startMonitoringSignificantLocationChanges()
    }

    func locationManager(_ manager: CLLocationManager, didUpdateLocations updates: [CLLocation]) {
        report(location: updates.last)
    }

    func report(location: CLLocation?) {
        guard let url = Orchard.reportURL, let token = Orchard.token else { return }
        NEHotspotNetwork.fetchCurrent { network in
            let device = UIDevice.current
            var body: [String: Any] = [
                "battery": Double(device.batteryLevel),
                "battery_state": ["unknown", "unplugged", "charging", "full"][device.batteryState.rawValue],
                "timestamp": Int(Date().timeIntervalSince1970),
            ]
            if let network {
                body["ssid"] = network.ssid
                body["bssid"] = network.bssid
            }
            if let location {
                body["latitude"] = location.coordinate.latitude
                body["longitude"] = location.coordinate.longitude
                body["accuracy"] = location.horizontalAccuracy
                body["altitude"] = location.altitude
                body["speed"] = location.speed
                body["course"] = location.course
            }
            var request = URLRequest(url: url)
            request.httpMethod = "POST"
            request.setValue("Bearer \(token)", forHTTPHeaderField: "Authorization")
            request.setValue("application/json", forHTTPHeaderField: "Content-Type")
            request.httpBody = try? JSONSerialization.data(withJSONObject: body)
            URLSession.shared.dataTask(with: request).resume()
        }
    }
}
```

Tokens are rotated by re-enrolling the device. A report with an unknown token gets `401`.
