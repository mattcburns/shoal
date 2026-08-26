package redfish

import "encoding/json"

// This file defines hand-rolled decode targets for the DMTF Redfish JSON
// shapes this package consumes, replacing the equivalent gofish/redfish
// generated types. Field names/JSON tags intentionally mirror the gofish
// structs they replace (and the raw Redfish schema) so behavior — same
// requests, same response mapping — stays identical.

// rfLink is a Redfish "@odata.id" reference, e.g. {"@odata.id": "/redfish/v1/Systems"}.
type rfLink struct {
	ODataID string `json:"@odata.id"`
}

// rfCollection is a Redfish resource collection document.
type rfCollection struct {
	ODataID  string   `json:"@odata.id"`
	Name     string   `json:"Name"`
	Count    int      `json:"Members@odata.count"`
	Members  []rfLink `json:"Members"`
	NextLink string   `json:"Members@odata.nextLink"`
}

// rfServiceRoot is the Redfish /redfish/v1 service root document.
type rfServiceRoot struct {
	ODataID        string `json:"@odata.id"`
	ID             string `json:"Id"`
	Name           string `json:"Name"`
	RedfishVersion string `json:"RedfishVersion"`
	UUID           string `json:"UUID"`
	Systems        rfLink `json:"Systems"`
	Managers       rfLink `json:"Managers"`
	Chassis        rfLink `json:"Chassis"`
	UpdateService  rfLink `json:"UpdateService"`
	Links          struct {
		Sessions rfLink `json:"Sessions"`
	} `json:"Links"`
}

// rfStatus mirrors gofish's common.Status.
type rfStatus struct {
	Health       string `json:"Health"`
	HealthRollup string `json:"HealthRollup"`
	State        string `json:"State"`
}

// --- ComputerSystem ---

// rfBootFields decodes the two Boot properties Shoal reads.
type rfBootFields struct {
	BootSourceOverrideEnabled string `json:"BootSourceOverrideEnabled"`
	BootSourceOverrideTarget  string `json:"BootSourceOverrideTarget"`
}

// rfSerialConsoleProtocol mirrors gofish redfish.SerialConsoleProtocol.
type rfSerialConsoleProtocol struct {
	ConsoleEntryCommand string `json:"ConsoleEntryCommand"`
	Port                int    `json:"Port"`
	ServiceEnabled      bool   `json:"ServiceEnabled"`
}

// rfHostSerialConsole mirrors gofish redfish.HostSerialConsole
// (ComputerSystem.SerialConsole).
type rfHostSerialConsole struct {
	IPMI   rfSerialConsoleProtocol `json:"IPMI"`
	SSH    rfSerialConsoleProtocol `json:"SSH"`
	Telnet rfSerialConsoleProtocol `json:"Telnet"`
}

// rfComputerSystem mirrors the subset of gofish redfish.ComputerSystem this
// package reads/writes.
//
// Boot is kept as raw JSON (rather than decoded into a struct with only the
// two fields Shoal reads) so setBoot can PATCH back every property the BMC
// originally sent, mutating only BootSourceOverrideEnabled/Target -- gofish's
// SetBoot does the same full-struct round-trip (read live Boot, mutate two
// fields, PATCH the whole thing back), which matters for BMCs whose Boot
// PATCH is a full replace rather than a JSON merge-patch.
type rfComputerSystem struct {
	ODataID       string              `json:"@odata.id"`
	ID            string              `json:"Id"`
	Name          string              `json:"Name"`
	UUID          string              `json:"UUID"`
	SerialNumber  string              `json:"SerialNumber"`
	Manufacturer  string              `json:"Manufacturer"`
	Model         string              `json:"Model"`
	PowerState    string              `json:"PowerState"`
	Boot          json.RawMessage     `json:"Boot"`
	SerialConsole rfHostSerialConsole `json:"SerialConsole"`
	LogServices   rfLink              `json:"LogServices"`
	VirtualMedia  rfLink              `json:"VirtualMedia"`
	Actions       struct {
		Reset struct {
			Target            string   `json:"target"`
			AllowedResetTypes []string `json:"ResetType@Redfish.AllowableValues"`
		} `json:"#ComputerSystem.Reset"`
	} `json:"Actions"`
}

// bootFields decodes the two Boot properties Shoal reads from the raw Boot
// JSON captured on the system document.
func (s *rfComputerSystem) bootFields() rfBootFields {
	var f rfBootFields
	if len(s.Boot) > 0 {
		_ = json.Unmarshal(s.Boot, &f)
	}
	return f
}

// --- Manager ---

// rfManagerSerialConsole mirrors gofish redfish.SerialConsole (Manager.SerialConsole).
type rfManagerSerialConsole struct {
	ConnectTypesSupported []string `json:"ConnectTypesSupported"`
	ServiceEnabled        bool     `json:"ServiceEnabled"`
}

// rfSerialConnectTypeSSH mirrors gofish's SSHSerialConnectTypesSupported enum value.
const rfSerialConnectTypeSSH = "SSH"

// rfManager mirrors the subset of gofish redfish.Manager this package reads.
type rfManager struct {
	ODataID         string                 `json:"@odata.id"`
	ID              string                 `json:"Id"`
	Name            string                 `json:"Name"`
	Model           string                 `json:"Model"`
	UUID            string                 `json:"UUID"`
	SerialConsole   rfManagerSerialConsole `json:"SerialConsole"`
	LogServices     rfLink                 `json:"LogServices"`
	VirtualMedia    rfLink                 `json:"VirtualMedia"`
	NetworkProtocol rfLink                 `json:"NetworkProtocol"`
}

// rfProtocolSetting mirrors gofish redfish.NetworkProtocol (one protocol's
// port/enabled pair inside ManagerNetworkProtocol, e.g. SSH/HTTP/HTTPS).
type rfProtocolSetting struct {
	Port            int  `json:"Port"`
	ProtocolEnabled bool `json:"ProtocolEnabled"`
}

// rfNetworkProtocolSettings mirrors gofish redfish.NetworkProtocolSettings
// (the Manager's .../NetworkProtocol resource).
type rfNetworkProtocolSettings struct {
	SSH rfProtocolSetting `json:"SSH"`
}

// --- VirtualMedia ---

// rfVirtualMedia mirrors the subset of gofish redfish.VirtualMedia this
// package reads/acts on.
type rfVirtualMedia struct {
	ODataID    string   `json:"@odata.id"`
	ID         string   `json:"Id"`
	Name       string   `json:"Name"`
	Image      string   `json:"Image"`
	Inserted   bool     `json:"Inserted"`
	MediaTypes []string `json:"MediaTypes"`
	Actions    struct {
		EjectMedia struct {
			Target string `json:"target"`
		} `json:"#VirtualMedia.EjectMedia"`
		InsertMedia struct {
			Target string `json:"target"`
		} `json:"#VirtualMedia.InsertMedia"`
	} `json:"Actions"`
}

// --- LogService / LogEntry ---

// rfSELLogEntryType mirrors gofish redfish.SELLogEntryTypes.
const rfSELLogEntryType = "SEL"

// rfLogService mirrors the subset of gofish redfish.LogService this package reads.
type rfLogService struct {
	ODataID      string `json:"@odata.id"`
	ID           string `json:"Id"`
	Name         string `json:"Name"`
	LogEntryType string `json:"LogEntryType"`
	Entries      rfLink `json:"Entries"`
}

// rfLogEntry mirrors the subset of gofish redfish.LogEntry this package reads.
type rfLogEntry struct {
	ODataID        string `json:"@odata.id"`
	ID             string `json:"Id"`
	Name           string `json:"Name"`
	Message        string `json:"Message"`
	Description    string `json:"Description"`
	MessageID      string `json:"MessageId"`
	Severity       string `json:"Severity"`
	EntryType      string `json:"EntryType"`
	Created        string `json:"Created"`
	EventTimestamp string `json:"EventTimestamp"`
	SensorType     string `json:"SensorType"`
	SensorNumber   int    `json:"SensorNumber"`
}

// --- Chassis / Sensors / Thermal / Power ---

// rfChassis mirrors the subset of gofish redfish.Chassis this package reads.
type rfChassis struct {
	ODataID     string `json:"@odata.id"`
	ID          string `json:"Id"`
	Name        string `json:"Name"`
	LogServices rfLink `json:"LogServices"`
	Sensors     rfLink `json:"Sensors"`
	Thermal     rfLink `json:"Thermal"`
	Power       rfLink `json:"Power"`
}

// rfSensor mirrors the subset of gofish redfish.Sensor this package reads.
type rfSensor struct {
	ODataID         string   `json:"@odata.id"`
	ID              string   `json:"Id"`
	Name            string   `json:"Name"`
	ReadingType     string   `json:"ReadingType"`
	Reading         float64  `json:"Reading"`
	ReadingUnits    string   `json:"ReadingUnits"`
	PhysicalContext string   `json:"PhysicalContext"`
	Status          rfStatus `json:"Status"`
}

// rfThermal mirrors the subset of gofish redfish.Thermal this package reads.
type rfThermal struct {
	ODataID      string          `json:"@odata.id"`
	Temperatures []rfTemperature `json:"Temperatures"`
	Fans         []rfFan         `json:"Fans"`
}

// rfTemperature mirrors the subset of gofish redfish.Temperature this package reads.
type rfTemperature struct {
	ODataID         string  `json:"@odata.id"`
	MemberID        string  `json:"MemberId"`
	ID              string  `json:"Id"`
	Name            string  `json:"Name"`
	ReadingCelsius  float64 `json:"ReadingCelsius"`
	PhysicalContext string  `json:"PhysicalContext"`
}

// rfFan mirrors the subset of gofish redfish.ThermalFan this package reads.
type rfFan struct {
	ODataID         string  `json:"@odata.id"`
	MemberID        string  `json:"MemberId"`
	ID              string  `json:"Id"`
	Name            string  `json:"Name"`
	Reading         float64 `json:"Reading"`
	ReadingUnits    string  `json:"ReadingUnits"`
	PhysicalContext string  `json:"PhysicalContext"`
}

// rfPower mirrors the subset of gofish redfish.Power this package reads.
type rfPower struct {
	ODataID  string      `json:"@odata.id"`
	Voltages []rfVoltage `json:"Voltages"`
}

// rfVoltage mirrors the subset of gofish redfish.Voltage this package reads.
type rfVoltage struct {
	ODataID         string  `json:"@odata.id"`
	MemberID        string  `json:"MemberId"`
	ID              string  `json:"Id"`
	Name            string  `json:"Name"`
	ReadingVolts    float64 `json:"ReadingVolts"`
	PhysicalContext string  `json:"PhysicalContext"`
}

// --- UpdateService / SoftwareInventory ---

// rfUpdateService mirrors the subset of gofish redfish.UpdateService this package reads.
type rfUpdateService struct {
	ODataID           string `json:"@odata.id"`
	FirmwareInventory rfLink `json:"FirmwareInventory"`
	SoftwareInventory rfLink `json:"SoftwareInventory"`
}

// rfSoftwareInventory mirrors the subset of gofish redfish.SoftwareInventory this package reads.
type rfSoftwareInventory struct {
	ODataID      string   `json:"@odata.id"`
	ID           string   `json:"Id"`
	Name         string   `json:"Name"`
	Version      string   `json:"Version"`
	SoftwareID   string   `json:"SoftwareId"`
	Manufacturer string   `json:"Manufacturer"`
	ReleaseDate  string   `json:"ReleaseDate"`
	Status       rfStatus `json:"Status"`
	Updateable   bool     `json:"Updateable"`
}
