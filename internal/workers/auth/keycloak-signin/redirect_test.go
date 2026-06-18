package keycloaksignin

import (
	"testing"
)

func TestIsAllowedRedirectDomain(t *testing.T) {
	allowed := []string{"lemici.com", "api-one.com", "api-two.com", "localhost"}

	tests := []struct {
		name    string
		rawURL  string
		allowed []string
		want    bool
	}{
		{
			name:    "exact domain match",
			rawURL:  "https://lemici.com/some/path",
			allowed: allowed,
			want:    true,
		},
		{
			name:    "subdomain match",
			rawURL:  "https://dev.lemici.com/franchises/123",
			allowed: allowed,
			want:    true,
		},
		{
			name:    "allowed api-one domain",
			rawURL:  "https://api-one.com/callback",
			allowed: allowed,
			want:    true,
		},
		{
			name:    "localhost with http allowed",
			rawURL:  "http://localhost:3000/callback",
			allowed: allowed,
			want:    true,
		},
		{
			name:    "evil subdomain bypass attempt",
			rawURL:  "https://evil-lemici.com/phish",
			allowed: allowed,
			want:    false,
		},
		{
			name:    "deeply nested evil subdomain",
			rawURL:  "https://lemici.com.evil.com/phish",
			allowed: allowed,
			want:    false,
		},
		{
			name:    "javascript scheme",
			rawURL:  "javascript:alert(1)",
			allowed: allowed,
			want:    false,
		},
		{
			name:    "data scheme",
			rawURL:  "data:text/html,<script>alert(1)</script>",
			allowed: allowed,
			want:    false,
		},
		{
			name:    "http scheme for non-localhost",
			rawURL:  "http://api-one.com/callback",
			allowed: allowed,
			want:    false,
		},
		{
			name:    "malformed URL",
			rawURL:  "not-a-url",
			allowed: allowed,
			want:    false,
		},
		{
			name:    "empty string",
			rawURL:  "",
			allowed: allowed,
			want:    false,
		},
		{
			name:    "userinfo prefix - Hostname() not fooled",
			rawURL:  "https://lemici.com@evil.com/callback",
			allowed: allowed,
			want:    false,
		},
		{
			name:    "empty allowlist",
			rawURL:  "https://lemici.com/",
			allowed: []string{},
			want:    false,
		},
		{
			name:    "nil allowlist",
			rawURL:  "https://lemici.com/",
			allowed: nil,
			want:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isAllowedRedirectDomain(tt.rawURL, tt.allowed)
			if got != tt.want {
				t.Errorf("isAllowedRedirectDomain(%q, %v) = %v, want %v", tt.rawURL, tt.allowed, got, tt.want)
			}
		})
	}
}
