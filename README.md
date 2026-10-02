# Orchard MDM

A self-hosted mobile device management server for iPhone and iPad, written in Go. It covers what
teams usually reach for Microsoft Intune or Jamf to do: enroll devices, push Wi-Fi/VPN/email and
restriction profiles, install apps, enforce compliance, run remote actions, and keep an inventory
with battery, network and location history. All of it ships as one binary with a built-in web
console and REST API.

**Orchard MDM deliberately cannot wipe devices.** See [No remote wipe](#no-remote-wipe).

![Overview: the orchard grid shows every device, shaped and colored by compliance](docs/images/dashboard.png)

## Features

**Enrollment**
- Enrollment links with QR codes. Each link sets ownership, assigned user, groups, a use limit and an expiry
- **BYOD with User Enrollment:** people add their work account in Settings ("Sign In to Work or School Account").
  Work data stays in a separate volume, and personal apps, data, identifiers and location stay private. Sign-in uses an enrollment code or
  single sign-on (OpenID Connect: Entra ID, Google, Okta…). Service discovery runs via your domain's well-known file or Apple Business Manager.
  Account-driven *device* enrollment for organization-owned devices is also supported ([guide](docs/byod.md))
- Automated Device Enrollment through Apple Business Manager / Apple School Manager: token upload,
  device sync, Setup Assistant profiles (skip screens, supervision, mandatory enrollment, await configuration) and automatic profile assignment
- SCEP-issued device identities with one-time challenges, request signing (`Mdm-Signature`) and identity renewal
- APNs push certificates: CSR generation, signing with your MDM vendor certificate or via
  [mdmcert.download](https://mdmcert.download), upload, renewal checks and expiry warnings

**Configuration**
- Profile builder with ~30 payloads: passcode, restrictions (90+ keys), Wi-Fi (incl. 802.1X), VPN (IKEv2, Cisco IPSec, app VPN),
  encrypted DNS, global proxy, cellular, mail, Exchange, Google, CalDAV/CardDAV, LDAP, web clips, Home Screen layout,
  notifications, lock screen message, Single App Mode, web content filter, managed domains, certificates, SCEP, AirPlay, AirPrint, fonts and custom payloads
- Upload existing `.mobileconfig` files (signed or unsigned)
- Per-device variables such as `{{device.serial}}`, `{{user.email}}` and `{{device.asset_tag}}` in profiles and app configuration
- Declarative Device Management (iOS 16+): declarations, automatic activations, status subscriptions (battery health,
  OS version, passcode, software update state) and per-declaration status
- Profiles are signed with the server's TLS certificate, so iOS shows them as Verified

**Apps**
- App Store apps via search, links or IDs. Apps and Books (VPP) license sync and assignment, with no Apple Account needed
- In-house `.ipa` hosting with generated install manifests
- Required, available (self-service) and uninstall intents. Managed app configuration and attributes. Takeover of user-installed apps

**Groups, assignments and compliance**
- Static groups, dynamic groups (model, iOS version, ownership, tags, installed apps, battery, …) and the built-in All Devices
- Assign profiles, apps, declarations and compliance policies to groups, with exclusions. A reconciler converges every device
  and tracks per-device delivery state, retries and failures
- Compliance policies: minimum/maximum iOS, passcode, encryption, supervision, check-in age, free storage, required/blocked apps,
  roaming, Activation Lock and profile installation. Grace periods, plus optional automatic remote lock

**Devices**
- Inventory: hardware, iOS, storage, battery, carrier, MACs, phone number, supervision, Activation Lock, Find My,
  installed apps, profiles, certificates, restrictions, provisioning profiles, available OS updates
- History: battery and free storage over time, public IPs seen, carrier/roaming, and location fixes (Lost Mode, or a
  [companion app](docs/companion-app.md) for Wi-Fi SSID and GPS)
- 50+ commands: lock with message, restart, shut down, clear passcode, Lost Mode with location tracking and sound,
  iOS updates, rename, wallpaper, Bluetooth/hotspot/roaming settings, time zone, Activation Lock bypass code,
  AirPlay mirroring, eSIM refresh, install/remove profiles, apps and books, and a raw command editor
- Bulk actions, CSV export, a command queue with full request/response inspection, and an activity timeline
- Company Portal for users: device status, compliance reasons, optional apps, support contacts

**Operations**
- Web console with roles (admin, operator, helpdesk, read-only), an audit log, and light/dark themes
- REST API with API keys ([reference](docs/API.md)) and HMAC-signed webhooks
- Single static binary with embedded SQLite (no CGO) and assets. Built-in Let's Encrypt, or run behind a reverse proxy

## Screenshots

The console, the Company Portal and the enrollment pages, from a demo fleet of 14 simulated devices. Click any image for full size.

<details open>
<summary><b>Devices</b></summary>
<br>

<table>
<tr>
<td width="50%" valign="top"><a href="docs/images/devices.jpg"><img src="docs/images/devices.jpg" alt="Device list with filters, bulk actions and CSV export"></a><br><sub>Device list with filters, bulk actions and CSV export</sub></td>
<td width="50%" valign="top"><a href="docs/images/device.jpg"><img src="docs/images/device.jpg" alt="Device overview: identity, status, security and network"></a><br><sub>Device overview: identity, status, security and network</sub></td>
</tr>
<tr>
<td width="50%" valign="top"><a href="docs/images/device-telemetry.jpg"><img src="docs/images/device-telemetry.jpg" alt="Battery and free-storage history, networks seen, recent samples"></a><br><sub>Battery and free-storage history, networks seen, recent samples</sub></td>
<td width="50%" valign="top"><a href="docs/images/device-location.jpg"><img src="docs/images/device-location.jpg" alt="Location history (Lost Mode or the companion app)"></a><br><sub>Location history (Lost Mode or the companion app)</sub></td>
</tr>
<tr>
<td width="50%" valign="top"><a href="docs/images/device-lost-mode.jpg"><img src="docs/images/device-lost-mode.jpg" alt="A device in Lost Mode"></a><br><sub>A device in Lost Mode</sub></td>
<td width="50%" valign="top"><a href="docs/images/device-configuration.jpg"><img src="docs/images/device-configuration.jpg" alt="Everything assigned to a device: delivery state, compliance and declarations"></a><br><sub>Everything assigned to a device: delivery state, compliance and declarations</sub></td>
</tr>
<tr>
<td width="50%" valign="top"><a href="docs/images/device-apps.jpg"><img src="docs/images/device-apps.jpg" alt="Installed apps, with managed apps marked"></a><br><sub>Installed apps, with managed apps marked</sub></td>
<td width="50%" valign="top"><a href="docs/images/device-profiles.jpg"><img src="docs/images/device-profiles.jpg" alt="Installed configuration profiles"></a><br><sub>Installed configuration profiles</sub></td>
</tr>
<tr>
<td width="50%" valign="top"><a href="docs/images/device-certificates.jpg"><img src="docs/images/device-certificates.jpg" alt="Certificates on the device"></a><br><sub>Certificates on the device</sub></td>
<td width="50%" valign="top"><a href="docs/images/device-commands.jpg"><img src="docs/images/device-commands.jpg" alt="Commands sent to the device"></a><br><sub>Commands sent to the device</sub></td>
</tr>
<tr>
<td width="50%" valign="top"><a href="docs/images/device-activity.jpg"><img src="docs/images/device-activity.jpg" alt="Device activity timeline"></a><br><sub>Device activity timeline</sub></td>
<td width="50%" valign="top"><a href="docs/images/manage-device.png"><img src="docs/images/manage-device.png" alt="Manage: wake, sample, re-apply, recovery codes, remove management (no wipe)"></a><br><sub>Manage: wake, sample, re-apply, recovery codes, remove management (no wipe)</sub></td>
</tr>
<tr>
<td width="50%" valign="top"><a href="docs/images/send-command.png"><img src="docs/images/send-command.png" alt="Send command: 50+ commands grouped by purpose"></a><br><sub>Send command: 50+ commands grouped by purpose</sub></td>
<td width="50%" valign="top"><a href="docs/images/command-form.png"><img src="docs/images/command-form.png" alt="Command options, e.g. a lock screen message and phone number"></a><br><sub>Command options, e.g. a lock screen message and phone number</sub></td>
</tr>
<tr>
<td width="50%" valign="top"><a href="docs/images/command-detail.png"><img src="docs/images/command-detail.png" alt="Command detail with the exact request and the device response"></a><br><sub>Command detail with the exact request and the device response</sub></td>
<td width="50%" valign="top"><a href="docs/images/command-queue.jpg"><img src="docs/images/command-queue.jpg" alt="Fleet-wide command queue"></a><br><sub>Fleet-wide command queue</sub></td>
</tr>
</table>

</details>

<details>
<summary><b>Groups</b></summary>
<br>

<table>
<tr>
<td width="50%" valign="top"><a href="docs/images/groups.jpg"><img src="docs/images/groups.jpg" alt="Static, dynamic and built-in groups"></a><br><sub>Static, dynamic and built-in groups</sub></td>
<td width="50%" valign="top"><a href="docs/images/group-static.jpg"><img src="docs/images/group-static.jpg" alt="A static group with its members and assignments"></a><br><sub>A static group with its members and assignments</sub></td>
</tr>
<tr>
<td width="50%" valign="top"><a href="docs/images/group-dynamic.jpg"><img src="docs/images/group-dynamic.jpg" alt="A dynamic group and its rules"></a><br><sub>A dynamic group and its rules</sub></td>
<td width="50%" valign="top"><a href="docs/images/group-new.png"><img src="docs/images/group-new.png" alt="Creating a group: static or rule-based"></a><br><sub>Creating a group: static or rule-based</sub></td>
</tr>
</table>

</details>

<details>
<summary><b>Configuration profiles</b></summary>
<br>

<table>
<tr>
<td width="50%" valign="top"><a href="docs/images/profiles.jpg"><img src="docs/images/profiles.jpg" alt="Profiles with their payloads and deployment state"></a><br><sub>Profiles with their payloads and deployment state</sub></td>
<td width="50%" valign="top"><a href="docs/images/profile-wifi.jpg"><img src="docs/images/profile-wifi.jpg" alt="Profile builder: Wi-Fi with 802.1X"></a><br><sub>Profile builder: Wi-Fi with 802.1X</sub></td>
</tr>
<tr>
<td width="50%" valign="top"><a href="docs/images/profile-restrictions.jpg"><img src="docs/images/profile-restrictions.jpg" alt="Passcode and 90+ restrictions, with supervised-only keys labeled"></a><br><sub>Passcode and 90+ restrictions, with supervised-only keys labeled</sub></td>
<td width="50%" valign="top"><a href="docs/images/add-payload.png"><img src="docs/images/add-payload.png" alt="Payload picker; labels show supervised-only and not-on-BYOD payloads"></a><br><sub>Payload picker; labels show supervised-only and not-on-BYOD payloads</sub></td>
</tr>
<tr>
<td width="50%" valign="top"><a href="docs/images/profile-new.jpg"><img src="docs/images/profile-new.jpg" alt="A new, empty profile"></a><br><sub>A new, empty profile</sub></td>
<td width="50%" valign="top"><a href="docs/images/profile-dark.jpg"><img src="docs/images/profile-dark.jpg" alt="Profile builder in dark mode"></a><br><sub>Profile builder in dark mode</sub></td>
</tr>
</table>

</details>

<details>
<summary><b>Apps, declarations and compliance</b></summary>
<br>

<table>
<tr>
<td width="50%" valign="top"><a href="docs/images/apps.jpg"><img src="docs/images/apps.jpg" alt="App catalog and Apps and Books licenses"></a><br><sub>App catalog and Apps and Books licenses</sub></td>
<td width="50%" valign="top"><a href="docs/images/app.jpg"><img src="docs/images/app.jpg" alt="App settings, managed app configuration, assignments and deployment"></a><br><sub>App settings, managed app configuration, assignments and deployment</sub></td>
</tr>
<tr>
<td width="50%" valign="top"><a href="docs/images/app-store-search.png"><img src="docs/images/app-store-search.png" alt="Adding an app from the App Store"></a><br><sub>Adding an app from the App Store</sub></td>
<td width="50%" valign="top"><a href="docs/images/declarations.jpg"><img src="docs/images/declarations.jpg" alt="Declarative Device Management declarations"></a><br><sub>Declarative Device Management declarations</sub></td>
</tr>
<tr>
<td width="50%" valign="top"><a href="docs/images/declaration.jpg"><img src="docs/images/declaration.jpg" alt="Editing a declaration and its assignments"></a><br><sub>Editing a declaration and its assignments</sub></td>
<td width="50%" valign="top"><a href="docs/images/compliance.jpg"><img src="docs/images/compliance.jpg" alt="Compliance overview and devices that need attention"></a><br><sub>Compliance overview and devices that need attention</sub></td>
</tr>
<tr>
<td width="50%" valign="top"><a href="docs/images/compliance-policy.jpg"><img src="docs/images/compliance-policy.jpg" alt="A compliance policy: OS, security, storage, apps, grace period and auto-lock"></a><br><sub>A compliance policy: OS, security, storage, apps, grace period and auto-lock</sub></td>
<td width="50%" valign="top"><a href="docs/images/dashboard-dark.png"><img src="docs/images/dashboard-dark.png" alt="Overview in dark mode"></a><br><sub>Overview in dark mode</sub></td>
</tr>
</table>

</details>

<details>
<summary><b>Enrollment</b></summary>
<br>

<table>
<tr>
<td width="50%" valign="top"><a href="docs/images/enrollment.jpg"><img src="docs/images/enrollment.jpg" alt="Enrollment: server readiness, enrollment links, work account sign-in and addresses"></a><br><sub>Enrollment: server readiness, enrollment links, work account sign-in and addresses</sub></td>
<td width="50%" valign="top"><a href="docs/images/enrollment-share.png"><img src="docs/images/enrollment-share.png" alt="Sharing an enrollment link as a QR code"></a><br><sub>Sharing an enrollment link as a QR code</sub></td>
</tr>
<tr>
<td width="50%" valign="top"><a href="docs/images/work-account-settings.png"><img src="docs/images/work-account-settings.png" alt="Work account sign-in (account-driven enrollment) settings"></a><br><sub>Work account sign-in (account-driven enrollment) settings</sub></td>
<td width="50%" valign="top"><a href="docs/images/ade.jpg"><img src="docs/images/ade.jpg" alt="Automated Device Enrollment with Apple Business Manager"></a><br><sub>Automated Device Enrollment with Apple Business Manager</sub></td>
</tr>
<tr>
<td width="50%" valign="top"><a href="docs/images/enroll-page.jpg"><img src="docs/images/enroll-page.jpg" alt="The enrollment page on a computer, with a QR code for the device"></a><br><sub>The enrollment page on a computer, with a QR code for the device</sub></td>
<td width="50%" valign="top"><a href="docs/images/enrollment-dark.jpg"><img src="docs/images/enrollment-dark.jpg" alt="Enrollment in dark mode"></a><br><sub>Enrollment in dark mode</sub></td>
</tr>
</table>

</details>

<details>
<summary><b>BYOD (User Enrollment)</b></summary>
<br>

<table>
<tr>
<td width="50%" valign="top"><a href="docs/images/byod-device.jpg"><img src="docs/images/byod-device.jpg" alt="A personal iPhone: work account shown, device identifiers hidden"></a><br><sub>A personal iPhone: work account shown, device identifiers hidden</sub></td>
<td width="50%" valign="top"><a href="docs/images/byod-configuration.jpg"><img src="docs/images/byod-configuration.jpg" alt="Payloads iOS rejects on personal devices are marked not applicable"></a><br><sub>Payloads iOS rejects on personal devices are marked not applicable</sub></td>
</tr>
<tr>
<td width="50%" valign="top"><a href="docs/images/byod-commands.png"><img src="docs/images/byod-commands.png" alt="Only commands iOS allows on User Enrollment are offered"></a><br><sub>Only commands iOS allows on User Enrollment are offered</sub></td>
<td width="50%" valign="top"><a href="docs/images/device-dark.jpg"><img src="docs/images/device-dark.jpg" alt="Device page in dark mode"></a><br><sub>Device page in dark mode</sub></td>
</tr>
</table>

</details>

<details>
<summary><b>Activity and settings</b></summary>
<br>

<table>
<tr>
<td width="50%" valign="top"><a href="docs/images/activity.jpg"><img src="docs/images/activity.jpg" alt="Device activity across the fleet"></a><br><sub>Device activity across the fleet</sub></td>
<td width="50%" valign="top"><a href="docs/images/audit-log.jpg"><img src="docs/images/audit-log.jpg" alt="Audit log with readable details and links to what changed"></a><br><sub>Audit log with readable details and links to what changed</sub></td>
</tr>
<tr>
<td width="50%" valign="top"><a href="docs/images/settings-general.jpg"><img src="docs/images/settings-general.jpg" alt="General settings: organization, support contacts, enrollment"></a><br><sub>General settings: organization, support contacts, enrollment</sub></td>
<td width="50%" valign="top"><a href="docs/images/settings-schedule.jpg"><img src="docs/images/settings-schedule.jpg" alt="Schedules and data retention"></a><br><sub>Schedules and data retention</sub></td>
</tr>
<tr>
<td width="50%" valign="top"><a href="docs/images/settings-push.jpg"><img src="docs/images/settings-push.jpg" alt="APNs push certificate status and renewal"></a><br><sub>APNs push certificate status and renewal</sub></td>
<td width="50%" valign="top"><a href="docs/images/settings-apps-and-books.jpg"><img src="docs/images/settings-apps-and-books.jpg" alt="Apps and Books content tokens"></a><br><sub>Apps and Books content tokens</sub></td>
</tr>
<tr>
<td width="50%" valign="top"><a href="docs/images/settings-administrators.jpg"><img src="docs/images/settings-administrators.jpg" alt="Administrators and roles"></a><br><sub>Administrators and roles</sub></td>
<td width="50%" valign="top"><a href="docs/images/settings-api-keys.jpg"><img src="docs/images/settings-api-keys.jpg" alt="API keys"></a><br><sub>API keys</sub></td>
</tr>
<tr>
<td width="50%" valign="top"><a href="docs/images/settings-webhooks.jpg"><img src="docs/images/settings-webhooks.jpg" alt="Signed webhooks with delivery status"></a><br><sub>Signed webhooks with delivery status</sub></td>
<td width="50%" valign="top"><a href="docs/images/settings-about.jpg"><img src="docs/images/settings-about.jpg" alt="Server details, device identity CA and issued certificates"></a><br><sub>Server details, device identity CA and issued certificates</sub></td>
</tr>
<tr>
<td width="50%" valign="top"><a href="docs/images/sign-in.png"><img src="docs/images/sign-in.png" alt="Console sign-in"></a><br><sub>Console sign-in</sub></td>
<td width="50%" valign="top"><a href="docs/images/setup.png"><img src="docs/images/setup.png" alt="First-run setup on a new server"></a><br><sub>First-run setup on a new server</sub></td>
</tr>
<tr>
<td width="50%" valign="top"><a href="docs/images/device-telemetry-dark.jpg"><img src="docs/images/device-telemetry-dark.jpg" alt="Battery and network history in dark mode"></a><br><sub>Battery and network history in dark mode</sub></td>
<td width="50%" valign="top"><a href="docs/images/mobile-menu.png"><img src="docs/images/mobile-menu.png" alt="Console navigation on a phone"></a><br><sub>Console navigation on a phone</sub></td>
</tr>
</table>

</details>

<details>
<summary><b>On phones: Company Portal, sign-in and the console</b></summary>
<br>

<table><tr><td align="center" valign="top"><a href="docs/images/portal.jpg"><img src="docs/images/portal.jpg" width="230" alt="Company Portal"></a><br><sub>Company Portal</sub></td><td align="center" valign="top"><a href="docs/images/portal-dark.jpg"><img src="docs/images/portal-dark.jpg" width="230" alt="Company Portal, dark"></a><br><sub>Company Portal, dark</sub></td><td align="center" valign="top"><a href="docs/images/byod-signin.jpg"><img src="docs/images/byod-signin.jpg" width="230" alt="Work account sign-in"></a><br><sub>Work account sign-in</sub></td><td align="center" valign="top"><a href="docs/images/enroll-code.jpg"><img src="docs/images/enroll-code.jpg" width="230" alt="Enrollment code page"></a><br><sub>Enrollment code page</sub></td></tr></table>

<table><tr><td align="center" valign="top"><a href="docs/images/mobile-dashboard.jpg"><img src="docs/images/mobile-dashboard.jpg" width="230" alt="Console overview"></a><br><sub>Console overview</sub></td><td align="center" valign="top"><a href="docs/images/mobile-devices.jpg"><img src="docs/images/mobile-devices.jpg" width="230" alt="Device list"></a><br><sub>Device list</sub></td><td align="center" valign="top"><a href="docs/images/mobile-device.jpg"><img src="docs/images/mobile-device.jpg" width="230" alt="Device page"></a><br><sub>Device page</sub></td></tr></table>

</details>

## No remote wipe

Orchard was built without device wipe, and enforces that in several places:

- The enrollment profile grants every MDM access right **except "device erase"**, so enrolled devices
  refuse `EraseDevice` even if one were ever sent.
- The command queue refuses `EraseDevice` and `DeleteUser` from every path: the catalog, the raw command editor, the API and automation.
- The passcode payload builder doesn't offer `maxFailedAttempts`, which makes devices erase themselves after
  failed passcodes. It is also stripped (with a warning) from uploaded profiles.
- Compliance actions stop at a remote lock. "Remove management" only removes the MDM profile and the managed
  apps and profiles; the user's data stays on the device.
- BYOD User Enrollments get Apple's fixed rights for personal devices, which never include erase. Removing the work
  account deletes only the separate work volume.

## Requirements

- A domain name and an HTTPS URL devices can reach (`https://mdm.example.com`). iOS requires a publicly trusted TLS certificate.
  Orchard can obtain one from Let's Encrypt, or you can terminate TLS on a reverse proxy.
- An **MDM push certificate** from Apple (free). You need a CSR signed by an MDM vendor: either your own vendor
  certificate from the Apple Developer Program, or the free mdmcert.download service. The console walks you through it.
- Optional: Apple Business Manager for Automated Device Enrollment and Apps and Books.

## Quick start

```sh
# build (Go 1.26+; or use the Docker image below)
go build -o orchard ./cmd/orchard

# run with automatic Let's Encrypt certificates
sudo ./orchard -url https://mdm.example.com -acme-domains mdm.example.com -acme-email it@example.com \
  -listen :443 -http-redirect :80 -data /var/lib/orchard
```

1. Open `https://mdm.example.com`. On first start the server log prints a **setup code**; enter it to create the first administrator
   (or pass `-admin-user` / `-admin-password`).
2. **Settings → Apple Push**: create a certificate request, have it signed, upload it at
   [identity.apple.com/pushcert](https://identity.apple.com/pushcert/), then upload Apple's certificate back to Orchard.
   Note the Apple Account you used: renewals must use the same one.
3. **Enrollment → New enrollment link**, then open the link in Safari on an iPhone or iPad (or scan its QR code) and install the profile.
4. Build profiles, add apps and compliance policies, and assign them to groups. Devices pick changes up within a minute.

### Docker

```sh
docker build -t orchard-mdm .
docker run -d --name orchard -p 443:8443 -p 80:8080 -v orchard-data:/data \
  -e ORCHARD_URL=https://mdm.example.com \
  -e ORCHARD_ACME_DOMAINS=mdm.example.com -e ORCHARD_ACME_EMAIL=it@example.com \
  -e ORCHARD_LISTEN=:8443 -e ORCHARD_HTTP_REDIRECT=:8080 \
  orchard-mdm
```

See [`docker-compose.yml`](docker-compose.yml) for a compose setup.

### Behind a reverse proxy

Devices sign every request (`Mdm-Signature`), so the proxy doesn't need client-certificate authentication. Proxy
everything to Orchard and pass the client address:

```nginx
server {
    listen 443 ssl http2;
    server_name mdm.example.com;
    client_max_body_size 4g;              # in-house .ipa uploads
    location / {
        proxy_pass http://127.0.0.1:8080;
        proxy_set_header Host $host;
        proxy_set_header X-Forwarded-For $remote_addr;
        proxy_set_header X-Forwarded-Proto https;
    }
}
```

Run Orchard with `-url https://mdm.example.com -listen 127.0.0.1:8080 -trust-proxy`.

## Configuration

Every flag can also be set as an environment variable (`ORCHARD_` + upper-case name).

| Flag | Env | Default | Purpose |
|---|---|---|---|
| `-url` | `ORCHARD_URL` | | Public HTTPS base URL devices use |
| `-listen` | `ORCHARD_LISTEN` | `:8443` with TLS, `:8080` without | Listen address |
| `-data` | `ORCHARD_DATA` | `./data` | Database, uploads and ACME cache |
| `-tls-cert`, `-tls-key` | `ORCHARD_TLS_CERT`, `ORCHARD_TLS_KEY` | | TLS certificate files (reloaded when they change) |
| `-acme-domains`, `-acme-email` | `ORCHARD_ACME_DOMAINS`, `ORCHARD_ACME_EMAIL` | | Obtain certificates from Let's Encrypt |
| `-http-redirect` | `ORCHARD_HTTP_REDIRECT` | | Plain HTTP listener that redirects to HTTPS and answers ACME challenges |
| `-trust-proxy` | `ORCHARD_TRUST_PROXY` | `false` | Trust `X-Forwarded-For` / `X-Forwarded-Proto` |
| `-client-cert-header` | `ORCHARD_CLIENT_CERT_HEADER` | | Header carrying a URL-escaped client certificate from a TLS-terminating proxy |
| `-admin-user`, `-admin-password` | `ORCHARD_ADMIN_USER`, `ORCHARD_ADMIN_PASSWORD` | | Create the first administrator non-interactively |
| `-log-level`, `-log-json` | `ORCHARD_LOG_LEVEL`, `ORCHARD_LOG_JSON` | `info` | Logging |
| `-apns-url`, `-dep-url`, `-vpp-url`, `-itunes-url` | | Apple production endpoints | Override Apple endpoints (testing) |

Everything else (organization name, support contacts, enrollment policy, inventory and telemetry intervals, retention,
Lost Mode location polling, profile signing) lives under **Settings** in the console.

## Automation

- **REST API:** create a key under Settings → API keys and send `Authorization: Bearer orch_…`. See [docs/API.md](docs/API.md).
- **Webhooks:** Orchard POSTs JSON events (`device.enrolled`, `device.unenrolled`, `compliance.changed`, `command.failed`,
  `device.location`, `device.telemetry`, `ade.device_added`, `apns.cert_expiring`, …). Verify `X-Orchard-Signature`:
  ```python
  expected = "sha256=" + hmac.new(secret.encode(), body, hashlib.sha256).hexdigest()
  ```

## Architecture

```
cmd/orchard          server entry point
cmd/orchard-sim      simulated iOS devices for demos and load tests
internal/mdm         MDM protocol: enrollment profile, check-in, command queue, results, identity checks
internal/scepsrv     SCEP certificate authority endpoint
internal/apns        APNs HTTP/2 client and push certificate workflows
internal/ddm         Declarative Device Management (declarations, activations, status)
internal/reconcile   dynamic groups and desired-state convergence of profiles, apps and declarations
internal/compliance  compliance evaluation and actions
internal/dep         Automated Device Enrollment (OAuth 1 session, sync, profiles, enrollment endpoint)
internal/vpp         Apps and Books client and license sync
internal/profiles    payload schemas, profile builder and upload parser
internal/api         REST API, sessions, API keys, roles, audit
internal/portal      enrollment pages, work account sign-in (account-driven enrollment, OIDC), Company Portal, companion-agent API, app downloads
internal/store       SQLite persistence (modernc.org/sqlite, no CGO)
web/                 the console (vanilla JS modules, embedded)
```

## Development

```sh
go test ./...                 # unit tests and end-to-end flows with simulated devices
go run ./cmd/orchard -url http://127.0.0.1:8080 -admin-user admin -admin-password 'change-me-now'
go run ./cmd/orchard-sim -enroll http://127.0.0.1:8080/enroll/<token> -n 10
# BYOD: enroll through work account sign-in (turn it on under Enrollment first)
go run ./cmd/orchard-sim -server http://127.0.0.1:8080 -account sam@acme.example -code <enrollment code> -n 2
```

The end-to-end tests enroll simulated devices through real SCEP, sign every request like iOS does, and drive inventory,
commands, Lost Mode location, profile and app assignment, Declarative Management, compliance, ADE (against a fake Apple API),
BYOD account-driven User Enrollment (enrollment code and single sign-on against a fake OpenID Connect provider) and the full REST API.

## Limitations

- Targets iPhone and iPad (device enrollment and BYOD User Enrollment). macOS, tvOS and Shared iPad user channels are not managed.
- BYOD User Enrollment needs Managed Apple Accounts (federated or created in Apple Business Manager).
- The connected Wi-Fi network and continuous location are not available through MDM. Use the
  [companion app approach](docs/companion-app.md), with your users' consent.
- Simulated devices in `orchard-sim` cannot receive real push notifications; they poll instead.

## License

[MIT](LICENSE). The bundled Atkinson Hyperlegible fonts are under the SIL Open Font License
([web/static/fonts/OFL.txt](web/static/fonts/OFL.txt)).
