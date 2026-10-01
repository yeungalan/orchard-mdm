package store

// migrations are applied in order; never edit an existing entry, append a new one.
var migrations = []string{
	// 1: initial schema
	`
CREATE TABLE settings (
	key   TEXT PRIMARY KEY,
	value TEXT NOT NULL
);

CREATE TABLE keypairs (
	name       TEXT PRIMARY KEY,
	cert_pem   TEXT NOT NULL DEFAULT '',
	key_pem    TEXT NOT NULL DEFAULT '',
	meta       TEXT NOT NULL DEFAULT '',
	created_at INTEGER NOT NULL
);

CREATE TABLE users (
	id            INTEGER PRIMARY KEY AUTOINCREMENT,
	username      TEXT NOT NULL UNIQUE COLLATE NOCASE,
	display_name  TEXT NOT NULL DEFAULT '',
	email         TEXT NOT NULL DEFAULT '',
	password_hash TEXT NOT NULL,
	role          TEXT NOT NULL,
	disabled      INTEGER NOT NULL DEFAULT 0,
	created_at    INTEGER NOT NULL,
	last_login    INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE sessions (
	token_hash TEXT PRIMARY KEY,
	user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	csrf       TEXT NOT NULL,
	ip         TEXT NOT NULL DEFAULT '',
	user_agent TEXT NOT NULL DEFAULT '',
	created_at INTEGER NOT NULL,
	expires_at INTEGER NOT NULL
);

CREATE TABLE api_keys (
	id         INTEGER PRIMARY KEY AUTOINCREMENT,
	name       TEXT NOT NULL,
	prefix     TEXT NOT NULL,
	key_hash   TEXT NOT NULL UNIQUE,
	role       TEXT NOT NULL,
	created_by TEXT NOT NULL DEFAULT '',
	created_at INTEGER NOT NULL,
	last_used  INTEGER NOT NULL DEFAULT 0,
	expires_at INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE devices (
	udid                   TEXT PRIMARY KEY,
	serial_number          TEXT NOT NULL DEFAULT '',
	imei                   TEXT NOT NULL DEFAULT '',
	meid                   TEXT NOT NULL DEFAULT '',
	device_name            TEXT NOT NULL DEFAULT '',
	model                  TEXT NOT NULL DEFAULT '',
	model_name             TEXT NOT NULL DEFAULT '',
	product_name           TEXT NOT NULL DEFAULT '',
	os_version             TEXT NOT NULL DEFAULT '',
	build_version          TEXT NOT NULL DEFAULT '',
	enrollment_status      TEXT NOT NULL DEFAULT 'pending',
	enrollment_type        TEXT NOT NULL DEFAULT 'manual',
	ownership              TEXT NOT NULL DEFAULT 'unknown',
	enrollment_token_id    INTEGER NOT NULL DEFAULT 0,
	enrolled_at            INTEGER NOT NULL DEFAULT 0,
	unenrolled_at          INTEGER NOT NULL DEFAULT 0,
	last_seen              INTEGER NOT NULL DEFAULT 0,
	last_inventory         INTEGER NOT NULL DEFAULT 0,
	last_push              INTEGER NOT NULL DEFAULT 0,
	topic                  TEXT NOT NULL DEFAULT '',
	push_token             TEXT NOT NULL DEFAULT '',
	push_magic             TEXT NOT NULL DEFAULT '',
	unlock_token           BLOB,
	bootstrap_token        BLOB,
	cert_fingerprint       TEXT NOT NULL DEFAULT '',
	cert_not_after         INTEGER NOT NULL DEFAULT 0,
	supervised             INTEGER NOT NULL DEFAULT 0,
	dep_enrolled           INTEGER NOT NULL DEFAULT 0,
	awaiting_configuration INTEGER NOT NULL DEFAULT 0,
	activation_lock        INTEGER NOT NULL DEFAULT 0,
	find_my                INTEGER NOT NULL DEFAULT 0,
	passcode_present       INTEGER NOT NULL DEFAULT 0,
	passcode_compliant     INTEGER NOT NULL DEFAULT 0,
	encryption_caps        INTEGER NOT NULL DEFAULT 0,
	lost_mode              INTEGER NOT NULL DEFAULT 0,
	battery_level          REAL NOT NULL DEFAULT -1,
	capacity_gb            REAL NOT NULL DEFAULT 0,
	available_gb           REAL NOT NULL DEFAULT 0,
	wifi_mac               TEXT NOT NULL DEFAULT '',
	bluetooth_mac          TEXT NOT NULL DEFAULT '',
	phone_number           TEXT NOT NULL DEFAULT '',
	carrier                TEXT NOT NULL DEFAULT '',
	iccid                  TEXT NOT NULL DEFAULT '',
	compliance             TEXT NOT NULL DEFAULT 'unknown',
	compliance_reasons     TEXT NOT NULL DEFAULT '',
	noncompliant_since     INTEGER NOT NULL DEFAULT 0,
	portal_token           TEXT NOT NULL DEFAULT '',
	tags                   TEXT NOT NULL DEFAULT '',
	notes                  TEXT NOT NULL DEFAULT '',
	assigned_user          TEXT NOT NULL DEFAULT '',
	assigned_email         TEXT NOT NULL DEFAULT '',
	asset_tag              TEXT NOT NULL DEFAULT '',
	activation_lock_bypass TEXT NOT NULL DEFAULT '',
	location_json          TEXT NOT NULL DEFAULT '',
	location_at            INTEGER NOT NULL DEFAULT 0,
	info_json              TEXT NOT NULL DEFAULT '',
	security_json          TEXT NOT NULL DEFAULT '',
	restrictions_json      TEXT NOT NULL DEFAULT '',
	os_updates_json        TEXT NOT NULL DEFAULT '',
	os_update_status_json  TEXT NOT NULL DEFAULT '',
	provisioning_json      TEXT NOT NULL DEFAULT '',
	media_json             TEXT NOT NULL DEFAULT '',
	ddm_status_json        TEXT NOT NULL DEFAULT '',
	ddm_token              TEXT NOT NULL DEFAULT '',
	ddm_last_status        INTEGER NOT NULL DEFAULT 0,
	last_ip                TEXT NOT NULL DEFAULT '',
	ssid                   TEXT NOT NULL DEFAULT '',
	bssid                  TEXT NOT NULL DEFAULT '',
	local_ip               TEXT NOT NULL DEFAULT '',
	battery_state          TEXT NOT NULL DEFAULT '',
	battery_health         TEXT NOT NULL DEFAULT '',
	cellular_technology    TEXT NOT NULL DEFAULT '',
	roaming                INTEGER NOT NULL DEFAULT 0,
	hotspot                INTEGER NOT NULL DEFAULT 0,
	agent_token            TEXT NOT NULL DEFAULT '',
	agent_last_seen        INTEGER NOT NULL DEFAULT 0,
	telemetry_at           INTEGER NOT NULL DEFAULT 0,
	created_at             INTEGER NOT NULL,
	updated_at             INTEGER NOT NULL
);
CREATE INDEX devices_serial ON devices(serial_number);
CREATE INDEX devices_portal ON devices(portal_token);
CREATE INDEX devices_status ON devices(enrollment_status);
CREATE INDEX devices_agent ON devices(agent_token);

CREATE TABLE device_telemetry (
	id          INTEGER PRIMARY KEY AUTOINCREMENT,
	device_id   TEXT NOT NULL REFERENCES devices(udid) ON DELETE CASCADE,
	ts          INTEGER NOT NULL,
	source      TEXT NOT NULL,
	battery     REAL NOT NULL DEFAULT -1,
	battery_state TEXT NOT NULL DEFAULT '',
	available_gb REAL NOT NULL DEFAULT -1,
	ssid        TEXT NOT NULL DEFAULT '',
	bssid       TEXT NOT NULL DEFAULT '',
	ip          TEXT NOT NULL DEFAULT '',
	local_ip    TEXT NOT NULL DEFAULT '',
	carrier     TEXT NOT NULL DEFAULT '',
	cellular_technology TEXT NOT NULL DEFAULT '',
	roaming     INTEGER NOT NULL DEFAULT 0,
	data        TEXT NOT NULL DEFAULT ''
);
CREATE INDEX device_telemetry_dev ON device_telemetry(device_id, ts);

CREATE TABLE device_locations (
	id          INTEGER PRIMARY KEY AUTOINCREMENT,
	device_id   TEXT NOT NULL REFERENCES devices(udid) ON DELETE CASCADE,
	ts          INTEGER NOT NULL,
	source      TEXT NOT NULL,
	latitude    REAL NOT NULL,
	longitude   REAL NOT NULL,
	accuracy    REAL NOT NULL DEFAULT 0,
	altitude    REAL NOT NULL DEFAULT 0,
	speed       REAL NOT NULL DEFAULT -1,
	course      REAL NOT NULL DEFAULT -1
);
CREATE INDEX device_locations_dev ON device_locations(device_id, ts);

CREATE TABLE device_apps (
	device_id     TEXT NOT NULL REFERENCES devices(udid) ON DELETE CASCADE,
	bundle_id     TEXT NOT NULL,
	name          TEXT NOT NULL DEFAULT '',
	version       TEXT NOT NULL DEFAULT '',
	short_version TEXT NOT NULL DEFAULT '',
	bundle_size   INTEGER NOT NULL DEFAULT 0,
	dynamic_size  INTEGER NOT NULL DEFAULT 0,
	is_managed    INTEGER NOT NULL DEFAULT 0,
	managed_status TEXT NOT NULL DEFAULT '',
	has_config    INTEGER NOT NULL DEFAULT 0,
	PRIMARY KEY (device_id, bundle_id)
);

CREATE TABLE device_profiles (
	device_id     TEXT NOT NULL REFERENCES devices(udid) ON DELETE CASCADE,
	identifier    TEXT NOT NULL,
	display_name  TEXT NOT NULL DEFAULT '',
	organization  TEXT NOT NULL DEFAULT '',
	description   TEXT NOT NULL DEFAULT '',
	uuid          TEXT NOT NULL DEFAULT '',
	version       INTEGER NOT NULL DEFAULT 0,
	is_managed    INTEGER NOT NULL DEFAULT 0,
	is_encrypted  INTEGER NOT NULL DEFAULT 0,
	removal_disallowed INTEGER NOT NULL DEFAULT 0,
	payload_types TEXT NOT NULL DEFAULT '',
	PRIMARY KEY (device_id, identifier)
);

CREATE TABLE device_certificates (
	device_id   TEXT NOT NULL REFERENCES devices(udid) ON DELETE CASCADE,
	sha256      TEXT NOT NULL,
	common_name TEXT NOT NULL DEFAULT '',
	subject     TEXT NOT NULL DEFAULT '',
	issuer      TEXT NOT NULL DEFAULT '',
	is_identity INTEGER NOT NULL DEFAULT 0,
	not_before  INTEGER NOT NULL DEFAULT 0,
	not_after   INTEGER NOT NULL DEFAULT 0,
	PRIMARY KEY (device_id, sha256)
);

CREATE TABLE commands (
	uuid         TEXT PRIMARY KEY,
	device_id    TEXT NOT NULL,
	request_type TEXT NOT NULL,
	payload      BLOB NOT NULL,
	status       TEXT NOT NULL DEFAULT 'Queued',
	result       BLOB,
	error_text   TEXT NOT NULL DEFAULT '',
	created_at   INTEGER NOT NULL,
	sent_at      INTEGER NOT NULL DEFAULT 0,
	completed_at INTEGER NOT NULL DEFAULT 0,
	created_by   TEXT NOT NULL DEFAULT '',
	source       TEXT NOT NULL DEFAULT '',
	ref          TEXT NOT NULL DEFAULT ''
);
CREATE INDEX commands_device ON commands(device_id, status, created_at);
CREATE INDEX commands_created ON commands(created_at);
CREATE INDEX commands_ref ON commands(device_id, ref);

CREATE TABLE groups (
	id          INTEGER PRIMARY KEY AUTOINCREMENT,
	name        TEXT NOT NULL UNIQUE COLLATE NOCASE,
	description TEXT NOT NULL DEFAULT '',
	kind        TEXT NOT NULL DEFAULT 'static',
	rules       TEXT NOT NULL DEFAULT '',
	created_at  INTEGER NOT NULL,
	updated_at  INTEGER NOT NULL
);

CREATE TABLE group_members (
	group_id  INTEGER NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
	device_id TEXT NOT NULL REFERENCES devices(udid) ON DELETE CASCADE,
	PRIMARY KEY (group_id, device_id)
);
CREATE INDEX group_members_device ON group_members(device_id);

CREATE TABLE profiles (
	id          INTEGER PRIMARY KEY AUTOINCREMENT,
	name        TEXT NOT NULL,
	identifier  TEXT NOT NULL UNIQUE,
	description TEXT NOT NULL DEFAULT '',
	source      TEXT NOT NULL DEFAULT 'builder',
	payloads    TEXT NOT NULL DEFAULT '',
	raw         BLOB,
	version     INTEGER NOT NULL DEFAULT 1,
	removable   INTEGER NOT NULL DEFAULT 1,
	scope       TEXT NOT NULL DEFAULT 'System',
	payload_types TEXT NOT NULL DEFAULT '',
	created_at  INTEGER NOT NULL,
	updated_at  INTEGER NOT NULL
);

CREATE TABLE apps (
	id               INTEGER PRIMARY KEY AUTOINCREMENT,
	kind             TEXT NOT NULL,
	name             TEXT NOT NULL,
	bundle_id        TEXT NOT NULL DEFAULT '',
	itunes_id        INTEGER NOT NULL DEFAULT 0,
	version          TEXT NOT NULL DEFAULT '',
	icon_url         TEXT NOT NULL DEFAULT '',
	seller           TEXT NOT NULL DEFAULT '',
	description      TEXT NOT NULL DEFAULT '',
	ipa_file         TEXT NOT NULL DEFAULT '',
	ipa_size         INTEGER NOT NULL DEFAULT 0,
	file_secret      TEXT NOT NULL DEFAULT '',
	config           TEXT NOT NULL DEFAULT '',
	attributes       TEXT NOT NULL DEFAULT '',
	remove_on_unenroll INTEGER NOT NULL DEFAULT 1,
	prevent_backup   INTEGER NOT NULL DEFAULT 0,
	use_vpp          INTEGER NOT NULL DEFAULT 0,
	take_management  INTEGER NOT NULL DEFAULT 1,
	created_at       INTEGER NOT NULL,
	updated_at       INTEGER NOT NULL
);

CREATE TABLE declarations (
	id           INTEGER PRIMARY KEY AUTOINCREMENT,
	identifier   TEXT NOT NULL UNIQUE,
	type         TEXT NOT NULL,
	name         TEXT NOT NULL DEFAULT '',
	description  TEXT NOT NULL DEFAULT '',
	payload      TEXT NOT NULL DEFAULT '{}',
	server_token TEXT NOT NULL,
	created_at   INTEGER NOT NULL,
	updated_at   INTEGER NOT NULL
);

CREATE TABLE compliance_policies (
	id          INTEGER PRIMARY KEY AUTOINCREMENT,
	name        TEXT NOT NULL,
	description TEXT NOT NULL DEFAULT '',
	enabled     INTEGER NOT NULL DEFAULT 1,
	rules       TEXT NOT NULL DEFAULT '{}',
	actions     TEXT NOT NULL DEFAULT '{}',
	grace_hours INTEGER NOT NULL DEFAULT 0,
	created_at  INTEGER NOT NULL,
	updated_at  INTEGER NOT NULL
);

CREATE TABLE assignments (
	id         INTEGER PRIMARY KEY AUTOINCREMENT,
	item_type  TEXT NOT NULL,
	item_id    INTEGER NOT NULL,
	group_id   INTEGER NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
	intent     TEXT NOT NULL DEFAULT 'install',
	created_at INTEGER NOT NULL,
	UNIQUE (item_type, item_id, group_id)
);
CREATE INDEX assignments_group ON assignments(group_id);

CREATE TABLE device_profile_state (
	device_id    TEXT NOT NULL REFERENCES devices(udid) ON DELETE CASCADE,
	profile_id   INTEGER NOT NULL,
	identifier   TEXT NOT NULL DEFAULT '',
	version      INTEGER NOT NULL DEFAULT 0,
	status       TEXT NOT NULL,
	command_uuid TEXT NOT NULL DEFAULT '',
	error        TEXT NOT NULL DEFAULT '',
	updated_at   INTEGER NOT NULL,
	PRIMARY KEY (device_id, profile_id)
);

CREATE TABLE device_app_state (
	device_id    TEXT NOT NULL REFERENCES devices(udid) ON DELETE CASCADE,
	app_id       INTEGER NOT NULL,
	bundle_id    TEXT NOT NULL DEFAULT '',
	intent       TEXT NOT NULL DEFAULT 'install',
	status       TEXT NOT NULL,
	command_uuid TEXT NOT NULL DEFAULT '',
	error        TEXT NOT NULL DEFAULT '',
	attempts     INTEGER NOT NULL DEFAULT 0,
	updated_at   INTEGER NOT NULL,
	PRIMARY KEY (device_id, app_id)
);

CREATE TABLE device_declaration_status (
	device_id    TEXT NOT NULL REFERENCES devices(udid) ON DELETE CASCADE,
	identifier   TEXT NOT NULL,
	active       INTEGER NOT NULL DEFAULT 0,
	valid        TEXT NOT NULL DEFAULT '',
	server_token TEXT NOT NULL DEFAULT '',
	reasons      TEXT NOT NULL DEFAULT '',
	updated_at   INTEGER NOT NULL,
	PRIMARY KEY (device_id, identifier)
);

CREATE TABLE enrollment_tokens (
	id          INTEGER PRIMARY KEY AUTOINCREMENT,
	name        TEXT NOT NULL,
	token       TEXT NOT NULL UNIQUE,
	ownership   TEXT NOT NULL DEFAULT 'corporate',
	group_ids   TEXT NOT NULL DEFAULT '',
	assigned_user TEXT NOT NULL DEFAULT '',
	max_uses    INTEGER NOT NULL DEFAULT 0,
	uses        INTEGER NOT NULL DEFAULT 0,
	expires_at  INTEGER NOT NULL DEFAULT 0,
	enabled     INTEGER NOT NULL DEFAULT 1,
	created_at  INTEGER NOT NULL
);

CREATE TABLE scep_challenges (
	challenge   TEXT PRIMARY KEY,
	purpose     TEXT NOT NULL DEFAULT 'enroll',
	ref         TEXT NOT NULL DEFAULT '',
	created_at  INTEGER NOT NULL,
	expires_at  INTEGER NOT NULL,
	used_at     INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE issued_certs (
	serial      TEXT PRIMARY KEY,
	sha256      TEXT NOT NULL,
	subject     TEXT NOT NULL DEFAULT '',
	not_before  INTEGER NOT NULL,
	not_after   INTEGER NOT NULL,
	purpose     TEXT NOT NULL DEFAULT '',
	ref         TEXT NOT NULL DEFAULT '',
	device_id   TEXT NOT NULL DEFAULT '',
	revoked     INTEGER NOT NULL DEFAULT 0,
	created_at  INTEGER NOT NULL
);
CREATE INDEX issued_certs_sha ON issued_certs(sha256);

CREATE TABLE dep_servers (
	id                  INTEGER PRIMARY KEY AUTOINCREMENT,
	name                TEXT NOT NULL,
	cert_pem            TEXT NOT NULL,
	key_pem             TEXT NOT NULL,
	consumer_key        TEXT NOT NULL DEFAULT '',
	consumer_secret     TEXT NOT NULL DEFAULT '',
	access_token        TEXT NOT NULL DEFAULT '',
	access_secret       TEXT NOT NULL DEFAULT '',
	access_token_expiry INTEGER NOT NULL DEFAULT 0,
	server_name         TEXT NOT NULL DEFAULT '',
	server_uuid         TEXT NOT NULL DEFAULT '',
	org_name            TEXT NOT NULL DEFAULT '',
	org_email           TEXT NOT NULL DEFAULT '',
	org_phone           TEXT NOT NULL DEFAULT '',
	org_address         TEXT NOT NULL DEFAULT '',
	org_id              TEXT NOT NULL DEFAULT '',
	admin_id            TEXT NOT NULL DEFAULT '',
	cursor              TEXT NOT NULL DEFAULT '',
	last_sync           INTEGER NOT NULL DEFAULT 0,
	last_error          TEXT NOT NULL DEFAULT '',
	default_profile_id  INTEGER NOT NULL DEFAULT 0,
	created_at          INTEGER NOT NULL,
	updated_at          INTEGER NOT NULL
);

CREATE TABLE dep_profiles (
	id            INTEGER PRIMARY KEY AUTOINCREMENT,
	name          TEXT NOT NULL,
	config        TEXT NOT NULL DEFAULT '{}',
	group_ids     TEXT NOT NULL DEFAULT '',
	created_at    INTEGER NOT NULL,
	updated_at    INTEGER NOT NULL
);

CREATE TABLE dep_profile_uploads (
	dep_profile_id INTEGER NOT NULL REFERENCES dep_profiles(id) ON DELETE CASCADE,
	dep_server_id  INTEGER NOT NULL REFERENCES dep_servers(id) ON DELETE CASCADE,
	profile_uuid   TEXT NOT NULL,
	config_hash    TEXT NOT NULL,
	uploaded_at    INTEGER NOT NULL,
	PRIMARY KEY (dep_profile_id, dep_server_id)
);

CREATE TABLE dep_devices (
	serial_number        TEXT PRIMARY KEY,
	dep_server_id        INTEGER NOT NULL REFERENCES dep_servers(id) ON DELETE CASCADE,
	model                TEXT NOT NULL DEFAULT '',
	description          TEXT NOT NULL DEFAULT '',
	color                TEXT NOT NULL DEFAULT '',
	os                   TEXT NOT NULL DEFAULT '',
	device_family        TEXT NOT NULL DEFAULT '',
	asset_tag            TEXT NOT NULL DEFAULT '',
	profile_status       TEXT NOT NULL DEFAULT '',
	profile_uuid         TEXT NOT NULL DEFAULT '',
	profile_assign_time  TEXT NOT NULL DEFAULT '',
	profile_push_time    TEXT NOT NULL DEFAULT '',
	device_assigned_date TEXT NOT NULL DEFAULT '',
	device_assigned_by   TEXT NOT NULL DEFAULT '',
	op_type              TEXT NOT NULL DEFAULT '',
	op_date              TEXT NOT NULL DEFAULT '',
	assigned_profile_id  INTEGER NOT NULL DEFAULT 0,
	updated_at           INTEGER NOT NULL
);

CREATE TABLE vpp_tokens (
	id            INTEGER PRIMARY KEY AUTOINCREMENT,
	name          TEXT NOT NULL,
	token         TEXT NOT NULL,
	org_name      TEXT NOT NULL DEFAULT '',
	location_name TEXT NOT NULL DEFAULT '',
	exp_date      INTEGER NOT NULL DEFAULT 0,
	last_sync     INTEGER NOT NULL DEFAULT 0,
	last_error    TEXT NOT NULL DEFAULT '',
	created_at    INTEGER NOT NULL
);

CREATE TABLE vpp_assets (
	vpp_token_id      INTEGER NOT NULL REFERENCES vpp_tokens(id) ON DELETE CASCADE,
	adam_id           TEXT NOT NULL,
	pricing_param     TEXT NOT NULL DEFAULT 'STDQ',
	product_type      TEXT NOT NULL DEFAULT '',
	name              TEXT NOT NULL DEFAULT '',
	icon_url          TEXT NOT NULL DEFAULT '',
	available_count   INTEGER NOT NULL DEFAULT 0,
	assigned_count    INTEGER NOT NULL DEFAULT 0,
	total_count       INTEGER NOT NULL DEFAULT 0,
	retired_count     INTEGER NOT NULL DEFAULT 0,
	device_assignable INTEGER NOT NULL DEFAULT 1,
	revocable         INTEGER NOT NULL DEFAULT 1,
	updated_at        INTEGER NOT NULL,
	PRIMARY KEY (vpp_token_id, adam_id, pricing_param)
);

CREATE TABLE webhooks (
	id            INTEGER PRIMARY KEY AUTOINCREMENT,
	name          TEXT NOT NULL,
	url           TEXT NOT NULL,
	secret        TEXT NOT NULL DEFAULT '',
	events        TEXT NOT NULL DEFAULT '',
	enabled       INTEGER NOT NULL DEFAULT 1,
	last_status   INTEGER NOT NULL DEFAULT 0,
	last_error    TEXT NOT NULL DEFAULT '',
	last_delivery INTEGER NOT NULL DEFAULT 0,
	created_at    INTEGER NOT NULL
);

CREATE TABLE audit_log (
	id      INTEGER PRIMARY KEY AUTOINCREMENT,
	ts      INTEGER NOT NULL,
	actor   TEXT NOT NULL,
	action  TEXT NOT NULL,
	target  TEXT NOT NULL DEFAULT '',
	details TEXT NOT NULL DEFAULT '',
	ip      TEXT NOT NULL DEFAULT ''
);
CREATE INDEX audit_ts ON audit_log(ts);

CREATE TABLE events (
	id        INTEGER PRIMARY KEY AUTOINCREMENT,
	ts        INTEGER NOT NULL,
	device_id TEXT NOT NULL DEFAULT '',
	type      TEXT NOT NULL,
	level     TEXT NOT NULL DEFAULT 'info',
	message   TEXT NOT NULL DEFAULT '',
	details   TEXT NOT NULL DEFAULT ''
);
CREATE INDEX events_device ON events(device_id, ts);
CREATE INDEX events_ts ON events(ts);

INSERT INTO groups(name, description, kind, rules, created_at, updated_at)
VALUES ('All Devices', 'Every enrolled device. Built in; cannot be deleted.', 'all', '', strftime('%s','now'), strftime('%s','now'))
`,
	// 2: account-driven enrollment (BYOD User Enrollment and account-driven device enrollment)
	`
ALTER TABLE devices ADD COLUMN managed_apple_id TEXT NOT NULL DEFAULT '';
ALTER TABLE devices ADD COLUMN user_enrollment INTEGER NOT NULL DEFAULT 0;
ALTER TABLE devices ADD COLUMN account_enrollment_id INTEGER NOT NULL DEFAULT 0;
CREATE INDEX devices_managed_apple_id ON devices(managed_apple_id);

CREATE TABLE account_enrollments (
	id                  INTEGER PRIMARY KEY AUTOINCREMENT,
	token_hash          TEXT NOT NULL UNIQUE,
	mode                TEXT NOT NULL,
	user_identifier     TEXT NOT NULL,
	managed_apple_id    TEXT NOT NULL,
	display_name        TEXT NOT NULL DEFAULT '',
	auth_method         TEXT NOT NULL DEFAULT '',
	enrollment_token_id INTEGER NOT NULL DEFAULT 0,
	group_ids           TEXT NOT NULL DEFAULT '',
	product             TEXT NOT NULL DEFAULT '',
	os_build            TEXT NOT NULL DEFAULT '',
	device_id           TEXT NOT NULL DEFAULT '',
	ip                  TEXT NOT NULL DEFAULT '',
	created_at          INTEGER NOT NULL,
	expires_at          INTEGER NOT NULL,
	profile_issued_at   INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX account_enrollments_created ON account_enrollments(created_at)
`,
}
