// internal/api/middleware/photo_security.go
package middleware

import (
	"bytes"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

// PhotoSecurityConfig holds validation constraints for profile photos.
type PhotoSecurityConfig struct {
	MaxFileSize  int64
	AllowedTypes []string
	MaxWidth     int
	MaxHeight    int
}

// PhotoValidationResult holds the result of photo security validation.
type PhotoValidationResult struct {
	Valid       bool
	ContentType string
	Width       int
	Height      int
	Error       string
	ErrorCode   string
}

// ValidatePhoto performs security validation on an uploaded photo file.
// It checks magic bytes, file size, dimensions, EXIF data, and malicious payloads.
func ValidatePhoto(data []byte, config PhotoSecurityConfig) *PhotoValidationResult {
	result := &PhotoValidationResult{}

	// 1. File size check
	if config.MaxFileSize > 0 && int64(len(data)) > config.MaxFileSize {
		result.Error = fmt.Sprintf("File size %d bytes exceeds maximum %d bytes", len(data), config.MaxFileSize)
		result.ErrorCode = "PHOTO_TOO_LARGE"
		return result
	}

	if len(data) < 8 {
		result.Error = "File too small to be a valid image"
		result.ErrorCode = "PHOTO_INVALID"
		return result
	}

	// 2. Magic bytes validation
	contentType := detectContentType(data)
	if contentType == "" {
		result.Error = "Unsupported image format. Allowed: JPEG, PNG, WebP"
		result.ErrorCode = "PHOTO_UNSUPPORTED_FORMAT"
		return result
	}

	// Check if content type is allowed
	allowed := false
	for _, t := range config.AllowedTypes {
		if strings.EqualFold(t, contentType) {
			allowed = true
			break
		}
	}
	if !allowed {
		result.Error = fmt.Sprintf("Content type %s not in allowed list", contentType)
		result.ErrorCode = "PHOTO_UNSUPPORTED_FORMAT"
		return result
	}

	// 3. Malicious payload detection
	if err := detectMaliciousPayload(data); err != nil {
		result.Error = err.Error()
		result.ErrorCode = "PHOTO_SECURITY_REJECTED"
		return result
	}

	// 4. Hidden script detection
	if err := detectHiddenScripts(data); err != nil {
		result.Error = err.Error()
		result.ErrorCode = "PHOTO_SECURITY_REJECTED"
		return result
	}

	// 5. Image dimensions check
	width, height, err := getImageDimensions(data, contentType)
	if err != nil {
		result.Error = fmt.Sprintf("Failed to read image dimensions: %v", err)
		result.ErrorCode = "PHOTO_INVALID"
		return result
	}

	if config.MaxWidth > 0 && width > config.MaxWidth {
		result.Error = fmt.Sprintf("Image width %dpx exceeds maximum %dpx", width, config.MaxWidth)
		result.ErrorCode = "PHOTO_TOO_LARGE_DIMENSIONS"
		return result
	}
	if config.MaxHeight > 0 && height > config.MaxHeight {
		result.Error = fmt.Sprintf("Image height %dpx exceeds maximum %dpx", height, config.MaxHeight)
		result.ErrorCode = "PHOTO_TOO_LARGE_DIMENSIONS"
		return result
	}

	// 6. Basic steganography check — detect single-color flood (all same pixel = suspicious)
	if isSingleColorFlood(data, contentType) {
		result.Error = "Image appears to be a single-color flood, which is not a valid profile photo"
		result.ErrorCode = "PHOTO_SECURITY_REJECTED"
		return result
	}

	result.Valid = true
	result.ContentType = contentType
	result.Width = width
	result.Height = height
	return result
}

// detectContentType checks file magic bytes to determine the actual content type.
func detectContentType(data []byte) string {
	// JPEG: FF D8 FF
	if len(data) >= 3 && data[0] == 0xFF && data[1] == 0xD8 && data[2] == 0xFF {
		return "image/jpeg"
	}
	// PNG: 89 50 4E 47 0D 0A 1A 0A
	if len(data) >= 8 && data[0] == 0x89 && data[1] == 0x50 && data[2] == 0x4E && data[3] == 0x47 &&
		data[4] == 0x0D && data[5] == 0x0A && data[6] == 0x1A && data[7] == 0x0A {
		return "image/png"
	}
	// WebP: RIFF....WEBP
	if len(data) >= 12 && string(data[0:4]) == "RIFF" && string(data[8:12]) == "WEBP" {
		return "image/webp"
	}
	// GIF: GIF87a or GIF89a
	if len(data) >= 6 && string(data[0:3]) == "GIF" && (string(data[3:6]) == "87a" || string(data[3:6]) == "89a") {
		return "image/gif"
	}
	return ""
}

// detectMaliciousPayload checks for non-image data embedded in the file.
func detectMaliciousPayload(data []byte) error {
	// Check for ZIP headers (PK magic bytes) — polyglot files
	if len(data) >= 4 && data[0] == 0x50 && data[1] == 0x4B && data[2] == 0x03 && data[3] == 0x04 {
		return fmt.Errorf("file contains ZIP/archive header — possible polyglot file")
	}
	// Check for MZ header (PE/EXE)
	if len(data) >= 2 && data[0] == 0x4D && data[1] == 0x5A {
		return fmt.Errorf("file contains PE/EXE header")
	}
	// Check for ELF header
	if len(data) >= 4 && data[0] == 0x7F && data[1] == 0x45 && data[2] == 0x4C && data[3] == 0x46 {
		return fmt.Errorf("file contains ELF header")
	}
	return nil
}

// detectHiddenScripts scans for script injection patterns in image metadata.
func detectHiddenScripts(data []byte) error {
	lower := bytes.ToLower(data)

	scriptPatterns := [][]byte{
		[]byte("<script"),
		[]byte("javascript:"),
		[]byte("eval("),
		[]byte("onclick"),
		[]byte("onerror"),
		[]byte("onload"),
		[]byte("expression("),
		[]byte("<iframe"),
		[]byte("document.cookie"),
	}

	for _, pattern := range scriptPatterns {
		if bytes.Contains(lower, pattern) {
			return fmt.Errorf("file contains suspicious script pattern: %s", string(pattern))
		}
	}
	return nil
}

// getImageDimensions decodes the image to get width and height.
func getImageDimensions(data []byte, contentType string) (int, int, error) {
	reader := bytes.NewReader(data)
	config, _, err := image.DecodeConfig(reader)
	if err != nil {
		return 0, 0, err
	}
	return config.Width, config.Height, nil
}

// isSingleColorFlood checks if the image is a single solid color (basic steganography check).
func isSingleColorFlood(data []byte, contentType string) bool {
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return false // Can't decode — skip check
	}

	bounds := img.Bounds()
	if bounds.Dx() < 4 || bounds.Dy() < 4 {
		return false // Too small to check
	}

	// Sample a few pixels from the image
	samplePoints := []image.Point{
		{bounds.Min.X + 1, bounds.Min.Y + 1},
		{bounds.Min.X + bounds.Dx()/2, bounds.Min.Y + bounds.Dy()/2},
		{bounds.Max.X - 2, bounds.Max.Y - 2},
		{bounds.Min.X + bounds.Dx()/4, bounds.Min.Y + bounds.Dy()/4},
	}

	firstColor := img.At(samplePoints[0].X, samplePoints[0].Y)
	r1, g1, b1, _ := firstColor.RGBA()

	allSame := true
	for _, p := range samplePoints[1:] {
		c := img.At(p.X, p.Y)
		r2, g2, b2, _ := c.RGBA()
		if r1 != r2 || g1 != g2 || b1 != b2 {
			allSame = false
			break
		}
	}

	return allSame
}

// EXIF GPS strip patterns — markers that indicate GPS data in JPEG APP1 segment.
var exifGPSMarkers = []string{
	"GPS",
	"IfDh",
}

// HasEXIFData checks if the image contains EXIF data (detection only).
func HasEXIFData(data []byte) bool {
	// JPEG APP1 marker: FF E1
	if len(data) < 4 || data[0] != 0xFF || data[1] != 0xE1 {
		return false
	}
	// Search for GPS markers in the EXIF segment
	for _, marker := range exifGPSMarkers {
		if bytes.Contains(data, []byte(marker)) {
			return true
		}
	}
	return false
}

// StripEXIF removes EXIF metadata (including GPS) from JPEG bytes.
// Returns the cleaned bytes. Non-JPEG files are returned as-is.
// This protects user location privacy (DPDPA compliance).
func StripEXIF(data []byte) []byte {
	if len(data) < 4 {
		return data
	}

	// Only process JPEG files
	if data[0] != 0xFF || data[1] != 0xD8 {
		return data
	}

	// Walk JPEG markers to find and remove APP1 (EXIF) segment
	// JPEG format: FF D8 (SOI) ... markers ... FF D9 (EOI)
	result := make([]byte, 0, len(data))
	result = append(result, data[:2]...) // Copy SOI marker (FF D8)

	i := 2 // Skip SOI
	for i < len(data)-1 {
		// Find next marker (FF xx)
		if data[i] != 0xFF {
			result = append(result, data[i])
			i++
			continue
		}

		// Skip padding FF bytes
		for i < len(data) && data[i] == 0xFF {
			i++
		}
		if i >= len(data) {
			break
		}

		marker := data[i]
		i++

		// SOS (FF DA) or EOI (FF D9) — copy rest of file
		if marker == 0xDA || marker == 0xD9 {
			result = append(result, 0xFF, marker)
			result = append(result, data[i:]...)
			return result
		}

		// Read segment length (2 bytes, big-endian)
		if i+1 >= len(data) {
			result = append(result, 0xFF, marker)
			result = append(result, data[i:]...)
			return result
		}
		segLen := int(data[i])<<8 | int(data[i+1])
		segStart := i
		segEnd := i + segLen
		if segEnd > len(data) {
			segEnd = len(data)
		}

		// APP1 (FF E1) — check if it's EXIF, skip if so
		if marker == 0xE1 {
			isEXIF := segEnd > segStart+8 &&
				string(data[segStart+2:segStart+6]) == "Exif" &&
				data[segStart+6] == 0x00 && data[segStart+7] == 0x00
			if isEXIF {
				// Skip this segment (EXIF data stripped)
				i = segEnd
				continue
			}
		}

		// All other markers — copy as-is
		result = append(result, 0xFF, marker)
		result = append(result, data[segStart:segEnd]...)
		i = segEnd
	}

	return result
}

// PhotoSecurityMiddleware returns a gin.HandlerFunc that validates uploaded photos.
// It expects a multipart form field named "photo".
func PhotoSecurityMiddleware(config PhotoSecurityConfig) gin.HandlerFunc {
	return func(c *gin.Context) {
		// Only validate if there's a file upload
		file, header, err := c.Request.FormFile("photo")
		if err != nil {
			c.Next() // No photo uploaded — continue
			return
		}
		defer file.Close()

		// Read file data
		data, err := io.ReadAll(io.LimitReader(file, config.MaxFileSize+1))
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{
				"success": false,
				"error":   "PHOTO_READ_FAILED",
				"message": "Failed to read uploaded photo",
			})
			c.Abort()
			return
		}

		// Validate
		result := ValidatePhoto(data, config)
		if !result.Valid {
			c.JSON(http.StatusBadRequest, gin.H{
				"success": false,
				"error":   result.ErrorCode,
				"message": result.Error,
			})
			c.Abort()
			return
		}

		// Strip EXIF data (including GPS) — DPDPA privacy compliance
		// This removes metadata before upload, protecting user location
		if HasEXIFData(data) {
			c.Set("photoHasGPS", true)
			data = StripEXIF(data)
		}

		// Store validated + sanitized data in context for handler
		c.Set("photoData", data)
		c.Set("photoContentType", result.ContentType)
		c.Set("photoFilename", header.Filename)
		c.Set("photoWidth", result.Width)
		c.Set("photoHeight", result.Height)
		c.Set("photoValidated", true)

		c.Next()
	}
}
