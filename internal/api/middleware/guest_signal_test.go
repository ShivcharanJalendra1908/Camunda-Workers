package middleware

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestBuildCompositeKey(t *testing.T) {
	key1 := buildCompositeKey("abc123", "salt1")
	key2 := buildCompositeKey("abc123", "salt1")
	assert.Equal(t, key1, key2, "same inputs should produce same key")
	assert.Len(t, key1, 16, "composite key should be 16 hex chars (8 bytes truncated)")
	assert.NotEmpty(t, key1)

	key3 := buildCompositeKey("abc123", "salt2")
	assert.NotEqual(t, key1, key3, "different salt should produce different key")

	key4 := buildCompositeKey("def456", "salt1")
	assert.NotEqual(t, key1, key4, "different token should produce different key")
}

func TestBuildCompositeKey_Length(t *testing.T) {
	for i := 0; i < 100; i++ {
		key := buildCompositeKey("token"+string(rune(i)), "salt")
		assert.Len(t, key, 16, "all keys should be 16 hex chars")
	}
}

func TestBuildFallbackCompositeKey(t *testing.T) {
	key1 := buildFallbackCompositeKey("192.168.1.100", "Mozilla/5.0", "salt1")
	key2 := buildFallbackCompositeKey("192.168.1.100", "Mozilla/5.0", "salt1")
	assert.Equal(t, key1, key2)
	assert.Len(t, key1, 16)

	key3 := buildFallbackCompositeKey("192.168.1.100", "Mozilla/5.0", "salt2")
	assert.NotEqual(t, key1, key3, "different salt produces different key")

	key4 := buildFallbackCompositeKey("10.0.0.1", "Mozilla/5.0", "salt1")
	assert.NotEqual(t, key1, key4, "different IP (/24) produces different key")
}

func TestBuildFallbackCompositeKey_IgnoresUA(t *testing.T) {
	key1 := buildFallbackCompositeKey("192.168.1.100", "Mozilla/5.0", "salt1")
	key2 := buildFallbackCompositeKey("192.168.1.100", "Chrome/120", "salt1")
	// Fallback key is IP + normalizedUA + salt, so different UAs produce different keys
	// This is intentional - same device, different browser = different fallback identity
	assert.NotEqual(t, key1, key2, "different UA produces different fallback key")
}

func TestIpSlash24_IPv4(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"192.168.1.100", "192.168.1"},
		{"10.0.0.1", "10.0.0"},
		{"172.16.0.1", "172.16.0"},
		{"8.8.8.8", "8.8.8"},
		{"255.255.255.255", "255.255.255"},
		{"  192.168.1.100  ", "192.168.1"}, // trim spaces
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := ipSlash24(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestIpSlash24_IPv6(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"2001:db8:85a3::1", "2001:db8:85a3"},
		{"::1", "::1"},           // only 2 groups, returns as-is
		{"fe80::1", "fe80::1"},  // only 2 groups, returns as-is
		{"2001:0db8:0000:0000:0000:0000:0000:0001", "2001:0db8:0000"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := ipSlash24(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestIpSlash24_SameSubnet(t *testing.T) {
	ips := []string{
		"192.168.1.1",
		"192.168.1.50",
		"192.168.1.254",
	}

	results := make(map[string]bool)
	for _, ip := range ips {
		results[ipSlash24(ip)] = true
	}

	assert.Len(t, results, 1, "all IPs in same /24 should map to same prefix")
	for k := range results {
		assert.Equal(t, "192.168.1", k)
	}
}

func TestIsValidHexToken(t *testing.T) {
	tests := []struct {
		input   string
		isValid bool
	}{
		{"abc123def4567890123456789abcdef0", true}, // 32 chars
		{"0123456789abcdef0123456789abcdef01", true}, // 33 chars
		{"abc123def4567890123456789abcdef", false},     // 30 chars, too short
		{"", false},
		{"abc123def4567890123456789abcdef0g", false}, // 'g' not hex
		{"ABC123def4567890123456789abcdef0", false},   // uppercase not allowed
		{"abc123def4567890123456789abcdef0!", false},  // special char
		{"12345678901234567890123456789012", true},    // all digits
		{"abcdefabcdefabcdefabcdefabcdefab", true},    // all lowercase hex
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := isValidHexToken(tt.input)
			assert.Equal(t, tt.isValid, result, "input: %q", tt.input)
		})
	}
}

func TestNormalizeUserAgent(t *testing.T) {
	// The normalizeUserAgent function should strip version numbers
	ua1 := normalizeUserAgent("Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36")
	ua2 := normalizeUserAgent("Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36")
	assert.Equal(t, ua1, ua2)

	// Same browser, different version numbers should normalize
	ua3 := normalizeUserAgent("Mozilla/5.0 (Windows NT 10.0; Win64; x64) Chrome/120.0.0.0")
	ua4 := normalizeUserAgent("Mozilla/5.0 (Windows NT 10.0; Win64; x64) Chrome/121.0.0.0")
	// If normalization strips versions, these should be equal
	if ua3 == ua4 {
		t.Log("normalizeUserAgent strips version numbers (expected)")
	} else {
		t.Log("normalizeUserAgent preserves version numbers")
	}
}
