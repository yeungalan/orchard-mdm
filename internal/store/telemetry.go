package store

import "fmt"

// TelemetrySample is one point-in-time reading of a device's state (battery,
// storage, network). Samples come from MDM inventory ("mdm"), DDM status
// reports ("ddm"), the server's view of the connection ("server") or the
// optional companion agent app ("agent").
type TelemetrySample struct {
	ID                 int64          `json:"id"`
	DeviceID           string         `json:"device_id"`
	TS                 int64          `json:"ts"`
	Source             string         `json:"source"`
	Battery            float64        `json:"battery"` // 0..1, -1 unknown
	BatteryState       string         `json:"battery_state"`
	AvailableGB        float64        `json:"available_gb"` // -1 unknown
	SSID               string         `json:"ssid"`
	BSSID              string         `json:"bssid"`
	IP                 string         `json:"ip"`
	LocalIP            string         `json:"local_ip"`
	Carrier            string         `json:"carrier"`
	CellularTechnology string         `json:"cellular_technology"`
	Roaming            bool           `json:"roaming"`
	Data               map[string]any `json:"data,omitempty"`
}

// InsertTelemetry stores a telemetry sample.
func (s *Store) InsertTelemetry(t *TelemetrySample) error {
	if t.TS == 0 {
		t.TS = Now()
	}
	data := ""
	if len(t.Data) > 0 {
		data = mustJSON(t.Data)
	}
	res, err := s.db.Exec(`INSERT INTO device_telemetry(device_id, ts, source, battery, battery_state, available_gb, ssid, bssid, ip, local_ip, carrier, cellular_technology, roaming, data)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, t.DeviceID, t.TS, t.Source, t.Battery, t.BatteryState, t.AvailableGB, t.SSID, t.BSSID, t.IP, t.LocalIP, t.Carrier,
		t.CellularTechnology, boolInt(t.Roaming), data)
	if err != nil {
		return err
	}
	t.ID, _ = res.LastInsertId()
	return nil
}

// ListTelemetry returns samples for a device newer than since (unix seconds), oldest first.
func (s *Store) ListTelemetry(udid string, since int64, limit int) ([]*TelemetrySample, error) {
	if limit <= 0 || limit > 5000 {
		limit = 1000
	}
	rows, err := s.db.Query(`SELECT * FROM (SELECT id, device_id, ts, source, battery, battery_state, available_gb, ssid, bssid, ip, local_ip, carrier, cellular_technology, roaming, data
		FROM device_telemetry WHERE device_id=? AND ts>=? ORDER BY ts DESC, id DESC LIMIT `+fmt.Sprint(limit)+`) ORDER BY ts, id`, udid, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*TelemetrySample{}
	for rows.Next() {
		t := &TelemetrySample{}
		var roaming int
		var data string
		if err := rows.Scan(&t.ID, &t.DeviceID, &t.TS, &t.Source, &t.Battery, &t.BatteryState, &t.AvailableGB, &t.SSID, &t.BSSID, &t.IP, &t.LocalIP, &t.Carrier,
			&t.CellularTechnology, &roaming, &data); err != nil {
			return nil, err
		}
		t.Roaming = roaming == 1
		t.Data = unmarshalJSON[map[string]any](data)
		out = append(out, t)
	}
	return out, rows.Err()
}

// LastServerIPSample returns the IP of the newest "server" sample for a device.
func (s *Store) LastServerIPSample(udid string) (string, int64) {
	var ip string
	var ts int64
	_ = s.db.QueryRow(`SELECT ip, ts FROM device_telemetry WHERE device_id=? AND source='server' ORDER BY ts DESC, id DESC LIMIT 1`, udid).Scan(&ip, &ts)
	return ip, ts
}

// Location is a recorded device position.
type Location struct {
	ID        int64   `json:"id"`
	DeviceID  string  `json:"device_id"`
	TS        int64   `json:"ts"`
	Source    string  `json:"source"` // mdm (Lost Mode DeviceLocation) | agent
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
	Accuracy  float64 `json:"accuracy"`
	Altitude  float64 `json:"altitude"`
	Speed     float64 `json:"speed"`
	Course    float64 `json:"course"`
}

// InsertLocation stores a location fix.
func (s *Store) InsertLocation(l *Location) error {
	if l.TS == 0 {
		l.TS = Now()
	}
	res, err := s.db.Exec(`INSERT INTO device_locations(device_id, ts, source, latitude, longitude, accuracy, altitude, speed, course) VALUES(?,?,?,?,?,?,?,?,?)`,
		l.DeviceID, l.TS, l.Source, l.Latitude, l.Longitude, l.Accuracy, l.Altitude, l.Speed, l.Course)
	if err != nil {
		return err
	}
	l.ID, _ = res.LastInsertId()
	return nil
}

// ListLocations returns a device's location history newer than since, newest first.
func (s *Store) ListLocations(udid string, since int64, limit int) ([]*Location, error) {
	if limit <= 0 || limit > 5000 {
		limit = 500
	}
	rows, err := s.db.Query(`SELECT id, device_id, ts, source, latitude, longitude, accuracy, altitude, speed, course FROM device_locations
		WHERE device_id=? AND ts>=? ORDER BY ts DESC, id DESC LIMIT `+fmt.Sprint(limit), udid, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Location{}
	for rows.Next() {
		l := &Location{}
		if err := rows.Scan(&l.ID, &l.DeviceID, &l.TS, &l.Source, &l.Latitude, &l.Longitude, &l.Accuracy, &l.Altitude, &l.Speed, &l.Course); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// PruneTelemetry removes samples and locations older than maxAge seconds.
func (s *Store) PruneTelemetry(maxAge int64) {
	cut := Now() - maxAge
	_, _ = s.db.Exec(`DELETE FROM device_telemetry WHERE ts<?`, cut)
	_, _ = s.db.Exec(`DELETE FROM device_locations WHERE ts<?`, cut)
}
