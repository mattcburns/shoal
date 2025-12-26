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

package imageproxy

import (
	"testing"
)

func TestNewConfig(t *testing.T) {
	tests := []struct {
		name            string
		port            string
		allowedDomains  string
		allowedSubnets  string
		rateLimit       int
		wantErr         bool
		wantDomains     []string
		wantSubnetCount int
	}{
		{
			name:            "wildcard domains",
			port:            "8082",
			allowedDomains:  "*",
			allowedSubnets:  "",
			rateLimit:       10,
			wantErr:         false,
			wantDomains:     []string{"*"},
			wantSubnetCount: 0,
		},
		{
			name:            "specific domains",
			port:            "8082",
			allowedDomains:  "example.com,files.example.org",
			allowedSubnets:  "",
			rateLimit:       10,
			wantErr:         false,
			wantDomains:     []string{"example.com", "files.example.org"},
			wantSubnetCount: 0,
		},
		{
			name:            "with subnets",
			port:            "8082",
			allowedDomains:  "*",
			allowedSubnets:  "192.168.1.0/24,10.0.0.0/8",
			rateLimit:       10,
			wantErr:         false,
			wantDomains:     []string{"*"},
			wantSubnetCount: 2,
		},
		{
			name:            "invalid subnet",
			port:            "8082",
			allowedDomains:  "*",
			allowedSubnets:  "invalid-subnet",
			rateLimit:       10,
			wantErr:         true,
			wantDomains:     nil,
			wantSubnetCount: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := NewConfig(tt.port, tt.allowedDomains, tt.allowedSubnets, tt.rateLimit)
			if (err != nil) != tt.wantErr {
				t.Errorf("NewConfig() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if tt.wantErr {
				return
			}

			if cfg.Port != tt.port {
				t.Errorf("Port = %v, want %v", cfg.Port, tt.port)
			}
			if cfg.RateLimit != tt.rateLimit {
				t.Errorf("RateLimit = %v, want %v", cfg.RateLimit, tt.rateLimit)
			}
			if len(cfg.AllowedDomains) != len(tt.wantDomains) {
				t.Errorf("AllowedDomains count = %v, want %v", len(cfg.AllowedDomains), len(tt.wantDomains))
			}
			if len(cfg.AllowedSubnets) != tt.wantSubnetCount {
				t.Errorf("AllowedSubnets count = %v, want %v", len(cfg.AllowedSubnets), tt.wantSubnetCount)
			}
		})
	}
}
