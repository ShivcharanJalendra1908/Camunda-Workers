package aws

import (
	"strings"
	"testing"
)

func TestBuildPhotoURL(t *testing.T) {
	tests := []struct {
		name       string
		cdnBaseURL string
		key        string
		expected   string
	}{
		{
			name:       "normal key",
			cdnBaseURL: "https://d1234.cloudfront.net",
			key:        "profile-photos/user123/abc.jpg",
			expected:   "https://d1234.cloudfront.net/profile-photos/user123/abc.jpg",
		},
		{
			name:       "CDN URL with trailing slash",
			cdnBaseURL: "https://d1234.cloudfront.net/",
			key:        "profile-photos/user123/abc.jpg",
			expected:   "https://d1234.cloudfront.net/profile-photos/user123/abc.jpg",
		},
		{
			name:       "key with leading slash",
			cdnBaseURL: "https://d1234.cloudfront.net",
			key:        "/profile-photos/user123/abc.jpg",
			expected:   "https://d1234.cloudfront.net/profile-photos/user123/abc.jpg",
		},
		{
			name:       "CDN URL with trailing slash and key with leading slash",
			cdnBaseURL: "https://d1234.cloudfront.net/",
			key:        "/profile-photos/user123/abc.jpg",
			expected:   "https://d1234.cloudfront.net/profile-photos/user123/abc.jpg",
		},
		{
			name:       "empty key returns empty",
			cdnBaseURL: "https://d1234.cloudfront.net",
			key:        "",
			expected:   "",
		},
		{
			name:       "empty CDN returns empty",
			cdnBaseURL: "",
			key:        "profile-photos/user123/abc.jpg",
			expected:   "",
		},
		{
			name:       "both empty returns empty",
			cdnBaseURL: "",
			key:        "",
			expected:   "",
		},
		{
			name:       "deep key path",
			cdnBaseURL: "https://cdn.example.com",
			key:        "photos/2024/01/user123/avatar.jpg",
			expected:   "https://cdn.example.com/photos/2024/01/user123/avatar.jpg",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := BuildPhotoURL(tt.cdnBaseURL, tt.key)
			if got != tt.expected {
				t.Errorf("BuildPhotoURL(%q, %q) = %q, want %q", tt.cdnBaseURL, tt.key, got, tt.expected)
			}
		})
	}
}

func TestMakePhotoKey(t *testing.T) {
	tests := []struct {
		name       string
		userID     string
		ext        string
		wantPrefix string
		wantSuffix string
	}{
		{
			name:       "jpg extension",
			userID:     "user-123",
			ext:        "jpg",
			wantPrefix: "profile-photos/user-123/",
			wantSuffix: ".jpg",
		},
		{
			name:       "png extension",
			userID:     "user-456",
			ext:        "png",
			wantPrefix: "profile-photos/user-456/",
			wantSuffix: ".png",
		},
		{
			name:       "empty extension defaults to jpg",
			userID:     "user-789",
			ext:        "",
			wantPrefix: "profile-photos/user-789/",
			wantSuffix: ".jpg",
		},
		{
			name:       "webp extension",
			userID:     "user-abc",
			ext:        "webp",
			wantPrefix: "profile-photos/user-abc/",
			wantSuffix: ".webp",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := MakePhotoKey(tt.userID, tt.ext)
			if !strings.HasPrefix(got, tt.wantPrefix) {
				t.Errorf("MakePhotoKey(%q, %q) = %q, want prefix %q", tt.userID, tt.ext, got, tt.wantPrefix)
			}
			if !strings.HasSuffix(got, tt.wantSuffix) {
				t.Errorf("MakePhotoKey(%q, %q) = %q, want suffix %q", tt.userID, tt.ext, got, tt.wantSuffix)
			}
			// Should contain a UUID-like segment
			parts := strings.Split(got, "/")
			if len(parts) != 3 {
				t.Errorf("MakePhotoKey(%q, %q) = %q, want 3 path segments", tt.userID, tt.ext, got)
			}
		})
	}
}

func TestSanitizeContentType(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"jpg", "image/jpeg"},
		{"jpeg", "image/jpeg"},
		{"png", "image/png"},
		{"webp", "image/webp"},
		{"gif", "image/gif"},
		{"image/jpeg", "image/jpeg"},
		{"image/png", "image/png"},
		{"JPEG", "image/jpeg"},
		{"JPG", "image/jpeg"},
		{"", "image/jpeg"},        // empty defaults to jpeg
		{"bmp", "image/jpeg"},     // unknown defaults to jpeg
		{"IMAGE/JPEG", "image/jpeg"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := sanitizeContentType(tt.input)
			if got != tt.expected {
				t.Errorf("sanitizeContentType(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}

func TestKeyFromURL(t *testing.T) {
	client := &S3Client{bucket: "lemici-profile-photos"}

	tests := []struct {
		name     string
		url      string
		expected string
	}{
		{
			name:     "standard S3 URL",
			url:      "https://lemici-profile-photos.s3.amazonaws.com/profile-photos/user123/abc.jpg",
			expected: "profile-photos/user123/abc.jpg",
		},
		{
			name:     "S3 URL with query params",
			url:      "https://lemici-profile-photos.s3.amazonaws.com/profile-photos/user123/abc.jpg?X-Amz-Algorithm=AWS4",
			expected: "profile-photos/user123/abc.jpg",
		},
		{
			name:     "non-S3 URL returns empty",
			url:      "https://example.com/some/path",
			expected: "",
		},
		{
			name:     "empty URL returns empty",
			url:      "",
			expected: "",
		},
		{
			name:     "wrong bucket",
			url:      "https://other-bucket.s3.amazonaws.com/key.jpg",
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := client.keyFromURL(tt.url)
			if got != tt.expected {
				t.Errorf("keyFromURL(%q) = %q, want %q", tt.url, got, tt.expected)
			}
		})
	}
}

func TestDeletePhotoByKey_NilClient_EmptyKey(t *testing.T) {
	// Nil client field — checks s.client == nil before empty key check
	client := &S3Client{bucket: "test-bucket"}

	err := client.DeletePhotoByKey(nil, "")
	if err == nil {
		t.Error("DeletePhotoByKey with nil client should return error even for empty key")
	}
}

func TestDeletePhotoByKey_NilClient(t *testing.T) {
	client := &S3Client{}

	err := client.DeletePhotoByKey(nil, "some-key")
	if err == nil {
		t.Error("DeletePhotoByKey with nil client should return error")
	}
}
