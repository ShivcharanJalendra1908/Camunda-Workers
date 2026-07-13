package middleware

import (
	"bytes"
	"encoding/binary"
	"testing"
)

func TestHasEXIFData(t *testing.T) {
	tests := []struct {
		name     string
		data     []byte
		expected bool
	}{
		{
			name:     "empty data",
			data:     []byte{},
			expected: false,
		},
		{
			name:     "too short",
			data:     []byte{0xFF, 0xE1},
			expected: false,
		},
		{
			name:     "not JPEG",
			data:     []byte{0x89, 0x50, 0x4E, 0x47},
			expected: false,
		},
		{
			name: "JPEG with EXIF GPS",
			data: func() []byte {
				buf := []byte{0xFF, 0xE1}
				// EXIF segment with GPS marker
				exifData := []byte("Exif\x00\x00GPS\x00\x01")
				segLen := uint16(len(exifData) + 2)
				var lenBytes [2]byte
				binary.BigEndian.PutUint16(lenBytes[:], segLen)
				buf = append(buf, lenBytes[:]...)
				return append(buf, exifData...)
			}(),
			expected: true,
		},
		{
			name: "JPEG with EXIF but no GPS",
			data: func() []byte {
				buf := []byte{0xFF, 0xE1}
				exifData := []byte("Exif\x00\x00Make\x00Canon")
				segLen := uint16(len(exifData) + 2)
				var lenBytes [2]byte
				binary.BigEndian.PutUint16(lenBytes[:], segLen)
				buf = append(buf, lenBytes[:]...)
				return append(buf, exifData...)
			}(),
			expected: false,
		},
		{
			name: "JPEG without APP1",
			data: func() []byte {
				return []byte{0xFF, 0xD8, 0xFF, 0xDB, 0x00, 0x43, 0x00}
			}(),
			expected: false,
		},
		{
			name: "JPEG with IfDh marker in EXIF",
			data: func() []byte {
				buf := []byte{0xFF, 0xE1}
				exifData := []byte("Exif\x00\x00IfDh\x00\x01")
				segLen := uint16(len(exifData) + 2)
				var lenBytes [2]byte
				binary.BigEndian.PutUint16(lenBytes[:], segLen)
				buf = append(buf, lenBytes[:]...)
				return append(buf, exifData...)
			}(),
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := HasEXIFData(tt.data)
			if got != tt.expected {
				t.Errorf("HasEXIFData() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestStripEXIF(t *testing.T) {
	tests := []struct {
		name           string
		data           []byte
		expectIdentical bool // true if output should equal input (non-JPEG or no EXIF)
	}{
		{
			name:           "empty data",
			data:           []byte{},
			expectIdentical: true,
		},
		{
			name:           "PNG unchanged",
			data:           []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A, 0x00},
			expectIdentical: true,
		},
		{
			name:           "too short",
			data:           []byte{0xFF, 0xD8},
			expectIdentical: true,
		},
		{
			name: "JPEG without EXIF segment",
			data: func() []byte {
				// Minimal JPEG: SOI + DQT + SOF0 + SOS + EOI
				buf := []byte{0xFF, 0xD8} // SOI
				buf = append(buf, 0xFF, 0xDB, 0x00, 0x43, 0x00) // DQT
				buf = append(buf, 0xFF, 0xDA, 0x00, 0x08, 0x01, 0x01, 0x00, 0x00, 0x3F, 0x00, 0x7B) // SOS
				buf = append(buf, 0xFF, 0xD9) // EOI
				return buf
			}(),
			expectIdentical: false, // Output may be slightly different due to marker walking
		},
		{
			name: "JPEG with EXIF segment is stripped",
			data: func() []byte {
				buf := []byte{0xFF, 0xD8} // SOI

				// APP1 (EXIF) segment
				exifPayload := []byte("Exif\x00\x00GPS\x00\x01Some GPS data here!!")
				segLen := uint16(len(exifPayload) + 2)
				var lenBytes [2]byte
				binary.BigEndian.PutUint16(lenBytes[:], segLen)
				buf = append(buf, 0xFF, 0xE1)
				buf = append(buf, lenBytes[:]...)
				buf = append(buf, exifPayload...)

				// DQT segment (should be preserved)
				buf = append(buf, 0xFF, 0xDB, 0x00, 0x43, 0x00)

				// SOS + EOI
				buf = append(buf, 0xFF, 0xDA, 0x00, 0x08, 0x01, 0x01, 0x00, 0x00, 0x3F, 0x00, 0x7B)
				buf = append(buf, 0xFF, 0xD9)
				return buf
			}(),
			expectIdentical: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := StripEXIF(tt.data)

			if tt.expectIdentical {
				if !bytes.Equal(result, tt.data) {
					t.Errorf("StripEXIF() changed data, got %d bytes, want %d bytes", len(result), len(tt.data))
				}
				return
			}

		// For non-identical cases, verify EXIF is removed
		if len(tt.data) >= 4 && tt.data[0] == 0xFF && tt.data[1] == 0xD8 {
			// JPEG: check no EXIF segment remains
			for i := 2; i < len(result)-1; i++ {
				if result[i] == 0xFF && result[i+1] == 0xE1 {
					if i+8 <= len(result) && string(result[i+4:i+8]) == "Exif" {
						t.Error("StripEXIF() did not remove EXIF segment")
					}
				}
			}
		}

			// Result should not be nil
			if result == nil {
				t.Error("StripEXIF() returned nil")
			}
		})
	}
}

func TestStripEXIF_PreservesSOI(t *testing.T) {
	// Build a minimal JPEG with EXIF
	data := []byte{0xFF, 0xD8} // SOI
	exifPayload := []byte("Exif\x00\x00GPS\x00\x01test")
	segLen := uint16(len(exifPayload) + 2)
	var lenBytes [2]byte
	binary.BigEndian.PutUint16(lenBytes[:], segLen)
	data = append(data, 0xFF, 0xE1)
	data = append(data, lenBytes[:]...)
	data = append(data, exifPayload...)
	data = append(data, 0xFF, 0xD9) // EOI

	result := StripEXIF(data)

	// SOI must be preserved
	if len(result) < 2 || result[0] != 0xFF || result[1] != 0xD8 {
		t.Error("StripEXIF() removed SOI marker")
	}

	// EXIF must be removed — no FF E1 with Exif content
	for i := 2; i < len(result)-1; i++ {
		if result[i] == 0xFF && result[i+1] == 0xE1 {
			if i+8 <= len(result) && string(result[i+4:i+8]) == "Exif" {
				t.Error("StripEXIF() did not remove EXIF segment")
			}
		}
	}
}

func TestValidatePhoto(t *testing.T) {
	// Minimal valid PNG
	validPNG := []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A, 0x00, 0x00, 0x00, 0x0D, 0x49, 0x48, 0x44, 0x52, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01, 0x08, 0x02, 0x00, 0x00, 0x00, 0x90, 0x77, 0x53, 0xDE, 0x00, 0x00, 0x00, 0x0C, 0x49, 0x44, 0x41, 0x54, 0x08, 0xD7, 0x63, 0xF8, 0xCF, 0xC0, 0x00, 0x00, 0x00, 0x02, 0x00, 0x01, 0xE2, 0x21, 0xBC, 0x33, 0x00, 0x00, 0x00, 0x00, 0x49, 0x45, 0x4E, 0x44, 0xAE, 0x42, 0x60, 0x82}
	// ZIP file disguised as image (needs 8+ bytes to pass minimum size check)
	fakeFile := []byte{0x50, 0x4B, 0x03, 0x04, 0x00, 0x00, 0x00, 0x00}
	// File with hidden script
	scriptFile := []byte{0xFF, 0xD8, 0xFF, 0xE0, 0x00, 0x10, 0x4A, 0x46, 0x49, 0x46, 0x00, 0x01, 0x01, 0x00, 0x00, 0x01, 0x00, 0x01, 0x00, 0x00, 0x3C, 0x73, 0x63, 0x72, 0x69, 0x70, 0x74, 0x3E}

	config := PhotoSecurityConfig{
		MaxFileSize:  5 * 1024 * 1024,
		AllowedTypes: []string{"image/jpeg", "image/png"},
		MaxWidth:     4000,
		MaxHeight:    4000,
	}

	tests := []struct {
		name        string
		data        []byte
		valid       bool
		errorCode   string
	}{
		{
			name:      "file too small",
			data:      []byte{0x00, 0x01},
			valid:     false,
			errorCode: "PHOTO_INVALID",
		},
		{
			name:      "ZIP header rejected",
			data:      fakeFile,
			valid:     false,
			errorCode: "PHOTO_UNSUPPORTED_FORMAT",
		},
		{
			name:      "script injection rejected",
			data:      scriptFile,
			valid:     false,
			errorCode: "PHOTO_SECURITY_REJECTED",
		},
		{
			name:      "PNG accepted",
			data:      validPNG,
			valid:     true,
			errorCode: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ValidatePhoto(tt.data, config)
			if result.Valid != tt.valid {
				t.Errorf("ValidatePhoto() Valid = %v, want %v, error: %s", result.Valid, tt.valid, result.Error)
			}
			if tt.errorCode != "" && result.ErrorCode != tt.errorCode {
				t.Errorf("ValidatePhoto() ErrorCode = %q, want %q", result.ErrorCode, tt.errorCode)
			}
		})
	}
}

func TestDetectContentType(t *testing.T) {
	tests := []struct {
		name        string
		data        []byte
		contentType string
	}{
		{"JPEG", []byte{0xFF, 0xD8, 0xFF, 0xE0}, "image/jpeg"},
		{"PNG", []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A}, "image/png"},
		{"WebP", []byte("RIFF\x00\x00\x00\x00WEBP"), "image/webp"},
		{"GIF87a", []byte("GIF87a"), "image/gif"},
		{"GIF89a", []byte("GIF89a"), "image/gif"},
		{"unknown", []byte{0x00, 0x01, 0x02, 0x03}, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := detectContentType(tt.data)
			if got != tt.contentType {
				t.Errorf("detectContentType() = %q, want %q", got, tt.contentType)
			}
		})
	}
}

func TestDetectMaliciousPayload(t *testing.T) {
	tests := []struct {
		name    string
		data    []byte
		wantErr bool
	}{
		{"ZIP header", []byte{0x50, 0x4B, 0x03, 0x04}, true},
		{"MZ header", []byte{0x4D, 0x5A}, true},
		{"ELF header", []byte{0x7F, 0x45, 0x4C, 0x46}, true},
		{"clean data", []byte{0xFF, 0xD8, 0xFF, 0xE0}, false},
		{"empty data", []byte{}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := detectMaliciousPayload(tt.data)
			if (err != nil) != tt.wantErr {
				t.Errorf("detectMaliciousPayload() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestDetectHiddenScripts(t *testing.T) {
	tests := []struct {
		name    string
		data    []byte
		wantErr bool
	}{
		{"script tag", []byte(`<script>alert(1)</script>`), true},
		{"javascript URI", []byte(`javascript:void(0)`), true},
		{"eval function", []byte(`eval(code)`), true},
		{"onclick handler", []byte(`onclick=alert(1)`), true},
		{"clean data", []byte(`normal image data`), false},
		{"empty data", []byte{}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := detectHiddenScripts(tt.data)
			if (err != nil) != tt.wantErr {
				t.Errorf("detectHiddenScripts() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
