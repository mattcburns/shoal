// Shoal is a Redfish aggregator service.
// Copyright (C) 2025  Matthew Burns
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
// GNU Affero General Public License for more details.
//
// You should have received a copy of the GNU Affero General Public License
// along with this program.  If not, see <https://www.gnu.org/licenses/>.

package redfish

// ODataIDRef represents a reference to another resource
type ODataIDRef struct {
	ODataID string `json:"@odata.id"`
}

// ServiceRoot represents the Redfish service root
type ServiceRoot struct {
	ODataContext       string           `json:"@odata.context"`
	ODataID            string           `json:"@odata.id"`
	ODataType          string           `json:"@odata.type"`
	ID                 string           `json:"Id"`
	Name               string           `json:"Name"`
	RedfishVersion     string           `json:"RedfishVersion"`
	UUID               string           `json:"UUID"`
	Systems            ODataIDRef       `json:"Systems"`
	Managers           ODataIDRef       `json:"Managers"`
	SessionService     ODataIDRef       `json:"SessionService"`
	AggregationService *ODataIDRef      `json:"AggregationService,omitempty"`
	Registries         *ODataIDRef      `json:"Registries,omitempty"`
	JsonSchemas        *ODataIDRef      `json:"JsonSchemas,omitempty"`
	AccountService     *ODataIDRef      `json:"AccountService,omitempty"`
	EventService       *ODataIDRef      `json:"EventService,omitempty"`
	TaskService        *ODataIDRef      `json:"TaskService,omitempty"`
	Links              ServiceRootLinks `json:"Links"`
}

// ServiceRootLinks contains links within the service root
type ServiceRootLinks struct {
	Sessions ODataIDRef `json:"Sessions"`
}

// Collection represents a generic Redfish collection
type Collection struct {
	ODataContext string       `json:"@odata.context"`
	ODataID      string       `json:"@odata.id"`
	ODataType    string       `json:"@odata.type"`
	Name         string       `json:"Name"`
	Members      []ODataIDRef `json:"Members"`
	MembersCount int          `json:"Members@odata.count"`
}

// Session represents a Redfish session
type Session struct {
	ODataContext string `json:"@odata.context"`
	ODataID      string `json:"@odata.id"`
	ODataType    string `json:"@odata.type"`
	ID           string `json:"Id"`
	Name         string `json:"Name"`
	UserName     string `json:"UserName"`
}

// SessionService represents the Redfish SessionService
type SessionService struct {
	ODataContext   string     `json:"@odata.context"`
	ODataID        string     `json:"@odata.id"`
	ODataType      string     `json:"@odata.type"`
	ID             string     `json:"Id"`
	Name           string     `json:"Name"`
	Description    string     `json:"Description"`
	ServiceEnabled bool       `json:"ServiceEnabled"`
	SessionTimeout int        `json:"SessionTimeout"`
	Sessions       ODataIDRef `json:"Sessions"`
}

// AccountService represents the Redfish AccountService
type AccountService struct {
	ODataContext   string     `json:"@odata.context"`
	ODataID        string     `json:"@odata.id"`
	ODataType      string     `json:"@odata.type"`
	ID             string     `json:"Id"`
	Name           string     `json:"Name"`
	ServiceEnabled bool       `json:"ServiceEnabled"`
	Accounts       ODataIDRef `json:"Accounts"`
	Roles          ODataIDRef `json:"Roles"`
}

// ManagerAccount represents a user account
type ManagerAccount struct {
	ODataContext string `json:"@odata.context"`
	ODataID      string `json:"@odata.id"`
	ODataType    string `json:"@odata.type"`
	ID           string `json:"Id"`
	Name         string `json:"Name"`
	UserName     string `json:"UserName"`
	RoleID       string `json:"RoleId"`
	Enabled      bool   `json:"Enabled"`
}

// Role represents a Redfish Role resource
type Role struct {
	ODataContext       string   `json:"@odata.context"`
	ODataID            string   `json:"@odata.id"`
	ODataType          string   `json:"@odata.type"`
	ID                 string   `json:"Id"`
	Name               string   `json:"Name"`
	IsPredefined       bool     `json:"IsPredefined"`
	AssignedPrivileges []string `json:"AssignedPrivileges"`
}

// ErrorResponse represents a Redfish error response
type ErrorResponse struct {
	Error ErrorDetail `json:"error"`
}

// ErrorDetail contains error details
type ErrorDetail struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// AggregationService represents the Redfish AggregationService
type AggregationService struct {
	ODataContext      string     `json:"@odata.context"`
	ODataID           string     `json:"@odata.id"`
	ODataType         string     `json:"@odata.type"`
	ID                string     `json:"Id"`
	Name              string     `json:"Name"`
	Description       string     `json:"Description"`
	ConnectionMethods ODataIDRef `json:"ConnectionMethods"`
}

// ConnectionMethod represents a Redfish ConnectionMethod
type ConnectionMethod struct {
	ODataContext            string                  `json:"@odata.context"`
	ODataID                 string                  `json:"@odata.id"`
	ODataType               string                  `json:"@odata.type"`
	ID                      string                  `json:"Id"`
	Name                    string                  `json:"Name"`
	ConnectionMethodType    string                  `json:"ConnectionMethodType"`
	ConnectionMethodVariant ConnectionMethodVariant `json:"ConnectionMethodVariant"`
}

// ConnectionMethodVariant represents the variant details of a connection method
type ConnectionMethodVariant struct {
	ODataType      string                    `json:"@odata.type"`
	Address        string                    `json:"Address"`
	Authentication *ConnectionAuthentication `json:"Authentication,omitempty"`
}

// ConnectionAuthentication represents authentication for a connection
type ConnectionAuthentication struct {
	Username string `json:"Username"`
	Password string `json:"Password"`
}

// EventService represents Redfish EventService (stub)
type EventService struct {
	ODataContext   string `json:"@odata.context"`
	ODataID        string `json:"@odata.id"`
	ODataType      string `json:"@odata.type"`
	ID             string `json:"Id"`
	Name           string `json:"Name"`
	ServiceEnabled bool   `json:"ServiceEnabled"`
}

// TaskService represents Redfish TaskService (stub)
type TaskService struct {
	ODataContext string     `json:"@odata.context"`
	ODataID      string     `json:"@odata.id"`
	ODataType    string     `json:"@odata.type"`
	ID           string     `json:"Id"`
	Name         string     `json:"Name"`
	Tasks        ODataIDRef `json:"Tasks"`
}

// Task represents a Redfish Task (stub)
type Task struct {
	ODataContext string `json:"@odata.context"`
	ODataID      string `json:"@odata.id"`
	ODataType    string `json:"@odata.type"`
	ID           string `json:"Id"`
	Name         string `json:"Name"`
	TaskState    string `json:"TaskState"`
}
