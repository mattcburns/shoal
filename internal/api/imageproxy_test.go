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

package api

import (
	"testing"
)

func TestRewriteImageURL(t *testing.T) {
	tests := []struct {
		name          string
		imageProxyURL string
		inputURL      string
		expectedURL   string
	}{
		{
			name:          "rewrite http URL",
			imageProxyURL: "http://localhost:8082",
			inputURL:      "http://fileserver.example.com/isos/ubuntu.iso",
			expectedURL:   "http://localhost:8082/proxy?url=http%3A%2F%2Ffileserver.example.com%2Fisos%2Fubuntu.iso",
		},
		{
			name:          "rewrite https URL",
			imageProxyURL: "http://localhost:8082",
			inputURL:      "https://cdn.example.org/images/debian.iso",
			expectedURL:   "http://localhost:8082/proxy?url=https%3A%2F%2Fcdn.example.org%2Fimages%2Fdebian.iso",
		},
		{
			name:          "no rewrite when proxy disabled",
			imageProxyURL: "",
			inputURL:      "http://fileserver.example.com/isos/ubuntu.iso",
			expectedURL:   "http://fileserver.example.com/isos/ubuntu.iso",
		},
		{
			name:          "no rewrite for non-http URLs",
			imageProxyURL: "http://localhost:8082",
			inputURL:      "nfs://server/path/to/image.iso",
			expectedURL:   "nfs://server/path/to/image.iso",
		},
		{
			name:          "handle special characters",
			imageProxyURL: "http://localhost:8082",
			inputURL:      "http://example.com/path/file name with spaces.iso",
			expectedURL:   "http://localhost:8082/proxy?url=http%3A%2F%2Fexample.com%2Fpath%2Ffile+name+with+spaces.iso",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := &Handler{
				imageProxyURL: tt.imageProxyURL,
			}

			result := h.rewriteImageURL(tt.inputURL)
			if result != tt.expectedURL {
				t.Errorf("rewriteImageURL() = %v, want %v", result, tt.expectedURL)
			}
		})
	}
}
