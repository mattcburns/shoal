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
	"net"
	"strings"
)

// Config holds configuration for the image proxy server
type Config struct {
	// Port is the port to listen on
	Port string
	
	// AllowedDomains is a list of allowed domain patterns (use "*" for all)
	AllowedDomains []string
	
	// AllowedSubnets is a list of allowed IP subnets in CIDR notation
	AllowedSubnets []*net.IPNet
	
	// RateLimit is the maximum concurrent downloads per IP
	RateLimit int
	
	// DisableSSRFProtection disables SSRF protection (for testing only)
	DisableSSRFProtection bool
}

// NewConfig creates a new Config from command-line parameters
func NewConfig(port, allowedDomains, allowedSubnets string, rateLimit int) (*Config, error) {
	cfg := &Config{
		Port:      port,
		RateLimit: rateLimit,
	}
	
	// Parse allowed domains
	if allowedDomains == "" || allowedDomains == "*" {
		cfg.AllowedDomains = []string{"*"}
	} else {
		cfg.AllowedDomains = strings.Split(allowedDomains, ",")
		for i := range cfg.AllowedDomains {
			cfg.AllowedDomains[i] = strings.TrimSpace(cfg.AllowedDomains[i])
		}
	}
	
	// Parse allowed subnets
	if allowedSubnets != "" {
		subnets := strings.Split(allowedSubnets, ",")
		cfg.AllowedSubnets = make([]*net.IPNet, 0, len(subnets))
		for _, subnet := range subnets {
			subnet = strings.TrimSpace(subnet)
			if subnet == "" {
				continue
			}
			_, ipNet, err := net.ParseCIDR(subnet)
			if err != nil {
				return nil, err
			}
			cfg.AllowedSubnets = append(cfg.AllowedSubnets, ipNet)
		}
	}
	
	return cfg, nil
}
