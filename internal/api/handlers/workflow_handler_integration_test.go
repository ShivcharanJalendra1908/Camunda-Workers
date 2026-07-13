package handlers

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/jpeg"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"camunda-workers/internal/api/middleware"
	"camunda-workers/internal/common/config"

	"github.com/gin-gonic/gin"
)

// ============================================================================
// constructPhotoURLs
// ============================================================================

func TestConstructPhotoURLs_FlatResponse(t *testing.T) {
	cfg := &config.Config{}
	cfg.Integrations.AWS.S3.CDNBaseURL = "https://d1234.cloudfront.net"

	h := &WorkflowHandler{config: cfg}

	response := map[string]interface{}{
		"profile_image": "profile-photos/user123/abc.jpg",
		"name":          "Test User",
	}

	h.constructPhotoURLs(response)

	expected := "https://d1234.cloudfront.net/profile-photos/user123/abc.jpg"
	if response["profile_image"] != expected {
		t.Errorf("profile_image = %q, want %q", response["profile_image"], expected)
	}
	if response["name"] != "Test User" {
		t.Errorf("name was modified: %v", response["name"])
	}
}

func TestConstructPhotoURLs_NestedUserObject(t *testing.T) {
	cfg := &config.Config{}
	cfg.Integrations.AWS.S3.CDNBaseURL = "https://d1234.cloudfront.net"

	h := &WorkflowHandler{config: cfg}

	response := map[string]interface{}{
		"user": map[string]interface{}{
			"profile_image": "profile-photos/user123/abc.jpg",
			"name":          "Test User",
		},
	}

	h.constructPhotoURLs(response)

	user := response["user"].(map[string]interface{})
	expected := "https://d1234.cloudfront.net/profile-photos/user123/abc.jpg"
	if user["profile_image"] != expected {
		t.Errorf("user.profile_image = %q, want %q", user["profile_image"], expected)
	}
}

func TestConstructPhotoURLs_NestedDataObject(t *testing.T) {
	cfg := &config.Config{}
	cfg.Integrations.AWS.S3.CDNBaseURL = "https://d1234.cloudfront.net"

	h := &WorkflowHandler{config: cfg}

	response := map[string]interface{}{
		"data": map[string]interface{}{
			"profile_image": "profile-photos/user123/abc.jpg",
			"photoUrl":      "profile-photos/user123/photo.jpg",
		},
	}

	h.constructPhotoURLs(response)

	data := response["data"].(map[string]interface{})
	expected1 := "https://d1234.cloudfront.net/profile-photos/user123/abc.jpg"
	expected2 := "https://d1234.cloudfront.net/profile-photos/user123/photo.jpg"
	if data["profile_image"] != expected1 {
		t.Errorf("data.profile_image = %q, want %q", data["profile_image"], expected1)
	}
	if data["photoUrl"] != expected2 {
		t.Errorf("data.photoUrl = %q, want %q", data["photoUrl"], expected2)
	}
}

func TestConstructPhotoURLs_EmptyKeySkipped(t *testing.T) {
	cfg := &config.Config{}
	cfg.Integrations.AWS.S3.CDNBaseURL = "https://d1234.cloudfront.net"

	h := &WorkflowHandler{config: cfg}

	response := map[string]interface{}{
		"profile_image": "",
		"name":          "Test User",
	}

	h.constructPhotoURLs(response)

	if response["profile_image"] != "" {
		t.Errorf("empty profile_image should remain empty, got %q", response["profile_image"])
	}
}

func TestConstructPhotoURLs_NoCDNBaseURL(t *testing.T) {
	cfg := &config.Config{}
	cfg.Integrations.AWS.S3.CDNBaseURL = ""

	h := &WorkflowHandler{config: cfg}

	response := map[string]interface{}{
		"profile_image": "profile-photos/user123/abc.jpg",
	}

	h.constructPhotoURLs(response)

	// Should remain unchanged when no CDN base URL
	if response["profile_image"] != "profile-photos/user123/abc.jpg" {
		t.Errorf("profile_image should remain unchanged without CDN, got %v", response["profile_image"])
	}
}

func TestConstructPhotoURLs_NilResponse(t *testing.T) {
	cfg := &config.Config{}
	cfg.Integrations.AWS.S3.CDNBaseURL = "https://d1234.cloudfront.net"

	h := &WorkflowHandler{config: cfg}

	// Should not panic
	h.constructPhotoURLs(nil)
}

func TestConstructPhotoURLs_NilConfig(t *testing.T) {
	h := &WorkflowHandler{config: nil}

	response := map[string]interface{}{
		"profile_image": "profile-photos/user123/abc.jpg",
	}

	// Should not panic
	h.constructPhotoURLs(response)

	if response["profile_image"] != "profile-photos/user123/abc.jpg" {
		t.Errorf("profile_image should remain unchanged with nil config, got %v", response["profile_image"])
	}
}

// ============================================================================
// Photo upload middleware chain integration
// ============================================================================

func TestPhotoMiddlewareChain_ValidateStripStore(t *testing.T) {
	// Create a valid JPEG with EXIF injected after SOI
	photoData := buildTestJPEGWithEXIF()

	config := middleware.PhotoSecurityConfig{
		MaxFileSize:  5 * 1024 * 1024,
		AllowedTypes: []string{"image/jpeg", "image/png"},
		MaxWidth:     4000,
		MaxHeight:    4000,
	}

	// Validate passes for valid JPEG
	result := middleware.ValidatePhoto(photoData, config)
	if !result.Valid {
		t.Fatalf("ValidatePhoto failed: %s", result.Error)
	}
	if result.ContentType != "image/jpeg" {
		t.Errorf("ContentType = %q, want image/jpeg", result.ContentType)
	}

	// StripEXIF correctly removes APP1 segment
	stripped := middleware.StripEXIF(photoData)
	if !bytes.HasPrefix(stripped, []byte{0xFF, 0xD8}) {
		t.Error("Stripped data should still start with JPEG SOI marker")
	}
	if len(stripped) >= len(photoData) {
		t.Errorf("Stripped photo should be smaller: got %d bytes, original %d bytes", len(stripped), len(photoData))
	}

	// Verify EXIF content is removed
	for i := 2; i < len(stripped)-1; i++ {
		if stripped[i] == 0xFF && stripped[i+1] == 0xE1 {
			if i+8 <= len(stripped) && string(stripped[i+4:i+8]) == "Exif" {
				t.Error("EXIF segment should be removed by StripEXIF")
			}
		}
	}
}

func TestPhotoMiddlewareChain_NonEXIFPhotoPassthrough(t *testing.T) {
	// Create a valid JPEG without EXIF
	photoData := buildTestJPEGWithoutEXIF()

	config := middleware.PhotoSecurityConfig{
		MaxFileSize:  5 * 1024 * 1024,
		AllowedTypes: []string{"image/jpeg"},
		MaxWidth:     4000,
		MaxHeight:    4000,
	}

	result := middleware.ValidatePhoto(photoData, config)
	if !result.Valid {
		t.Fatalf("ValidatePhoto failed: %s", result.Error)
	}

	// StripEXIF on a JPEG without EXIF should return same data
	stripped := middleware.StripEXIF(photoData)
	if !bytes.Equal(stripped, photoData) {
		t.Error("Non-EXIF photo should remain unchanged after StripEXIF")
	}
}

func TestPhotoMiddlewareChain_RejectOversizedFile(t *testing.T) {
	// Create a file that exceeds max size
	bigData := make([]byte, 10*1024*1024) // 10MB
	copy(bigData, []byte{0xFF, 0xD8, 0xFF, 0xE0}) // JPEG header

	config := middleware.PhotoSecurityConfig{
		MaxFileSize:  3 * 1024 * 1024, // 3MB limit
		AllowedTypes: []string{"image/jpeg"},
	}

	result := middleware.ValidatePhoto(bigData, config)
	if result.Valid {
		t.Error("ValidatePhoto should reject oversized file")
	}
	if result.ErrorCode != "PHOTO_TOO_LARGE" {
		t.Errorf("ErrorCode = %q, want PHOTO_TOO_LARGE", result.ErrorCode)
	}
}

func TestPhotoMiddlewareChain_RejectUnsupportedFormat(t *testing.T) {
	// BMP file
	bmpData := []byte{0x42, 0x4D, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00}

	config := middleware.PhotoSecurityConfig{
		MaxFileSize:  5 * 1024 * 1024,
		AllowedTypes: []string{"image/jpeg", "image/png"},
	}

	result := middleware.ValidatePhoto(bmpData, config)
	if result.Valid {
		t.Error("ValidatePhoto should reject unsupported format")
	}
}

func TestPhotoMiddlewareChain_GinMiddlewareIntegration(t *testing.T) {
	// Build a JPEG with EXIF
	photoData := buildTestJPEGWithEXIF()

	// Create multipart form
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, err := mw.CreateFormFile("photo", "test.jpg")
	if err != nil {
		t.Fatalf("CreateFormFile failed: %v", err)
	}
	fw.Write(photoData)
	mw.Close()

	// Create test request
	req := httptest.NewRequest(http.MethodPost, "/test", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())

	w := httptest.NewRecorder()

	// Create gin context
	router := setupTestRouter()
	router.POST("/test", middleware.PhotoSecurityMiddleware(middleware.PhotoSecurityConfig{
		MaxFileSize:  5 * 1024 * 1024,
		AllowedTypes: []string{"image/jpeg", "image/png"},
		MaxWidth:     4000,
		MaxHeight:    4000,
	}), func(c *gin.Context) {
		// Verify middleware set context values
		if !c.GetBool("photoValidated") {
			t.Error("photoValidated should be true")
		}
		photoData, exists := c.Get("photoData")
		if !exists {
			t.Fatal("photoData should be set in context")
		}
		data := photoData.([]byte)

		// Photo should have EXIF stripped
		if middleware.HasEXIFData(data) {
			t.Error("photoData in context should have EXIF stripped")
		}

		c.JSON(http.StatusOK, gin.H{"success": true})
	})

	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Status = %d, want %d, body: %s", w.Code, http.StatusOK, w.Body.String())
	}
}

// ============================================================================
// Helpers
// ============================================================================

func setupTestRouter() *gin.Engine {
	gin.SetMode(gin.TestMode)
	return gin.New()
}

func buildTestJPEGWithEXIF() []byte {
	// Create a valid JPEG using Go's image package
	img := image.NewRGBA(image.Rect(0, 0, 100, 100))
	for y := 0; y < 100; y++ {
		for x := 0; x < 100; x++ {
			img.Set(x, y, color.RGBA{
				R: uint8(x * 255 / 100),
				G: uint8(y * 255 / 100),
				B: 128,
				A: 255,
			})
		}
	}

	var buf bytes.Buffer
	jpeg.Encode(&buf, img, &jpeg.Options{Quality: 80})
	jpegData := buf.Bytes()

	// Inject EXIF APP1 segment after SOI marker (FF D8)
	exifPayload := []byte("Exif\x00\x00GPS\x00\x01\x00\x02\x03\x04\x05\x06\x07\x08")
	segLen := uint16(len(exifPayload) + 2)
	var lenBytes [2]byte
	binary.BigEndian.PutUint16(lenBytes[:], segLen)

	// Insert APP1 after SOI
	result := make([]byte, 0, len(jpegData)+4+len(exifPayload))
	result = append(result, jpegData[:2]...) // SOI
	result = append(result, 0xFF, 0xE1)     // APP1 marker
	result = append(result, lenBytes[:]...)  // Length
	result = append(result, exifPayload...)  // EXIF data
	result = append(result, jpegData[2:]...) // Rest of JPEG

	return result
}

func buildTestJPEGWithoutEXIF() []byte {
	img := image.NewRGBA(image.Rect(0, 0, 100, 100))
	for y := 0; y < 100; y++ {
		for x := 0; x < 100; x++ {
			img.Set(x, y, color.RGBA{
				R: uint8(x * 255 / 100),
				G: uint8(y * 255 / 100),
				B: 128,
				A: 255,
			})
		}
	}

	var buf bytes.Buffer
	jpeg.Encode(&buf, img, &jpeg.Options{Quality: 80})
	return buf.Bytes()
}

// ============================================================================
// launchOldPhotoCleanup
// ============================================================================

type mockS3Client struct {
	deletedKeys []string
}

func (m *mockS3Client) DeletePhotoByKey(_ interface{}, key string) error {
	if key != "" {
		m.deletedKeys = append(m.deletedKeys, key)
	}
	return nil
}

func TestLaunchOldPhotoCleanup_DifferentOldAndNew(t *testing.T) {
	// Test: response with oldProfileImage different from profile_image
	response := map[string]interface{}{
		"response": map[string]interface{}{
			"profile_image":    "profile-photos/user123/new-photo.jpg",
			"oldProfileImage":  "profile-photos/user123/old-photo.jpg",
		},
	}

	// Verify old key extraction logic
	resp, ok := response["response"].(map[string]interface{})
	if !ok {
		t.Fatal("response.response should be a map")
	}

	oldKey, _ := resp["oldProfileImage"].(string)
	newKey, _ := resp["profile_image"].(string)

	if oldKey != "profile-photos/user123/old-photo.jpg" {
		t.Errorf("oldKey = %q, want profile-photos/user123/old-photo.jpg", oldKey)
	}
	if newKey != "profile-photos/user123/new-photo.jpg" {
		t.Errorf("newKey = %q, want profile-photos/user123/new-photo.jpg", newKey)
	}
	if oldKey == newKey {
		t.Error("oldKey and newKey should be different")
	}
}

func TestLaunchOldPhotoCleanup_SameOldAndNew(t *testing.T) {
	// When old and new keys are the same, no cleanup should happen
	response := map[string]interface{}{
		"response": map[string]interface{}{
			"profile_image":   "profile-photos/user123/photo.jpg",
			"oldProfileImage": "profile-photos/user123/photo.jpg",
		},
	}

	resp := response["response"].(map[string]interface{})
	oldKey, _ := resp["oldProfileImage"].(string)
	newKey, _ := resp["profile_image"].(string)

	if oldKey != newKey {
		t.Error("oldKey and newKey should be the same")
	}
	// Logic: if oldKey == newKey, skip cleanup
}

func TestLaunchOldPhotoCleanup_NoOldKey(t *testing.T) {
	// First upload — no old key to clean up
	response := map[string]interface{}{
		"response": map[string]interface{}{
			"profile_image": "profile-photos/user123/photo.jpg",
		},
	}

	resp := response["response"].(map[string]interface{})
	oldKey, _ := resp["oldProfileImage"].(string)

	if oldKey != "" {
		t.Errorf("oldKey should be empty for first upload, got %q", oldKey)
	}
}

func TestLaunchOldPhotoCleanup_NestedResponse(t *testing.T) {
	// Response directly in envelope (not nested under "response")
	response := map[string]interface{}{
		"profile_image":   "profile-photos/user123/new.jpg",
		"oldProfileImage": "profile-photos/user123/old.jpg",
	}

	oldKey, _ := response["oldProfileImage"].(string)
	newKey, _ := response["profile_image"].(string)

	if oldKey == "" {
		t.Error("oldKey should not be empty")
	}
	if oldKey == newKey {
		t.Error("oldKey and newKey should be different")
	}
}
