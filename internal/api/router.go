/*
Shoal is a Redfish aggregator service.
Copyright (C) 2025  Matthew Burns

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program.  If not, see <https://www.gnu.org/licenses/>.
*/

package api

import (
	"context"
	"net/http"

	"shoal/internal/auth"
	"shoal/internal/bmc"
	"shoal/internal/database"
)

// NewRouter constructs an API router using the existing Handler methods.
// This does not change any routes or behavior; it simply centralizes mux setup
// so other code can delegate to it.
func NewRouter(db *database.DB) http.Handler {
	return NewRouterWithImageProxy(db, nil)
}

// NewRouterWithImageProxy constructs an API router with image proxy support
func NewRouterWithImageProxy(db *database.DB, proxyConfig *ImageProxyConfig) http.Handler {
	imageProxyURL := ""
	var cloudInitGen func(string, string) (string, string, error)
	var ociConv func(context.Context, string) (string, string, error)
	if proxyConfig != nil && proxyConfig.Enabled {
		imageProxyURL = proxyConfig.BaseURL
		cloudInitGen = proxyConfig.CloudInitGeneratorFunc
		ociConv = proxyConfig.OCIConverterFunc
	}

	h := &Handler{
		db:                     db,
		auth:                   auth.New(db),
		bmcSvc:                 bmc.New(db),
		imageProxyURL:          imageProxyURL,
		cloudInitGeneratorFunc: cloudInitGen,
		ociConverterFunc:       ociConv,
	}
	return newMux(h)
}

// newMux wires the HTTP routes to the existing handler methods on Handler.
// It mirrors the registrations performed in api.go:New to preserve behavior.
func newMux(h *Handler) *http.ServeMux {
	mux := http.NewServeMux()

	// Redfish service root and versioning
	mux.HandleFunc("/redfish/", h.handleRedfish)

	// $metadata and registries/schema store endpoints
	mux.HandleFunc("/redfish/v1/$metadata", h.handleMetadata)
	mux.HandleFunc("/redfish/v1/Registries", h.auth.RequireAuth(http.HandlerFunc(h.handleRegistriesCollection)).ServeHTTP)
	mux.HandleFunc("/redfish/v1/Registries/", h.auth.RequireAuth(http.HandlerFunc(h.handleRegistryFile)).ServeHTTP)
	mux.HandleFunc("/redfish/v1/SchemaStore", h.auth.RequireAuth(http.HandlerFunc(h.handleSchemaStoreRoot)).ServeHTTP)
	mux.HandleFunc("/redfish/v1/SchemaStore/", h.auth.RequireAuth(http.HandlerFunc(h.handleSchemaFile)).ServeHTTP)

	// Aggregator-specific BMC management endpoints
	mux.HandleFunc("/redfish/v1/AggregationService/ManagedNodes/", h.auth.RequireAuth(http.HandlerFunc(h.handleManagedNodes)).ServeHTTP)

	// WebSocket console endpoint
	mux.HandleFunc("/ws/console/", h.auth.RequireAuth(http.HandlerFunc(h.handleWebSocketConsole)).ServeHTTP)

	return mux
}
