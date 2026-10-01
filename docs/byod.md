# BYOD: User Enrollment with a work account

Orchard supports Apple's **account-driven enrollment**. People open
**Settings → General → VPN & Device Management → Sign In to Work or School Account**, enter their
work email, sign in, and their iPhone or iPad enrolls without downloading a profile.

It can enroll devices in two ways (Enrollment → Work account sign-in → Configure):

| | User Enrollment (BYOD) | Device enrollment |
|---|---|---|
| For | Personal devices | Organization-owned devices not in Apple Business Manager |
| Work data | Separate encrypted volume, removed when the work account is removed | Whole device |
| What you can see | Model, iOS version, battery, storage, managed apps and profiles | Full inventory |
| Device identifiers (serial, UDID, IMEI, phone number, MAC addresses) | Hidden by iOS | Collected |
| Location, Lost Mode, restart, OS updates, clear passcode | Not available | Available (supervision rules still apply) |
| Erase | Not possible (and Orchard never wipes devices anyway) | Not possible in Orchard |
| iOS | 15 and later | 17 and later |

## Requirements

- **Managed Apple Accounts.** Every person needs a Managed Apple Account with the same address they type
  on the device: either federated with Microsoft Entra ID or Google Workspace, or created in Apple Business Manager.
  After the device gets its enrollment profile, iOS asks the person to sign in to that account with Apple.
- Orchard reachable over HTTPS with a trusted certificate, and an APNs push certificate (as for any enrollment).
- A way for devices to find Orchard (service discovery). See below.

## 1. Turn it on

In **Enrollment → Work account sign-in → Configure**:

1. Check **Allow enrollment by signing in with a work account**.
2. Choose **User Enrollment (BYOD)**.
3. Enter your **work domains** (for example `acme.com`). Accounts in other domains are turned away.
4. Choose how people prove who they are:
   - **Enrollment code.** People type the code of one of your enrollment links (Enrollment → Share). The link's use limit,
     expiry and groups apply. Create a dedicated link such as "BYOD" with ownership "Personal".
   - **Single sign-on.** People sign in with your identity provider (OpenID Connect). See [Single sign-on](#single-sign-on).
   - **Both.**
5. Optionally pick static groups that every BYOD device joins. Then assign profiles, apps and compliance
   policies to those groups as usual.

## 2. Let devices find Orchard

The device takes the domain from the email address and requests
`https://<domain>/.well-known/com.apple.remotemanagement?user-identifier=…&model-family=…`.
Use either option.

**On your website.** Make that URL redirect to Orchard, keeping the query string:

```nginx
# on the web server for acme.com
location = /.well-known/com.apple.remotemanagement {
    return 307 https://mdm.acme.com/.well-known/com.apple.remotemanagement$is_args$args;
}
```

Orchard answers with the enrollment endpoint:

```json
{"Servers": [{"Version": "mdm-byod", "BaseURL": "https://mdm.acme.com/enroll/account/byod"}]}
```

**With Apple Business Manager (iOS 18.2 and later).** If your website can't host the file, connect Apple Business
Manager under *Automated enrollment*. Then press **Register** next to the server in Enrollment → Work account sign-in.
Devices that don't find the file on your domain are redirected to Orchard by Apple.

## Single sign-on

Any OpenID Connect provider works, including Microsoft Entra ID, Google, Okta and Keycloak. Create a web application in
your identity provider with this redirect URI (shown in the configuration dialog):

```
https://mdm.acme.com/enroll/account/callback
```

Then enter in Orchard:

| Setting | Entra ID | Google | Okta |
|---|---|---|---|
| Issuer URL | `https://login.microsoftonline.com/<tenant-id>/v2.0` | `https://accounts.google.com` | `https://<org>.okta.com` |
| Identity claim | `preferred_username` (or `email`) | `email` | `email` |

Keep **"The account people sign in with must match the work account on the device"** on. It stops someone from signing
in as themselves while enrolling a different Managed Apple Account. Orchard uses the authorization code flow with PKCE and
a nonce, and checks the ID token's issuer, audience, signature and expiry.

## What you can manage on a BYOD device

Orchard shows only the commands iOS accepts from User Enrollment, and refuses the others:

- **Available:** inventory (device information, security info, managed apps, profiles, certificates), lock screen,
  install/remove profiles and provisioning profiles, install/remove apps (with managed app configuration), books,
  Declarative Device Management, and removing management.
- **Not available:** Lost Mode and location, restart, shut down, clear passcode, OS updates, Activation Lock bypass,
  device settings (name, wallpaper, Bluetooth…), restrictions queries, and any form of erase.

Profiles are checked too. Payloads that iOS rejects on User Enrollment (cellular, global proxy, Home Screen layout,
notifications, Single App Mode, encrypted DNS, managed domains, device-wide VPN, network usage rules, lock screen message)
are marked **Not applicable** for those devices instead of failing repeatedly. Per-app VPN, Wi-Fi, accounts, certificates,
passcode, web clips and restrictions work. The profile builder labels payloads that don't apply to personal devices.

**Apps.** App Store apps install into the work volume. With Apps and Books, Orchard licenses apps to the person's Managed
Apple Account (user-based assignment): it registers the account as an Apps and Books user and associates the license with
it, because personal devices have no serial number to license against.

**Privacy.** For personal devices, Orchard asks only for managed apps (`ManagedAppsOnly`) and a reduced set of device
information. The same managed-apps-only inventory applies to personal devices enrolled with an enrollment link.

## Removing a BYOD device

**Manage → Remove management** (or the person removing the work account in Settings) ends the enrollment. iOS deletes the
work volume: managed apps, accounts and their data. Personal apps, photos and data are untouched.

## Troubleshooting

- **"Unable to discover…"** The well-known URL isn't reachable or returns an error. Check that work account sign-in is on, the
  domain is in the allowed list, and the redirect keeps the query string.
- **The sign-in page says the account isn't managed.** The email's domain isn't in the work domains list.
- **Enrollment stops after sign-in.** The person couldn't sign in to the Managed Apple Account with Apple. Check the account
  exists in Apple Business Manager and is federated or has a password.
- **Activity** lists every account sign-in, and Enrollment → Work account sign-in shows recent sign-ins and whether each
  produced a device.
