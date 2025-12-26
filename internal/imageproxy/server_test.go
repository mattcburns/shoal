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
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestServer_ServeHTTP_MissingURL(t *testing.T) {
	config := &Config{
		Port:           "8082",
		AllowedDomains: []string{"*"},
		RateLimit:      10,
	}
	server := NewServer(config)

	req := httptest.NewRequest(http.MethodGet, "/proxy", nil)
	rec := httptest.NewRecorder()

	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("Expected status 400, got %d", rec.Code)
	}
}

func TestServer_ServeHTTP_InvalidURL(t *testing.T) {
	config := &Config{
		Port:           "8082",
		AllowedDomains: []string{"*"},
		RateLimit:      10,
	}
	server := NewServer(config)

	req := httptest.NewRequest(http.MethodGet, "/proxy?url=not-a-valid-url", nil)
	rec := httptest.NewRecorder()

	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("Expected status 400, got %d", rec.Code)
	}
}

func TestServer_ServeHTTP_UnsupportedScheme(t *testing.T) {
	config := &Config{
		Port:           "8082",
		AllowedDomains: []string{"*"},
		RateLimit:      10,
	}
	server := NewServer(config)

	req := httptest.NewRequest(http.MethodGet, "/proxy?url=ftp://example.com/file.iso", nil)
	rec := httptest.NewRecorder()

	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("Expected status 400, got %d", rec.Code)
	}
}

func TestServer_ServeHTTP_DomainNotAllowed(t *testing.T) {
	config := &Config{
		Port:           "8082",
		AllowedDomains: []string{"example.com"},
		RateLimit:      10,
	}
	server := NewServer(config)

	req := httptest.NewRequest(http.MethodGet, "/proxy?url=http://badsite.com/file.iso", nil)
	rec := httptest.NewRecorder()

	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("Expected status 403, got %d", rec.Code)
	}
}

func TestServer_ServeHTTP_Success(t *testing.T) {
	// Create a test HTTP server to serve the image
	imageServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Length", "11")
		_, _ = w.Write([]byte("test-image"))
	}))
	defer imageServer.Close()

	config := &Config{
		Port:                  "8082",
		AllowedDomains:        []string{"*"},
		RateLimit:             10,
		DisableSSRFProtection: true, // Allow localhost for testing
	}
	server := NewServer(config)

	req := httptest.NewRequest(http.MethodGet, "/proxy?url="+imageServer.URL+"/test.iso", nil)
	rec := httptest.NewRecorder()

	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", rec.Code)
	}

	body := rec.Body.String()
	if body != "test-image" {
		t.Errorf("Expected body 'test-image', got '%s'", body)
	}
}

func TestServer_ServeHTTP_RangeRequest(t *testing.T) {
	// Create a test HTTP server that supports Range requests
	imageServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data := []byte("0123456789")
		
		rangeHeader := r.Header.Get("Range")
		if rangeHeader != "" {
			// Simple range parsing (just for testing)
			if strings.HasPrefix(rangeHeader, "bytes=") {
				rangeSpec := strings.TrimPrefix(rangeHeader, "bytes=")
				var start, end int
				if _, err := fmt.Sscanf(rangeSpec, "%d-%d", &start, &end); err == nil {
					w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, len(data)))
					w.WriteHeader(http.StatusPartialContent)
					_, _ = w.Write(data[start : end+1])
					return
				}
			}
		}
		
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Length", fmt.Sprintf("%d", len(data)))
		_, _ = w.Write(data)
	}))
	defer imageServer.Close()

	config := &Config{
		Port:                  "8082",
		AllowedDomains:        []string{"*"},
		RateLimit:             10,
		DisableSSRFProtection: true, // Allow localhost for testing
	}
	server := NewServer(config)

	req := httptest.NewRequest(http.MethodGet, "/proxy?url="+imageServer.URL+"/test.iso", nil)
	req.Header.Set("Range", "bytes=0-4")
	rec := httptest.NewRecorder()

	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusPartialContent {
		t.Errorf("Expected status 206, got %d", rec.Code)
	}

	body := rec.Body.String()
	if body != "01234" {
		t.Errorf("Expected body '01234', got '%s'", body)
	}
}

func TestServer_isIPAllowed(t *testing.T) {
	_, subnet1, _ := net.ParseCIDR("192.168.1.0/24")
	_, subnet2, _ := net.ParseCIDR("10.0.0.0/8")

	tests := []struct {
		name    string
		subnets []*net.IPNet
		ip      string
		want    bool
	}{
		{
			name:    "no subnets configured",
			subnets: nil,
			ip:      "1.2.3.4",
			want:    true,
		},
		{
			name:    "IP in allowed subnet",
			subnets: []*net.IPNet{subnet1, subnet2},
			ip:      "192.168.1.100",
			want:    true,
		},
		{
			name:    "IP not in allowed subnet",
			subnets: []*net.IPNet{subnet1},
			ip:      "10.0.0.1",
			want:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := &Config{
				AllowedSubnets: tt.subnets,
			}
			server := NewServer(config)

			got := server.isIPAllowed(tt.ip)
			if got != tt.want {
				t.Errorf("isIPAllowed() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestServer_isDomainAllowed(t *testing.T) {
	tests := []struct {
		name    string
		domains []string
		host    string
		want    bool
	}{
		{
			name:    "wildcard allows all",
			domains: []string{"*"},
			host:    "example.com",
			want:    true,
		},
		{
			name:    "exact match",
			domains: []string{"example.com", "files.example.org"},
			host:    "example.com",
			want:    true,
		},
		{
			name:    "no match",
			domains: []string{"example.com"},
			host:    "badsite.com",
			want:    false,
		},
		{
			name:    "wildcard subdomain match",
			domains: []string{"*.example.com"},
			host:    "files.example.com",
			want:    true,
		},
		{
			name:    "host with port",
			domains: []string{"example.com"},
			host:    "example.com:8080",
			want:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := &Config{
				AllowedDomains: tt.domains,
			}
			server := NewServer(config)

			got := server.isDomainAllowed(tt.host)
			if got != tt.want {
				t.Errorf("isDomainAllowed() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestServer_validateTargetURL_PrivateIP(t *testing.T) {
	config := &Config{
		Port:           "8082",
		AllowedDomains: []string{"*"},
		RateLimit:      10,
	}
	server := NewServer(config)

	// Test with localhost (should be blocked)
	req := httptest.NewRequest(http.MethodGet, "/proxy?url=http://localhost/file.iso", nil)
	rec := httptest.NewRecorder()

	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("Expected status 403 for localhost, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestIsPrivateIP(t *testing.T) {
	tests := []struct {
		ip   string
		want bool
	}{
		{"127.0.0.1", true},
		{"10.0.0.1", true},
		{"192.168.1.1", true},
		{"172.16.0.1", true},
		{"169.254.1.1", true},
		{"8.8.8.8", false},
		{"1.1.1.1", false},
		{"::1", true},
		{"fe80::1", true},
		{"2001:4860:4860::8888", false},
	}

	for _, tt := range tests {
		t.Run(tt.ip, func(t *testing.T) {
			ip := net.ParseIP(tt.ip)
			if ip == nil {
				t.Fatalf("Failed to parse IP: %s", tt.ip)
			}
			got := isPrivateIP(ip)
			if got != tt.want {
				t.Errorf("isPrivateIP(%s) = %v, want %v", tt.ip, got, tt.want)
			}
		})
	}
}

func TestServer_MethodNotAllowed(t *testing.T) {
	config := &Config{
		Port:           "8082",
		AllowedDomains: []string{"*"},
		RateLimit:      10,
	}
	server := NewServer(config)

	req := httptest.NewRequest(http.MethodPost, "/proxy?url=http://example.com/file.iso", nil)
	rec := httptest.NewRecorder()

	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("Expected status 405, got %d", rec.Code)
	}
}

func TestServer_HeadRequest(t *testing.T) {
	// Create a test HTTP server
	imageServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Length", "1000")
		if r.Method == http.MethodHead {
			return
		}
		_, _ = w.Write([]byte("data"))
	}))
	defer imageServer.Close()

	config := &Config{
		Port:                  "8082",
		AllowedDomains:        []string{"*"},
		RateLimit:             10,
		DisableSSRFProtection: true, // Allow localhost for testing
	}
	server := NewServer(config)

	req := httptest.NewRequest(http.MethodHead, "/proxy?url="+imageServer.URL+"/test.iso", nil)
	rec := httptest.NewRecorder()

	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", rec.Code)
	}

	// HEAD request should not have a body
	if rec.Body.Len() > 0 {
		t.Errorf("Expected empty body for HEAD request, got %d bytes", rec.Body.Len())
	}

	// Should have Content-Length header
	if rec.Header().Get("Content-Length") == "" {
		t.Error("Expected Content-Length header")
	}
}

func TestGetClientIP(t *testing.T) {
	tests := []struct {
		name           string
		remoteAddr     string
		xForwardedFor  string
		xRealIP        string
		expectedIP     string
	}{
		{
			name:       "from RemoteAddr",
			remoteAddr: "192.168.1.100:12345",
			expectedIP: "192.168.1.100",
		},
		{
			name:          "from X-Forwarded-For",
			remoteAddr:    "10.0.0.1:12345",
			xForwardedFor: "203.0.113.1, 198.51.100.1",
			expectedIP:    "203.0.113.1",
		},
		{
			name:       "from X-Real-IP",
			remoteAddr: "10.0.0.1:12345",
			xRealIP:    "203.0.113.50",
			expectedIP: "203.0.113.50",
		},
		{
			name:          "X-Forwarded-For takes precedence over X-Real-IP",
			remoteAddr:    "10.0.0.1:12345",
			xForwardedFor: "203.0.113.1",
			xRealIP:       "203.0.113.50",
			expectedIP:    "203.0.113.1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/proxy", nil)
			req.RemoteAddr = tt.remoteAddr
			if tt.xForwardedFor != "" {
				req.Header.Set("X-Forwarded-For", tt.xForwardedFor)
			}
			if tt.xRealIP != "" {
				req.Header.Set("X-Real-IP", tt.xRealIP)
			}

			ip := getClientIP(req)
			if ip != tt.expectedIP {
				t.Errorf("getClientIP() = %v, want %v", ip, tt.expectedIP)
			}
		})
	}
}
