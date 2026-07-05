package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadDropdowns(t *testing.T) {
	// Create a temporary dropdowns.yaml for testing
	tmpDir := t.TempDir()
	content := `
dropdowns:
  theme:
    - label: "Light"
      value: "light"
    - label: "Dark"
      value: "dark"
  language:
    - label: "English"
      value: "en"
    - label: "Hindi"
      value: "hi"
`
	path := filepath.Join(tmpDir, "dropdowns.yaml")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("Failed to write test dropdowns.yaml: %v", err)
	}

	cfg, err := LoadDropdownsFromFile(path)
	if err != nil {
		t.Fatalf("LoadDropdownsFromFile failed: %v", err)
	}

	if len(cfg.Dropdowns) != 2 {
		t.Errorf("Expected 2 dropdowns, got %d", len(cfg.Dropdowns))
	}
}

func TestValidateValue(t *testing.T) {
	cfg := &DropdownConfig{
		Dropdowns: map[string][]DropdownOption{
			"theme": {
				{Label: "Light", Value: "light"},
				{Label: "Dark", Value: "dark"},
			},
		},
	}

	tests := []struct {
		name     string
		dropdown string
		value    string
		want     bool
	}{
		{"valid value", "theme", "light", true},
		{"invalid value", "theme", "blue", false},
		{"unknown dropdown", "unknown", "light", false},
		{"empty dropdown", "", "light", false},
		{"empty value", "theme", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := cfg.ValidateValue(tt.dropdown, tt.value)
			if got != tt.want {
				t.Errorf("ValidateValue(%q, %q) = %v, want %v", tt.dropdown, tt.value, got, tt.want)
			}
		})
	}
}

func TestGetOptions(t *testing.T) {
	cfg := &DropdownConfig{
		Dropdowns: map[string][]DropdownOption{
			"theme": {
				{Label: "Light", Value: "light"},
				{Label: "Dark", Value: "dark"},
			},
		},
	}

	opts := cfg.GetOptions("theme")
	if len(opts) != 2 {
		t.Errorf("Expected 2 options, got %d", len(opts))
	}

	opts = cfg.GetOptions("unknown")
	if opts != nil {
		t.Error("Expected nil for unknown dropdown")
	}
}

func TestGetLabel(t *testing.T) {
	cfg := &DropdownConfig{
		Dropdowns: map[string][]DropdownOption{
			"theme": {
				{Label: "Light", Value: "light"},
				{Label: "Dark", Value: "dark"},
			},
		},
	}

	label := cfg.GetLabel("theme", "light")
	if label != "Light" {
		t.Errorf("GetLabel = %q, want %q", label, "Light")
	}

	label = cfg.GetLabel("theme", "unknown")
	if label != "" {
		t.Errorf("GetLabel unknown = %q, want empty", label)
	}
}

func TestGetValues(t *testing.T) {
	cfg := &DropdownConfig{
		Dropdowns: map[string][]DropdownOption{
			"theme": {
				{Label: "Light", Value: "light"},
				{Label: "Dark", Value: "dark"},
			},
		},
	}

	values := cfg.GetValues("theme")
	if len(values) != 2 {
		t.Errorf("Expected 2 values, got %d", len(values))
	}
	if values[0] != "light" || values[1] != "dark" {
		t.Errorf("Values = %v, want [light dark]", values)
	}
}

func TestGetDropdownNames(t *testing.T) {
	cfg := &DropdownConfig{
		Dropdowns: map[string][]DropdownOption{
			"theme":    {{Label: "Light", Value: "light"}},
			"language": {{Label: "English", Value: "en"}},
		},
	}

	names := cfg.GetDropdownNames()
	if len(names) != 2 {
		t.Errorf("Expected 2 names, got %d", len(names))
	}
}

func TestValidateAll(t *testing.T) {
	valid := &DropdownConfig{
		Dropdowns: map[string][]DropdownOption{
			"theme": {{Label: "Light", Value: "light"}},
		},
	}
	if err := valid.ValidateAll(); err != nil {
		t.Errorf("ValidateAll valid: %v", err)
	}

	emptyValue := &DropdownConfig{
		Dropdowns: map[string][]DropdownOption{
			"theme": {{Label: "Light", Value: ""}},
		},
	}
	if err := emptyValue.ValidateAll(); err == nil {
		t.Error("ValidateAll empty value: expected error")
	}

	emptyLabel := &DropdownConfig{
		Dropdowns: map[string][]DropdownOption{
			"theme": {{Label: "", Value: "light"}},
		},
	}
	if err := emptyLabel.ValidateAll(); err == nil {
		t.Error("ValidateAll empty label: expected error")
	}
}

func TestFindDropdownByName(t *testing.T) {
	cfg := &DropdownConfig{
		Dropdowns: map[string][]DropdownOption{
			"theme": {{Label: "Light", Value: "light"}},
		},
	}

	opts, found := cfg.FindDropdownByName("theme")
	if !found || len(opts) != 1 {
		t.Error("FindDropdownByName exact match failed")
	}

	opts, found = cfg.FindDropdownByName("Theme")
	if !found || len(opts) != 1 {
		t.Error("FindDropdownByName case-insensitive failed")
	}

	opts, found = cfg.FindDropdownByName("unknown")
	if found {
		t.Error("FindDropdownByName unknown: expected not found")
	}
}

func TestNilDropdownConfig(t *testing.T) {
	var cfg *DropdownConfig

	if cfg.ValidateValue("theme", "light") {
		t.Error("Nil config ValidateValue should return false")
	}
	if opts := cfg.GetOptions("theme"); opts != nil {
		t.Error("Nil config GetOptions should return nil")
	}
	if label := cfg.GetLabel("theme", "light"); label != "" {
		t.Error("Nil config GetLabel should return empty")
	}
	if values := cfg.GetValues("theme"); values != nil {
		t.Error("Nil config GetValues should return nil")
	}
	if names := cfg.GetDropdownNames(); names != nil {
		t.Error("Nil config GetDropdownNames should return nil")
	}
	if _, found := cfg.FindDropdownByName("theme"); found {
		t.Error("Nil config FindDropdownByName should return false")
	}
	if err := cfg.ValidateAll(); err == nil {
		t.Error("Nil config ValidateAll should return error")
	}
}
