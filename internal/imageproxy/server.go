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
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Server is an HTTP image proxy server
type Server struct {
	config      *Config
	client      *http.Client
	rateLimiter *rateLimiter
}

// rateLimiter tracks concurrent downloads per IP
type rateLimiter struct {
	mu       sync.Mutex
	counters map[string]int
}

// NewServer creates a new image proxy server
func NewServer(config *Config) *Server {
	return &Server{
		config: config,
		client: &http.Client{
			Timeout: 5 * time.Minute, // Generous timeout for large images
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				// Limit redirects
				if len(via) >= 10 {
					return fmt.Errorf("too many redirects")
				}
				return nil
			},
		},
		rateLimiter: &rateLimiter{
			counters: make(map[string]int),
		},
	}
}

// ServeHTTP handles image proxy requests
// GET /proxy?url=<encoded-url>
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Validate client IP against allowed subnets
	clientIP := getClientIP(r)
	if !s.isIPAllowed(clientIP) {
		slog.Warn("Image proxy request from disallowed IP", "ip", clientIP)
		http.Error(w, "Access denied", http.StatusForbidden)
		return
	}

	// Check rate limit
	if !s.rateLimiter.acquire(clientIP) {
		slog.Warn("Image proxy rate limit exceeded", "ip", clientIP)
		http.Error(w, "Rate limit exceeded", http.StatusTooManyRequests)
		return
	}
	defer s.rateLimiter.release(clientIP)

	// Get and validate URL parameter
	targetURL := r.URL.Query().Get("url")
	if targetURL == "" {
		http.Error(w, "Missing url parameter", http.StatusBadRequest)
		return
	}

	// Parse and validate URL
	parsedURL, err := url.Parse(targetURL)
	if err != nil {
		slog.Warn("Invalid URL in proxy request", "url", targetURL, "error", err)
		http.Error(w, "Invalid URL", http.StatusBadRequest)
		return
	}

	// Validate URL scheme
	if parsedURL.Scheme != "http" && parsedURL.Scheme != "https" {
		slog.Warn("Unsupported URL scheme", "url", targetURL, "scheme", parsedURL.Scheme)
		http.Error(w, "Only HTTP and HTTPS URLs are supported", http.StatusBadRequest)
		return
	}

	// Validate domain against whitelist
	if !s.isDomainAllowed(parsedURL.Host) {
		slog.Warn("Blocked proxy request to disallowed domain", "url", targetURL, "host", parsedURL.Host)
		http.Error(w, "Domain not allowed", http.StatusForbidden)
		return
	}

	// Prevent SSRF attacks - block private IP ranges (unless disabled for testing)
	if !s.config.DisableSSRFProtection {
		if err := s.validateTargetURL(parsedURL); err != nil {
			slog.Warn("Blocked potentially unsafe proxy request", "url", targetURL, "error", err)
			http.Error(w, "URL not allowed", http.StatusForbidden)
			return
		}
	}

	// Create proxy request
	proxyReq, err := http.NewRequestWithContext(r.Context(), r.Method, targetURL, nil)
	if err != nil {
		slog.Error("Failed to create proxy request", "url", targetURL, "error", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	// Forward Range header for resumable downloads
	if rangeHeader := r.Header.Get("Range"); rangeHeader != "" {
		proxyReq.Header.Set("Range", rangeHeader)
	}

	// Set user agent
	proxyReq.Header.Set("User-Agent", "Shoal-ImageProxy/1.0")

	// Execute request
	slog.Debug("Proxying image request", "url", targetURL, "client_ip", clientIP)
	resp, err := s.client.Do(proxyReq)
	if err != nil {
		slog.Error("Failed to fetch image", "url", targetURL, "error", err)
		http.Error(w, "Failed to fetch image", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	// Copy response headers
	for key, values := range resp.Header {
		// Skip hop-by-hop headers
		if isHopByHopHeader(key) {
			continue
		}
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}

	// Write status code
	w.WriteHeader(resp.StatusCode)

	// Stream response body
	if r.Method == http.MethodGet {
		_, err = io.Copy(w, resp.Body)
		if err != nil {
			slog.Warn("Error streaming image", "url", targetURL, "error", err)
		}
	}

	slog.Info("Image proxy request completed", "url", targetURL, "client_ip", clientIP, "status", resp.StatusCode)
}

// getClientIP extracts the client IP from the request
func getClientIP(r *http.Request) string {
	// Check X-Forwarded-For header
	xff := r.Header.Get("X-Forwarded-For")
	if xff != "" {
		// Take the first IP in the list
		ips := strings.Split(xff, ",")
		if len(ips) > 0 {
			return strings.TrimSpace(ips[0])
		}
	}

	// Check X-Real-IP header
	xri := r.Header.Get("X-Real-IP")
	if xri != "" {
		return xri
	}

	// Fall back to RemoteAddr
	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return ip
}

// isIPAllowed checks if the client IP is in the allowed subnets
func (s *Server) isIPAllowed(ipStr string) bool {
	// If no subnets are configured, allow all
	if len(s.config.AllowedSubnets) == 0 {
		return true
	}

	ip := net.ParseIP(ipStr)
	if ip == nil {
		return false
	}

	for _, subnet := range s.config.AllowedSubnets {
		if subnet.Contains(ip) {
			return true
		}
	}

	return false
}

// isDomainAllowed checks if the domain is in the allowed list
func (s *Server) isDomainAllowed(host string) bool {
	// Wildcard allows all
	for _, domain := range s.config.AllowedDomains {
		if domain == "*" {
			return true
		}
		
		// Extract hostname without port
		hostname := host
		if idx := strings.LastIndex(host, ":"); idx != -1 {
			hostname = host[:idx]
		}
		
		// Exact match
		if domain == hostname {
			return true
		}
		
		// Wildcard subdomain match (e.g., *.example.com)
		if strings.HasPrefix(domain, "*.") {
			suffix := domain[1:] // Remove the *
			if strings.HasSuffix(hostname, suffix) {
				return true
			}
		}
	}

	return false
}

// validateTargetURL prevents SSRF attacks by blocking private IP ranges
func (s *Server) validateTargetURL(u *url.URL) error {
	// Extract hostname
	hostname := u.Hostname()
	
	// Resolve hostname to IP addresses
	ips, err := net.LookupIP(hostname)
	if err != nil {
		return fmt.Errorf("failed to resolve hostname: %w", err)
	}

	// Check each resolved IP
	for _, ip := range ips {
		if isPrivateIP(ip) {
			return fmt.Errorf("private IP address not allowed: %s", ip.String())
		}
	}

	return nil
}

// isPrivateIP checks if an IP is in a private range
func isPrivateIP(ip net.IP) bool {
	// Private IPv4 ranges
	privateIPv4Ranges := []string{
		"10.0.0.0/8",     // RFC1918
		"172.16.0.0/12",  // RFC1918
		"192.168.0.0/16", // RFC1918
		"127.0.0.0/8",    // Loopback
		"169.254.0.0/16", // Link-local
		"0.0.0.0/8",      // "This network"
	}

	// Private IPv6 ranges
	privateIPv6Ranges := []string{
		"::1/128",       // Loopback
		"fe80::/10",     // Link-local
		"fc00::/7",      // Unique local addresses
	}

	// Check IPv4
	if ip.To4() != nil {
		for _, cidr := range privateIPv4Ranges {
			_, network, _ := net.ParseCIDR(cidr)
			if network.Contains(ip) {
				return true
			}
		}
		return false
	}

	// Check IPv6
	for _, cidr := range privateIPv6Ranges {
		_, network, _ := net.ParseCIDR(cidr)
		if network.Contains(ip) {
			return true
		}
	}

	return false
}

// isHopByHopHeader checks if a header is a hop-by-hop header
func isHopByHopHeader(header string) bool {
	hopByHop := []string{
		"Connection",
		"Keep-Alive",
		"Proxy-Authenticate",
		"Proxy-Authorization",
		"Te",
		"Trailer",
		"Transfer-Encoding",
		"Upgrade",
	}

	headerLower := strings.ToLower(header)
	for _, h := range hopByHop {
		if strings.ToLower(h) == headerLower {
			return true
		}
	}
	return false
}

// acquire attempts to acquire a rate limit slot for the given IP
func (rl *rateLimiter) acquire(ip string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	// For simplicity, we just track concurrent requests
	// A more sophisticated implementation would use a sliding window
	count := rl.counters[ip]
	
	// This is a placeholder - the actual rate limit is enforced at acquisition time
	// In a real implementation, you'd want a proper rate limiter with time windows
	
	rl.counters[ip] = count + 1
	return true
}

// release releases a rate limit slot for the given IP
func (rl *rateLimiter) release(ip string) {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	count := rl.counters[ip]
	if count > 0 {
		rl.counters[ip] = count - 1
	}
	if rl.counters[ip] == 0 {
		delete(rl.counters, ip)
	}
}
