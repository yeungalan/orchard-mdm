// Package profiles builds and parses Apple configuration profiles.
package profiles

// Option is a choice of an enum field.
type Option struct {
	Value any    `json:"value"`
	Label string `json:"label"`
}

// Field describes one key of a payload. Key may be a dotted path to nest
// values in dictionaries (e.g. "IKEv2.RemoteAddress").
type Field struct {
	Key        string            `json:"key"`
	Label      string            `json:"label"`
	Type       string            `json:"type"`
	Default    any               `json:"default,omitempty"`
	Options    []Option          `json:"options,omitempty"`
	Help       string            `json:"help,omitempty"`
	Required   bool              `json:"required,omitempty"`
	Supervised bool              `json:"supervised,omitempty"`
	Section    string            `json:"section,omitempty"`
	Fields     []Field           `json:"fields,omitempty"`  // dictlist sub-fields
	ShowIf     map[string][]any  `json:"show_if,omitempty"` // only applies when other fields have one of these values
	RefTypes   []string          `json:"ref_types,omitempty"`
	Extra      map[string]string `json:"extra,omitempty"`
}

// Schema describes a payload type.
type Schema struct {
	Type        string  `json:"type"`
	Name        string  `json:"name"`
	Description string  `json:"description"`
	Category    string  `json:"category"`
	Unique      bool    `json:"unique"`
	Supervised  bool    `json:"supervised,omitempty"`
	Fields      []Field `json:"fields"`
	// UserEnrollment is false for payloads personal (User Enrollment) devices reject.
	UserEnrollment bool `json:"user_enrollment"`
}

func b(key, label string, def bool, supervised bool, section string) Field {
	return Field{Key: key, Label: label, Type: "bool", Default: def, Supervised: supervised, Section: section}
}

func opts(pairs ...any) []Option {
	var out []Option
	for i := 0; i+1 < len(pairs); i += 2 {
		out = append(out, Option{Value: pairs[i], Label: pairs[i+1].(string)})
	}
	return out
}

var proxyFields = []Field{
	{Key: "ProxyType", Label: "Type", Type: "enum", Default: "None", Options: opts("None", "None", "Manual", "Manual", "Auto", "Automatic (PAC)"), Section: "Proxy"},
	{Key: "ProxyServer", Label: "Proxy server", Type: "string", Section: "Proxy", ShowIf: map[string][]any{"ProxyType": {"Manual"}}},
	{Key: "ProxyServerPort", Label: "Proxy port", Type: "int", Section: "Proxy", ShowIf: map[string][]any{"ProxyType": {"Manual"}}},
	{Key: "ProxyUsername", Label: "Proxy username", Type: "string", Section: "Proxy", ShowIf: map[string][]any{"ProxyType": {"Manual"}}},
	{Key: "ProxyPassword", Label: "Proxy password", Type: "password", Section: "Proxy", ShowIf: map[string][]any{"ProxyType": {"Manual"}}},
	{Key: "ProxyPACURL", Label: "PAC URL", Type: "string", Section: "Proxy", ShowIf: map[string][]any{"ProxyType": {"Auto"}}},
	{Key: "ProxyPACFallbackAllowed", Label: "Allow direct connection if PAC is unreachable", Type: "bool", Default: false, Section: "Proxy", ShowIf: map[string][]any{"ProxyType": {"Auto"}}},
}

var ikev2 = map[string][]any{"VPNType": {"IKEv2"}}
var ipsec = map[string][]any{"VPNType": {"IPSec"}}
var customVPN = map[string][]any{"VPNType": {"VPN"}}

// Schemas is the payload catalog of the profile builder.
var Schemas = []Schema{
	{Type: "com.apple.mobiledevice.passwordpolicy", Name: "Passcode", Category: "Security", Unique: true,
		Description: "Passcode requirements. (Automatic erase after failed attempts is intentionally not offered.)",
		Fields: []Field{
			b("forcePIN", "Require passcode", true, false, ""),
			b("allowSimple", "Allow simple passcodes (1234, 1111…)", true, false, ""),
			b("requireAlphanumeric", "Require alphanumeric", false, false, ""),
			{Key: "minLength", Label: "Minimum length", Type: "int", Help: "0–16"},
			{Key: "minComplexChars", Label: "Minimum complex characters", Type: "int", Help: "0–4"},
			{Key: "maxPINAgeInDays", Label: "Maximum passcode age (days)", Type: "int", Help: "1–730"},
			{Key: "pinHistory", Label: "Passcode history", Type: "int", Help: "1–50 previous passcodes cannot be reused"},
			{Key: "maxInactivity", Label: "Auto-Lock (minutes)", Type: "enum", Options: opts(1, "1", 2, "2", 3, "3", 4, "4", 5, "5", 10, "10 (iPad)", 15, "15 (iPad)")},
			{Key: "maxGracePeriod", Label: "Require passcode after", Type: "enum", Options: opts(0, "Immediately", 1, "1 minute", 5, "5 minutes", 15, "15 minutes", 60, "1 hour", 240, "4 hours")},
		}},
	{Type: "com.apple.applicationaccess", Name: "Restrictions", Category: "Security", Unique: false,
		Description: "Device, app, iCloud, Safari and content restrictions. Items marked “supervised” only apply to supervised devices.",
		Fields: []Field{
			b("allowCamera", "Allow camera", true, false, "Device functionality"),
			b("allowScreenShot", "Allow screenshots and screen recording", true, false, "Device functionality"),
			b("allowAirDrop", "Allow AirDrop", true, true, "Device functionality"),
			b("allowAssistant", "Allow Siri", true, false, "Device functionality"),
			b("allowAssistantWhileLocked", "Allow Siri while locked", true, false, "Device functionality"),
			b("allowVoiceDialing", "Allow voice dialing while locked", true, false, "Device functionality"),
			b("allowPassbookWhileLocked", "Allow Wallet on lock screen", true, false, "Device functionality"),
			b("allowFingerprintForUnlock", "Allow Touch ID / Face ID to unlock", true, false, "Device functionality"),
			b("allowFingerprintModification", "Allow modifying Touch ID / Face ID", true, true, "Device functionality"),
			b("allowPasscodeModification", "Allow passcode changes", true, true, "Device functionality"),
			b("allowAccountModification", "Allow account changes", true, true, "Device functionality"),
			b("allowDeviceNameModification", "Allow device name changes", true, true, "Device functionality"),
			b("allowWallpaperModification", "Allow wallpaper changes", true, true, "Device functionality"),
			b("allowBluetoothModification", "Allow Bluetooth changes", true, true, "Device functionality"),
			b("allowPersonalHotspotModification", "Allow Personal Hotspot changes", true, true, "Device functionality"),
			b("allowCellularPlanModification", "Allow cellular plan changes", true, true, "Device functionality"),
			b("allowESIMModification", "Allow eSIM changes", true, true, "Device functionality"),
			b("allowVPNCreation", "Allow adding VPN configurations", true, true, "Device functionality"),
			b("allowEraseContentAndSettings", "Allow the user to “Erase All Content and Settings”", true, true, "Device functionality"),
			b("allowUIConfigurationProfileInstallation", "Allow installing configuration profiles", true, true, "Device functionality"),
			b("allowHostPairing", "Allow pairing with computers", true, true, "Device functionality"),
			b("allowUSBRestrictedMode", "Allow USB accessories while locked", true, true, "Device functionality"),
			b("allowFindMyDevice", "Allow Find My iPhone", true, true, "Device functionality"),
			b("allowFindMyFriends", "Allow Find My Friends", true, true, "Device functionality"),
			b("allowAutoUnlock", "Allow Auto Unlock with Apple Watch", true, false, "Device functionality"),
			b("allowNotificationsModification", "Allow notification settings changes", true, true, "Device functionality"),
			b("allowDictation", "Allow dictation", true, true, "Device functionality"),
			b("allowPredictiveKeyboard", "Allow predictive keyboard", true, true, "Device functionality"),
			b("allowAutoCorrection", "Allow auto-correction", true, true, "Device functionality"),
			b("allowSpellCheck", "Allow spell check", true, true, "Device functionality"),
			b("allowKeyboardShortcuts", "Allow keyboard shortcuts", true, true, "Device functionality"),
			b("allowDefinitionLookup", "Allow Look Up", true, true, "Device functionality"),
			b("allowLockScreenControlCenter", "Allow Control Center on lock screen", true, false, "Device functionality"),
			b("allowLockScreenNotificationsView", "Allow Notification Center on lock screen", true, false, "Device functionality"),
			b("allowLockScreenTodayView", "Allow Today view on lock screen", true, false, "Device functionality"),
			b("allowNFC", "Allow NFC", true, true, "Device functionality"),
			b("allowAirPrint", "Allow AirPrint", true, true, "Device functionality"),
			b("allowRemoteScreenObservation", "Allow Classroom screen observation", true, true, "Device functionality"),
			b("forceWiFiPowerOn", "Force Wi-Fi on", false, true, "Device functionality"),
			b("forceWiFiToAllowedNetworksOnly", "Join only Wi-Fi networks installed by profiles", false, true, "Device functionality"),
			b("allowVideoConferencing", "Allow FaceTime", true, true, "Apps"),
			b("allowAppInstallation", "Allow installing apps (App Store)", true, true, "Apps"),
			b("allowAppRemoval", "Allow deleting apps", true, true, "Apps"),
			b("allowAutomaticAppDownloads", "Allow automatic app downloads", true, true, "Apps"),
			b("allowInAppPurchases", "Allow in-app purchases", true, false, "Apps"),
			b("allowSafari", "Allow Safari", true, true, "Apps"),
			b("allowGameCenter", "Allow Game Center", true, true, "Apps"),
			b("allowMultiplayerGaming", "Allow multiplayer gaming", true, false, "Apps"),
			b("allowAddingGameCenterFriends", "Allow adding Game Center friends", true, false, "Apps"),
			b("allowiTunes", "Allow iTunes Store", true, true, "Apps"),
			b("allowBookstore", "Allow Apple Books", true, true, "Apps"),
			b("allowNews", "Allow News", true, true, "Apps"),
			b("allowPodcasts", "Allow Podcasts", true, true, "Apps"),
			b("allowMusicService", "Allow Apple Music", true, true, "Apps"),
			b("allowRadioService", "Allow Radio", true, true, "Apps"),
			b("allowChat", "Allow iMessage", true, true, "Apps"),
			b("allowAppClips", "Allow App Clips", true, true, "Apps"),
			b("allowEnterpriseAppTrust", "Allow trusting new enterprise app developers", true, false, "Apps"),
			b("allowExplicitContent", "Allow explicit music, podcasts and news", true, false, "Apps"),
			{Key: "blockedAppBundleIDs", Label: "Blocked apps (bundle IDs)", Type: "stringlist", Supervised: true, Section: "Apps"},
			{Key: "allowListedAppBundleIDs", Label: "Only allow these apps (bundle IDs)", Type: "stringlist", Supervised: true, Section: "Apps", Help: "When set, only these apps are visible."},
			{Key: "autonomousSingleAppModePermittedAppIDs", Label: "Apps allowed to enter Single App Mode", Type: "stringlist", Supervised: true, Section: "Apps"},
			b("allowOpenFromManagedToUnmanaged", "Allow documents from managed apps in unmanaged apps", true, false, "Managed data"),
			b("allowOpenFromUnmanagedToManaged", "Allow documents from unmanaged apps in managed apps", true, false, "Managed data"),
			b("allowManagedToWriteUnmanagedContacts", "Allow managed apps to write unmanaged contacts", false, false, "Managed data"),
			b("allowUnmanagedToReadManagedContacts", "Allow unmanaged apps to read managed contacts", false, false, "Managed data"),
			b("forceAirDropUnmanaged", "Treat AirDrop as an unmanaged destination", false, false, "Managed data"),
			b("allowManagedAppsCloudSync", "Allow managed apps to sync to iCloud", true, false, "Managed data"),
			b("allowEnterpriseBookBackup", "Allow backup of enterprise books", true, false, "Managed data"),
			b("allowCloudBackup", "Allow iCloud Backup", true, false, "iCloud"),
			b("allowCloudDocumentSync", "Allow iCloud Drive", true, false, "iCloud"),
			b("allowCloudKeychainSync", "Allow iCloud Keychain", true, false, "iCloud"),
			b("allowCloudPhotoLibrary", "Allow iCloud Photos", true, false, "iCloud"),
			b("allowSharedStream", "Allow Shared Albums", true, false, "iCloud"),
			b("allowActivityContinuation", "Allow Handoff", true, false, "iCloud"),
			b("allowCloudPrivateRelay", "Allow iCloud Private Relay", true, true, "iCloud"),
			b("forceEncryptedBackup", "Force encrypted backups", false, false, "iCloud"),
			b("allowPasswordAutoFill", "Allow password AutoFill", true, true, "Security & privacy"),
			b("allowPasswordProximityRequests", "Allow requesting passwords from nearby devices", true, true, "Security & privacy"),
			b("allowPasswordSharing", "Allow password sharing", true, true, "Security & privacy"),
			b("forceAuthenticationBeforeAutoFill", "Require Face ID / Touch ID before AutoFill", false, false, "Security & privacy"),
			b("allowUntrustedTLSPrompt", "Allow users to accept untrusted TLS certificates", true, false, "Security & privacy"),
			b("forceLimitAdTracking", "Force limited ad tracking", false, false, "Security & privacy"),
			b("allowDiagnosticSubmission", "Allow sending diagnostics to Apple", true, false, "Security & privacy"),
			b("allowDiagnosticSubmissionModification", "Allow changing diagnostics settings", true, true, "Security & privacy"),
			b("allowGlobalBackgroundFetchWhenRoaming", "Allow background fetch when roaming", true, false, "Security & privacy"),
			b("forceAssistantProfanityFilter", "Force Siri profanity filter", false, true, "Security & privacy"),
			b("allowRapidSecurityResponseInstallation", "Allow Rapid Security Responses", true, true, "Software updates"),
			b("forceDelayedSoftwareUpdates", "Delay software updates", false, true, "Software updates"),
			{Key: "enforcedSoftwareUpdateDelay", Label: "Delay updates by (days)", Type: "int", Supervised: true, Section: "Software updates", Help: "1–90"},
			b("safariAllowAutoFill", "Allow AutoFill in Safari", true, true, "Safari"),
			b("safariAllowJavaScript", "Allow JavaScript", true, false, "Safari"),
			b("safariAllowPopups", "Allow pop-ups", true, false, "Safari"),
			b("safariForceFraudWarning", "Force fraudulent website warning", false, false, "Safari"),
			{Key: "safariAcceptCookies", Label: "Accept cookies", Type: "enum", Section: "Safari", Options: opts(2.0, "Always", 1.5, "From websites I visit", 1.0, "From current website only", 0.0, "Never")},
			{Key: "ratingRegion", Label: "Ratings region", Type: "enum", Section: "Content ratings",
				Options: opts("us", "United States", "au", "Australia", "ca", "Canada", "de", "Germany", "fr", "France", "ie", "Ireland", "jp", "Japan", "nz", "New Zealand", "gb", "United Kingdom")},
			{Key: "ratingApps", Label: "Apps", Type: "enum", Section: "Content ratings", Options: opts(1000, "Allow all", 600, "17+", 300, "12+", 200, "9+", 100, "4+", 0, "Don't allow")},
			{Key: "ratingMovies", Label: "Movies", Type: "enum", Section: "Content ratings", Options: opts(1000, "Allow all", 500, "NC-17 / R18", 400, "R / R15+", 300, "PG-13 / PG12", 200, "PG", 100, "G", 0, "Don't allow")},
			{Key: "ratingTVShows", Label: "TV shows", Type: "enum", Section: "Content ratings", Options: opts(1000, "Allow all", 600, "TV-MA", 500, "TV-14", 400, "TV-PG", 300, "TV-G", 200, "TV-Y7", 100, "TV-Y", 0, "Don't allow")},
		}},
	{Type: "com.apple.wifi.managed", Name: "Wi-Fi", Category: "Network",
		Description: "Join a Wi-Fi network automatically.",
		Fields: append([]Field{
			{Key: "SSID_STR", Label: "Network name (SSID)", Type: "string", Required: true},
			b("HIDDEN_NETWORK", "Hidden network", false, false, ""),
			b("AutoJoin", "Auto join", true, false, ""),
			{Key: "EncryptionType", Label: "Security", Type: "enum", Default: "WPA2", Options: opts("None", "None", "WEP", "WEP", "WPA", "WPA/WPA2 Personal", "WPA2", "WPA2 Personal/Enterprise", "WPA3", "WPA3", "Any", "Any (Personal)")},
			{Key: "Password", Label: "Password", Type: "password", ShowIf: map[string][]any{"EncryptionType": {"WEP", "WPA", "WPA2", "WPA3", "Any"}}},
			b("DisableAssociationMACRandomization", "Disable private Wi-Fi address", false, false, ""),
			b("IsHotspot", "Passpoint (Hotspot 2.0)", false, false, ""),
			{Key: "EAPClientConfiguration.AcceptEAPTypes", Label: "Enterprise EAP types", Type: "multienum", Section: "Enterprise (802.1X)",
				Options: opts(13, "TLS", 21, "TTLS", 25, "PEAP", 17, "LEAP", 43, "EAP-FAST", 18, "EAP-SIM", 23, "EAP-AKA")},
			{Key: "EAPClientConfiguration.UserName", Label: "Username", Type: "string", Section: "Enterprise (802.1X)", Help: "Variables like {{user.email}} are supported."},
			{Key: "EAPClientConfiguration.UserPassword", Label: "Password", Type: "password", Section: "Enterprise (802.1X)"},
			{Key: "EAPClientConfiguration.OuterIdentity", Label: "Outer identity", Type: "string", Section: "Enterprise (802.1X)"},
			{Key: "EAPClientConfiguration.TTLSInnerAuthentication", Label: "TTLS inner authentication", Type: "enum", Section: "Enterprise (802.1X)", Options: opts("MSCHAPv2", "MSCHAPv2", "MSCHAP", "MSCHAP", "CHAP", "CHAP", "PAP", "PAP", "EAP", "EAP")},
			{Key: "EAPClientConfiguration.TLSTrustedServerNames", Label: "Trusted server certificate names", Type: "stringlist", Section: "Enterprise (802.1X)"},
			{Key: "PayloadCertificateUUID", Label: "Identity certificate", Type: "payloadref", Section: "Enterprise (802.1X)", RefTypes: []string{"com.apple.security.pkcs12", "com.apple.security.scep"}},
			{Key: "EAPClientConfiguration.PayloadCertificateAnchorUUID", Label: "Trusted server certificates", Type: "payloadrefs", Section: "Enterprise (802.1X)", RefTypes: []string{"com.apple.security.root", "com.apple.security.pkcs1"}},
		}, proxyFields...)},
	{Type: "com.apple.vpn.managed", Name: "VPN", Category: "Network",
		Description: "IKEv2, Cisco IPSec or app-based VPN.",
		Fields: []Field{
			{Key: "UserDefinedName", Label: "Connection name", Type: "string", Required: true},
			{Key: "VPNType", Label: "Type", Type: "enum", Default: "IKEv2", Options: opts("IKEv2", "IKEv2", "IPSec", "Cisco IPSec", "VPN", "App VPN (custom SSL)")},
			{Key: "VPNSubType", Label: "VPN app bundle ID", Type: "string", ShowIf: customVPN, Help: "e.g. com.cisco.anyconnect, com.paloaltonetworks.globalprotect.vpn"},
			{Key: "VPN.RemoteAddress", Label: "Server", Type: "string", ShowIf: customVPN},
			{Key: "VPN.AuthName", Label: "Username", Type: "string", ShowIf: customVPN},
			{Key: "VPN.AuthenticationMethod", Label: "Authentication", Type: "enum", Default: "Password", ShowIf: customVPN, Options: opts("Password", "Password", "Certificate", "Certificate")},
			{Key: "IKEv2.RemoteAddress", Label: "Server", Type: "string", ShowIf: ikev2},
			{Key: "IKEv2.RemoteIdentifier", Label: "Remote ID", Type: "string", ShowIf: ikev2},
			{Key: "IKEv2.LocalIdentifier", Label: "Local ID", Type: "string", ShowIf: ikev2},
			{Key: "IKEv2.AuthenticationMethod", Label: "Machine authentication", Type: "enum", Default: "SharedSecret", ShowIf: ikev2, Options: opts("SharedSecret", "Shared secret", "Certificate", "Certificate", "None", "None (EAP only)")},
			{Key: "IKEv2.SharedSecret", Label: "Shared secret", Type: "password", ShowIf: ikev2},
			{Key: "IKEv2.PayloadCertificateUUID", Label: "Identity certificate", Type: "payloadref", ShowIf: ikev2, RefTypes: []string{"com.apple.security.pkcs12", "com.apple.security.scep"}},
			{Key: "IKEv2.ExtendedAuthEnabled", Label: "Enable EAP (username/password)", Type: "intbool", ShowIf: ikev2},
			{Key: "IKEv2.AuthName", Label: "EAP username", Type: "string", ShowIf: ikev2},
			{Key: "IKEv2.AuthPassword", Label: "EAP password", Type: "password", ShowIf: ikev2},
			{Key: "IKEv2.DeadPeerDetectionRate", Label: "Dead peer detection", Type: "enum", ShowIf: ikev2, Options: opts("Medium", "Medium", "Low", "Low", "High", "High", "None", "None")},
			{Key: "IKEv2.EnablePFS", Label: "Perfect forward secrecy", Type: "intbool", ShowIf: ikev2},
			{Key: "IKEv2.DisableMOBIKE", Label: "Disable MOBIKE", Type: "intbool", ShowIf: ikev2},
			{Key: "IPSec.RemoteAddress", Label: "Server", Type: "string", ShowIf: ipsec},
			{Key: "IPSec.AuthenticationMethod", Label: "Authentication", Type: "enum", Default: "SharedSecret", ShowIf: ipsec, Options: opts("SharedSecret", "Shared secret / group name", "Certificate", "Certificate")},
			{Key: "IPSec.LocalIdentifier", Label: "Group name", Type: "string", ShowIf: ipsec},
			{Key: "IPSec.LocalIdentifierType", Label: "Local identifier type", Type: "enum", Default: "KeyID", ShowIf: ipsec, Options: opts("KeyID", "KeyID")},
			{Key: "IPSec.SharedSecret", Label: "Shared secret", Type: "datastring", ShowIf: ipsec},
			{Key: "IPSec.XAuthEnabled", Label: "Use XAuth", Type: "intbool", ShowIf: ipsec},
			{Key: "IPSec.XAuthName", Label: "Username", Type: "string", ShowIf: ipsec},
			{Key: "IPSec.XAuthPassword", Label: "Password", Type: "password", ShowIf: ipsec},
			{Key: "IPSec.PayloadCertificateUUID", Label: "Identity certificate", Type: "payloadref", ShowIf: ipsec, RefTypes: []string{"com.apple.security.pkcs12", "com.apple.security.scep"}},
			{Key: "_connect_on_demand", Label: "Connect on demand (always on)", Type: "bool", Default: false},
		}},
	{Type: "com.apple.dnsSettings.managed", Name: "Encrypted DNS", Category: "Network", Unique: true,
		Description: "DNS over HTTPS or TLS.",
		Fields: []Field{
			{Key: "DNSSettings.DNSProtocol", Label: "Protocol", Type: "enum", Default: "HTTPS", Options: opts("HTTPS", "DNS over HTTPS", "TLS", "DNS over TLS")},
			{Key: "DNSSettings.ServerURL", Label: "Server URL", Type: "string", ShowIf: map[string][]any{"DNSSettings.DNSProtocol": {"HTTPS"}}, Help: "e.g. https://dns.example.com/dns-query"},
			{Key: "DNSSettings.ServerName", Label: "Server name", Type: "string", ShowIf: map[string][]any{"DNSSettings.DNSProtocol": {"TLS"}}},
			{Key: "DNSSettings.ServerAddresses", Label: "Server addresses", Type: "stringlist"},
			{Key: "ProhibitDisablement", Label: "Prevent users from disabling", Type: "bool", Default: false, Supervised: true},
		}},
	{Type: "com.apple.proxy.http.global", Name: "Global HTTP proxy", Category: "Network", Unique: true, Supervised: true,
		Description: "Route all HTTP traffic through a proxy (supervised).",
		Fields: []Field{
			{Key: "ProxyType", Label: "Type", Type: "enum", Default: "Manual", Options: opts("Manual", "Manual", "Auto", "Automatic (PAC)")},
			{Key: "ProxyServer", Label: "Server", Type: "string", ShowIf: map[string][]any{"ProxyType": {"Manual"}}},
			{Key: "ProxyServerPort", Label: "Port", Type: "int", ShowIf: map[string][]any{"ProxyType": {"Manual"}}},
			{Key: "ProxyUsername", Label: "Username", Type: "string", ShowIf: map[string][]any{"ProxyType": {"Manual"}}},
			{Key: "ProxyPassword", Label: "Password", Type: "password", ShowIf: map[string][]any{"ProxyType": {"Manual"}}},
			{Key: "ProxyPACURL", Label: "PAC URL", Type: "string", ShowIf: map[string][]any{"ProxyType": {"Auto"}}},
			{Key: "ProxyPACFallbackAllowed", Label: "Allow direct connection if PAC unreachable", Type: "bool", Default: false},
			{Key: "ProxyCaptiveLoginAllowed", Label: "Allow captive network login bypass", Type: "bool", Default: false},
		}},
	{Type: "com.apple.cellular", Name: "Cellular", Category: "Network", Unique: true,
		Description: "Cellular data APN settings.",
		Fields: []Field{
			{Key: "AttachAPN.Name", Label: "Attach APN name", Type: "string"},
			{Key: "AttachAPN.AuthenticationType", Label: "Attach APN authentication", Type: "enum", Options: opts("CHAP", "CHAP", "PAP", "PAP")},
			{Key: "AttachAPN.Username", Label: "Attach APN username", Type: "string"},
			{Key: "AttachAPN.Password", Label: "Attach APN password", Type: "password"},
			{Key: "APNs", Label: "Data APNs", Type: "dictlist", Fields: []Field{
				{Key: "Name", Label: "APN", Type: "string", Required: true},
				{Key: "AuthenticationType", Label: "Authentication", Type: "enum", Options: opts("CHAP", "CHAP", "PAP", "PAP")},
				{Key: "Username", Label: "Username", Type: "string"},
				{Key: "Password", Label: "Password", Type: "password"},
				{Key: "ProxyServer", Label: "Proxy", Type: "string"},
				{Key: "ProxyPort", Label: "Proxy port", Type: "int"},
			}},
		}},
	{Type: "com.apple.networkusagerules", Name: "Cellular data usage", Category: "Network", Unique: true,
		Description: "Control cellular data use per app.",
		Fields: []Field{
			{Key: "ApplicationRules", Label: "Rules", Type: "dictlist", Fields: []Field{
				{Key: "AppIdentifierMatches", Label: "App bundle IDs (wildcards allowed)", Type: "stringlist", Required: true},
				{Key: "AllowCellularData", Label: "Allow cellular data", Type: "bool", Default: true},
				{Key: "AllowRoamingCellularData", Label: "Allow roaming data", Type: "bool", Default: true},
			}},
		}},
	{Type: "com.apple.mail.managed", Name: "Mail (IMAP/POP)", Category: "Accounts",
		Description: "Configure an IMAP or POP mail account.",
		Fields: []Field{
			{Key: "EmailAccountDescription", Label: "Account description", Type: "string"},
			{Key: "EmailAccountName", Label: "User display name", Type: "string", Help: "Variables like {{user.name}} are supported."},
			{Key: "EmailAccountType", Label: "Account type", Type: "enum", Default: "EmailTypeIMAP", Options: opts("EmailTypeIMAP", "IMAP", "EmailTypePOP", "POP")},
			{Key: "EmailAddress", Label: "Email address", Type: "string", Help: "{{user.email}}"},
			{Key: "IncomingMailServerHostName", Label: "Incoming server", Type: "string", Required: true, Section: "Incoming"},
			{Key: "IncomingMailServerPortNumber", Label: "Port", Type: "int", Section: "Incoming"},
			{Key: "IncomingMailServerUseSSL", Label: "Use SSL", Type: "bool", Default: true, Section: "Incoming"},
			{Key: "IncomingMailServerUsername", Label: "Username", Type: "string", Section: "Incoming"},
			{Key: "IncomingMailServerAuthentication", Label: "Authentication", Type: "enum", Default: "EmailAuthPassword", Section: "Incoming",
				Options: opts("EmailAuthPassword", "Password", "EmailAuthCRAMMD5", "MD5 challenge-response", "EmailAuthNTLM", "NTLM", "EmailAuthHTTPMD5", "HTTP MD5 digest", "EmailAuthNone", "None")},
			{Key: "IncomingPassword", Label: "Password", Type: "password", Section: "Incoming"},
			{Key: "OutgoingMailServerHostName", Label: "Outgoing server", Type: "string", Required: true, Section: "Outgoing"},
			{Key: "OutgoingMailServerPortNumber", Label: "Port", Type: "int", Section: "Outgoing"},
			{Key: "OutgoingMailServerUseSSL", Label: "Use SSL", Type: "bool", Default: true, Section: "Outgoing"},
			{Key: "OutgoingMailServerUsername", Label: "Username", Type: "string", Section: "Outgoing"},
			{Key: "OutgoingMailServerAuthentication", Label: "Authentication", Type: "enum", Default: "EmailAuthPassword", Section: "Outgoing",
				Options: opts("EmailAuthPassword", "Password", "EmailAuthCRAMMD5", "MD5 challenge-response", "EmailAuthNTLM", "NTLM", "EmailAuthHTTPMD5", "HTTP MD5 digest", "EmailAuthNone", "None")},
			{Key: "OutgoingPassword", Label: "Password", Type: "password", Section: "Outgoing"},
			{Key: "OutgoingPasswordSameAsIncomingPassword", Label: "Same password as incoming", Type: "bool", Default: false, Section: "Outgoing"},
			{Key: "PreventMove", Label: "Prevent moving messages to other accounts", Type: "bool", Default: false, Section: "Options"},
			{Key: "PreventAppSheet", Label: "Only allow sending from Mail", Type: "bool", Default: false, Section: "Options"},
			{Key: "disableMailRecentsSyncing", Label: "Disable recent addresses syncing", Type: "bool", Default: false, Section: "Options"},
			{Key: "allowMailDrop", Label: "Allow Mail Drop", Type: "bool", Default: true, Section: "Options"},
			{Key: "SMIMESigningEnabled", Label: "S/MIME signing", Type: "bool", Default: false, Section: "Options"},
			{Key: "SMIMEEncryptByDefault", Label: "S/MIME encrypt by default", Type: "bool", Default: false, Section: "Options"},
		}},
	{Type: "com.apple.eas.account", Name: "Exchange ActiveSync", Category: "Accounts",
		Description: "Exchange / Microsoft 365 mail, contacts and calendars.",
		Fields: []Field{
			{Key: "PayloadDisplayName", Label: "Account name", Type: "string", Default: "Exchange"},
			{Key: "Host", Label: "Server", Type: "string", Required: true, Help: "e.g. outlook.office365.com"},
			{Key: "SSL", Label: "Use SSL", Type: "bool", Default: true},
			{Key: "EmailAddress", Label: "Email address", Type: "string", Help: "{{user.email}}"},
			{Key: "UserName", Label: "Username", Type: "string", Help: "{{user.email}}"},
			{Key: "Password", Label: "Password", Type: "password"},
			{Key: "OAuth", Label: "Use OAuth (modern authentication)", Type: "bool", Default: false},
			{Key: "OAuthSignInURL", Label: "OAuth sign-in URL", Type: "string"},
			{Key: "MailNumberOfPastDaysToSync", Label: "Past days of mail to sync", Type: "enum", Options: opts(0, "No limit", 1, "1 day", 3, "3 days", 7, "1 week", 14, "2 weeks", 31, "1 month")},
			{Key: "PreventMove", Label: "Prevent moving messages", Type: "bool", Default: false},
			{Key: "PreventAppSheet", Label: "Only allow sending from Mail", Type: "bool", Default: false},
			{Key: "disableMailRecentsSyncing", Label: "Disable recent addresses syncing", Type: "bool", Default: false},
			{Key: "allowMailDrop", Label: "Allow Mail Drop", Type: "bool", Default: true},
			{Key: "SMIMESigningEnabled", Label: "S/MIME signing", Type: "bool", Default: false},
		}},
	{Type: "com.apple.google-oauth", Name: "Google account", Category: "Accounts",
		Description: "Prompt the user to sign in to a Google account.",
		Fields: []Field{
			{Key: "AccountDescription", Label: "Description", Type: "string"},
			{Key: "AccountName", Label: "Name", Type: "string", Help: "{{user.name}}"},
			{Key: "EmailAddress", Label: "Email address", Type: "string", Required: true, Help: "{{user.email}}"},
		}},
	{Type: "com.apple.caldav.account", Name: "Calendar (CalDAV)", Category: "Accounts", Description: "Add a CalDAV calendar account.",
		Fields: []Field{
			{Key: "CalDAVAccountDescription", Label: "Description", Type: "string"},
			{Key: "CalDAVHostName", Label: "Server", Type: "string", Required: true},
			{Key: "CalDAVPort", Label: "Port", Type: "int"},
			{Key: "CalDAVPrincipalURL", Label: "Principal URL", Type: "string"},
			{Key: "CalDAVUsername", Label: "Username", Type: "string"},
			{Key: "CalDAVPassword", Label: "Password", Type: "password"},
			{Key: "CalDAVUseSSL", Label: "Use SSL", Type: "bool", Default: true},
		}},
	{Type: "com.apple.carddav.account", Name: "Contacts (CardDAV)", Category: "Accounts", Description: "Add a CardDAV contacts account.",
		Fields: []Field{
			{Key: "CardDAVAccountDescription", Label: "Description", Type: "string"},
			{Key: "CardDAVHostName", Label: "Server", Type: "string", Required: true},
			{Key: "CardDAVPort", Label: "Port", Type: "int"},
			{Key: "CardDAVPrincipalURL", Label: "Principal URL", Type: "string"},
			{Key: "CardDAVUsername", Label: "Username", Type: "string"},
			{Key: "CardDAVPassword", Label: "Password", Type: "password"},
			{Key: "CardDAVUseSSL", Label: "Use SSL", Type: "bool", Default: true},
		}},
	{Type: "com.apple.subscribedcalendar.account", Name: "Subscribed calendar", Category: "Accounts", Description: "Subscribe to a read-only calendar (.ics URL).",
		Fields: []Field{
			{Key: "SubCalAccountDescription", Label: "Description", Type: "string"},
			{Key: "SubCalAccountHostName", Label: "Calendar URL", Type: "string", Required: true},
			{Key: "SubCalAccountUsername", Label: "Username", Type: "string"},
			{Key: "SubCalAccountPassword", Label: "Password", Type: "password"},
			{Key: "SubCalAccountUseSSL", Label: "Use SSL", Type: "bool", Default: true},
		}},
	{Type: "com.apple.ldap.account", Name: "LDAP directory", Category: "Accounts", Description: "Look up people in a company directory.",
		Fields: []Field{
			{Key: "LDAPAccountDescription", Label: "Description", Type: "string"},
			{Key: "LDAPAccountHostName", Label: "Server", Type: "string", Required: true},
			{Key: "LDAPAccountUseSSL", Label: "Use SSL", Type: "bool", Default: true},
			{Key: "LDAPAccountUserName", Label: "Bind user", Type: "string"},
			{Key: "LDAPAccountPassword", Label: "Password", Type: "password"},
			{Key: "LDAPSearchSettings", Label: "Search settings", Type: "dictlist", Fields: []Field{
				{Key: "LDAPSearchSettingDescription", Label: "Description", Type: "string"},
				{Key: "LDAPSearchSettingSearchBase", Label: "Search base", Type: "string", Required: true},
				{Key: "LDAPSearchSettingScope", Label: "Scope", Type: "enum", Default: "LDAPSearchSettingScopeSubtree",
					Options: opts("LDAPSearchSettingScopeSubtree", "Subtree", "LDAPSearchSettingScopeOneLevel", "One level", "LDAPSearchSettingScopeBase", "Base")},
			}},
		}},
	{Type: "com.apple.webClip.managed", Name: "Web Clip", Category: "Home Screen",
		Description: "Add a website shortcut to the Home Screen.",
		Fields: []Field{
			{Key: "Label", Label: "Label", Type: "string", Required: true},
			{Key: "URL", Label: "URL", Type: "string", Required: true},
			{Key: "Icon", Label: "Icon (PNG)", Type: "image"},
			{Key: "IsRemovable", Label: "Removable", Type: "bool", Default: true},
			{Key: "FullScreen", Label: "Open full screen", Type: "bool", Default: false},
			{Key: "Precomposed", Label: "Precomposed icon", Type: "bool", Default: false},
			{Key: "IgnoreManifestScope", Label: "Ignore manifest scope", Type: "bool", Default: false},
		}},
	{Type: "com.apple.homescreenlayout", Name: "Home Screen layout", Category: "Home Screen", Unique: true, Supervised: true,
		Description: "Arrange the Dock and Home Screen pages (supervised).",
		Fields: []Field{
			{Key: "Dock", Label: "Dock apps (bundle IDs)", Type: "dock", Help: "e.g. com.apple.mobilephone"},
			{Key: "Pages", Label: "Pages", Type: "pages", Help: "One bundle ID per line; separate pages with an empty line. Folders: “Folder Name: id1, id2”."},
		}},
	{Type: "com.apple.notificationsettings", Name: "Notifications", Category: "Home Screen", Unique: true, Supervised: true,
		Description: "Per-app notification settings (supervised).",
		Fields: []Field{
			{Key: "NotificationSettings", Label: "Apps", Type: "dictlist", Fields: []Field{
				{Key: "BundleIdentifier", Label: "Bundle ID", Type: "string", Required: true},
				{Key: "NotificationsEnabled", Label: "Allow notifications", Type: "bool", Default: true},
				{Key: "ShowInLockScreen", Label: "Show on lock screen", Type: "bool", Default: true},
				{Key: "ShowInNotificationCenter", Label: "Show in Notification Center", Type: "bool", Default: true},
				{Key: "AlertType", Label: "Alert style", Type: "enum", Default: 1, Options: opts(0, "None", 1, "Banner", 2, "Alert")},
				{Key: "BadgesEnabled", Label: "Badges", Type: "bool", Default: true},
				{Key: "SoundsEnabled", Label: "Sounds", Type: "bool", Default: true},
				{Key: "CriticalAlertEnabled", Label: "Critical alerts", Type: "bool", Default: false},
				{Key: "PreviewType", Label: "Previews", Type: "enum", Default: 0, Options: opts(0, "Always", 1, "When unlocked", 2, "Never")},
			}},
		}},
	{Type: "com.apple.shareddeviceconfiguration", Name: "Lock Screen message", Category: "Home Screen", Unique: true, Supervised: true,
		Description: "Asset tag / return message on the lock screen (supervised).",
		Fields: []Field{
			{Key: "LockScreenFootnote", Label: "Lock screen footnote", Type: "string", Help: "e.g. If found, return to IT — {{device.asset_tag}}"},
			{Key: "AssetTagInformation", Label: "Asset tag", Type: "string", Help: "{{device.asset_tag}}"},
		}},
	{Type: "com.apple.app.lock", Name: "Single App Mode", Category: "Home Screen", Unique: true, Supervised: true,
		Description: "Lock the device to one app (kiosk). Supervised only.",
		Fields: []Field{
			{Key: "App.Identifier", Label: "App bundle ID", Type: "string", Required: true},
			{Key: "App.Options.DisableTouch", Label: "Disable touch", Type: "bool", Default: false},
			{Key: "App.Options.DisableDeviceRotation", Label: "Disable rotation", Type: "bool", Default: false},
			{Key: "App.Options.DisableVolumeButtons", Label: "Disable volume buttons", Type: "bool", Default: false},
			{Key: "App.Options.DisableRingerSwitch", Label: "Disable ringer switch", Type: "bool", Default: false},
			{Key: "App.Options.DisableSleepWakeButton", Label: "Disable sleep/wake button", Type: "bool", Default: false},
			{Key: "App.Options.DisableAutoLock", Label: "Disable auto-lock", Type: "bool", Default: false},
			{Key: "App.Options.EnableVoiceOver", Label: "VoiceOver", Type: "bool", Default: false},
			{Key: "App.Options.EnableZoom", Label: "Zoom", Type: "bool", Default: false},
			{Key: "App.Options.EnableInvertColors", Label: "Invert colors", Type: "bool", Default: false},
			{Key: "App.Options.EnableAssistiveTouch", Label: "AssistiveTouch", Type: "bool", Default: false},
			{Key: "App.Options.EnableMonoAudio", Label: "Mono audio", Type: "bool", Default: false},
		}},
	{Type: "com.apple.webcontent-filter", Name: "Web content filter", Category: "Security", Unique: true, Supervised: true,
		Description: "Built-in Safari content filter (supervised).",
		Fields: []Field{
			{Key: "FilterType", Label: "Filter type", Type: "enum", Default: "BuiltIn", Options: opts("BuiltIn", "Built-in")},
			{Key: "AutoFilterEnabled", Label: "Limit adult content", Type: "bool", Default: false},
			{Key: "PermittedURLs", Label: "Always allowed URLs", Type: "stringlist"},
			{Key: "DenyListURLs", Label: "Blocked URLs", Type: "stringlist"},
			{Key: "AllowListBookmarks", Label: "Only allow these sites", Type: "dictlist", Fields: []Field{
				{Key: "URL", Label: "URL", Type: "string", Required: true},
				{Key: "Title", Label: "Title", Type: "string"},
			}},
		}},
	{Type: "com.apple.domains", Name: "Managed domains", Category: "Security", Unique: true,
		Description: "Mark email and web domains as managed.",
		Fields: []Field{
			{Key: "EmailDomains", Label: "Managed email domains", Type: "stringlist"},
			{Key: "WebDomains", Label: "Managed Safari web domains", Type: "stringlist"},
			{Key: "SafariPasswordAutoFillDomains", Label: "Password AutoFill domains", Type: "stringlist", Supervised: true},
		}},
	{Type: "com.apple.security.root", Name: "Root certificate", Category: "Certificates",
		Description: "Install a trusted root or intermediate certificate (PEM or DER).",
		Fields: []Field{
			{Key: "PayloadCertificateFileName", Label: "File name", Type: "string"},
			{Key: "PayloadContent", Label: "Certificate", Type: "cert", Required: true},
		}},
	{Type: "com.apple.security.pkcs12", Name: "Identity certificate (.p12)", Category: "Certificates",
		Description: "Install a certificate with its private key.",
		Fields: []Field{
			{Key: "PayloadCertificateFileName", Label: "File name", Type: "string"},
			{Key: "PayloadContent", Label: "PKCS#12 file", Type: "data", Required: true},
			{Key: "Password", Label: "Password", Type: "password"},
		}},
	{Type: "com.apple.security.scep", Name: "SCEP certificate", Category: "Certificates",
		Description: "Request a certificate from a SCEP server (e.g. for Wi-Fi/VPN).",
		Fields: []Field{
			{Key: "PayloadContent.URL", Label: "SCEP URL", Type: "string", Required: true},
			{Key: "PayloadContent.Name", Label: "CA name", Type: "string"},
			{Key: "PayloadContent.Subject", Label: "Subject", Type: "x500", Help: "e.g. /O=Example/CN={{device.serial}}"},
			{Key: "PayloadContent.Challenge", Label: "Challenge", Type: "password"},
			{Key: "PayloadContent.Keysize", Label: "Key size", Type: "enum", Default: 2048, Options: opts(2048, "2048", 4096, "4096", 1024, "1024")},
			{Key: "PayloadContent.Key Type", Label: "Key type", Type: "enum", Default: "RSA", Options: opts("RSA", "RSA")},
			{Key: "PayloadContent.Key Usage", Label: "Key usage", Type: "enum", Default: 5, Options: opts(5, "Signing and encryption", 1, "Signing", 4, "Encryption")},
			{Key: "PayloadContent.SubjectAltName.ntPrincipalName", Label: "NT principal name", Type: "string"},
			{Key: "PayloadContent.SubjectAltName.rfc822Name", Label: "Email (SAN)", Type: "string"},
			{Key: "PayloadContent.Retries", Label: "Retries", Type: "int"},
			{Key: "PayloadContent.RetryDelay", Label: "Retry delay (seconds)", Type: "int"},
		}},
	{Type: "com.apple.airplay", Name: "AirPlay", Category: "Other", Unique: true, Description: "Saved AirPlay passwords and allowed destinations.",
		Fields: []Field{
			{Key: "AllowList", Label: "Allowed destinations (supervised)", Type: "dictlist", Fields: []Field{{Key: "DeviceID", Label: "Device ID (MAC)", Type: "string", Required: true}}},
			{Key: "Passwords", Label: "Destination passwords", Type: "dictlist", Fields: []Field{
				{Key: "DeviceName", Label: "Device name", Type: "string", Required: true},
				{Key: "Password", Label: "Password", Type: "password", Required: true}}},
		}},
	{Type: "com.apple.airprint", Name: "AirPrint", Category: "Other", Unique: true, Description: "Add printers that are on another network.",
		Fields: []Field{
			{Key: "AirPrint", Label: "Printers", Type: "dictlist", Fields: []Field{
				{Key: "IPAddress", Label: "IP address / host", Type: "string", Required: true},
				{Key: "ResourcePath", Label: "Resource path", Type: "string", Required: true, Help: "e.g. ipp/print"},
				{Key: "Port", Label: "Port", Type: "int"},
				{Key: "ForceTLS", Label: "Require TLS", Type: "bool", Default: false},
			}},
		}},
	{Type: "com.apple.font", Name: "Font", Category: "Other", Description: "Install a TrueType or OpenType font.",
		Fields: []Field{
			{Key: "Name", Label: "Font name", Type: "string"},
			{Key: "Font", Label: "Font file (TTF/OTF)", Type: "data", Required: true},
		}},
	{Type: "custom", Name: "Custom payload", Category: "Other",
		Description: "Any payload type as raw plist keys (advanced).",
		Fields: []Field{
			{Key: "_PayloadType", Label: "PayloadType", Type: "string", Required: true},
			{Key: "_plist", Label: "Payload keys (XML <dict>…</dict>)", Type: "plistdict", Required: true},
		}},
}

// SchemaByType finds a schema.
func SchemaByType(t string) (*Schema, bool) {
	for i := range Schemas {
		if Schemas[i].Type == t {
			return &Schemas[i], true
		}
	}
	return nil, false
}

// userEnrollmentForbidden lists payload types iOS rejects on User Enrollment
// (personal) devices, from Apple's device-management schema.
var userEnrollmentForbidden = map[string]bool{
	"com.apple.cellular": true, "com.apple.homescreenlayout": true, "com.apple.notificationsettings": true, "com.apple.proxy.http.global": true,
	"com.apple.shareddeviceconfiguration": true, "com.apple.app.lock": true, "com.apple.dnsSettings.managed": true, "com.apple.domains": true,
	"com.apple.networkusagerules": true, "com.apple.vpn.managed": true, "com.apple.apn.managed": true, "com.apple.SetupAssistant.managed": true,
	"com.apple.profileRemovalPassword": true, "com.apple.tvremote": true, "com.apple.osxserver.account": true, "com.apple.cellularprivatenetwork.managed": true,
}

// UnsupportedOnUserEnrollment returns the payload types that personal devices
// enrolled with User Enrollment won't accept.
func UnsupportedOnUserEnrollment(types []string) []string {
	var out []string
	for _, t := range types {
		if userEnrollmentForbidden[t] {
			out = append(out, t)
		}
	}
	return out
}

func init() {
	for i := range Schemas {
		Schemas[i].UserEnrollment = !userEnrollmentForbidden[Schemas[i].Type]
	}
}
