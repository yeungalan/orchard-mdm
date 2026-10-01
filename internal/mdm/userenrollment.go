package mdm

import "errors"

// ErrNotOnUserEnrollment is returned for commands that iOS doesn't accept
// from personal devices enrolled with User Enrollment.
var ErrNotOnUserEnrollment = errors.New("this command isn't available on personal devices enrolled with User Enrollment")

// userEnrollmentCommands are the request types iOS accepts on a User
// Enrollment (from Apple's device-management schema, supportedOS.iOS.userenrollment).
// Everything that reaches beyond the managed work data (erase, Lost Mode,
// location, restart, OS updates, passcode, Activation Lock…) is excluded by iOS.
var userEnrollmentCommands = map[string]bool{
	"DeviceInformation": true, "SecurityInfo": true, "InstalledApplicationList": true, "ManagedApplicationList": true,
	"ProfileList": true, "CertificateList": true, "ProvisioningProfileList": true, "ManagedMediaList": true,
	"InstallProfile": true, "RemoveProfile": true, "InstallProvisioningProfile": true, "RemoveProvisioningProfile": true,
	"InstallApplication": true, "RemoveApplication": true, "ValidateApplications": true,
	"ManagedApplicationConfiguration": true, "ManagedApplicationAttributes": true, "ManagedApplicationFeedback": true,
	"InstallMedia": true, "RemoveMedia": true, "DeclarativeManagement": true, "DeviceLock": true, "Settings": true,
}

// AllowedOnUserEnrollment reports whether iOS accepts the request type from a
// User Enrollment.
func AllowedOnUserEnrollment(requestType string) bool { return userEnrollmentCommands[requestType] }

// UserEnrollmentQueries is the DeviceInformation query set for personal
// devices: hardware, OS, battery and storage only (no identifiers, phone or
// network details).
var UserEnrollmentQueries = []string{
	"DeviceName", "OSVersion", "BuildVersion", "SupplementalBuildVersion", "SupplementalOSVersionExtra", "ModelName", "Model", "ProductName",
	"DeviceCapacity", "AvailableDeviceCapacity", "BatteryLevel", "IsSupervised", "OrganizationInfo", "MDMOptions", "iTunesStoreAccountIsActive",
}

// UserEnrollmentTelemetryQueries is the lightweight sample for personal devices.
var UserEnrollmentTelemetryQueries = []string{"BatteryLevel", "AvailableDeviceCapacity", "DeviceCapacity", "OSVersion", "BuildVersion", "DeviceName"}
