package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/viper"
)

// DropdownOption represents a single option in a dropdown list.
type DropdownOption struct {
	Label string `mapstructure:"label" yaml:"label"`
	Value string `mapstructure:"value" yaml:"value"`
}

// PlanInfo holds the label and entitlements for a subscription plan.
type PlanInfo struct {
	Label        string   `mapstructure:"label" yaml:"label"`
	Entitlements []string `mapstructure:"entitlements" yaml:"entitlements"`
}

// DropdownConfig holds all dropdown configurations.
type DropdownConfig struct {
	Dropdowns       map[string][]DropdownOption  `mapstructure:"dropdowns" yaml:"dropdowns"`
	PlanEntitlements map[string]PlanInfo         `mapstructure:"plan_entitlements" yaml:"plan_entitlements"`
}

// LoadDropdowns loads dropdown configuration from config/dropdowns.yaml.
func LoadDropdowns() (*DropdownConfig, error) {
	v := viper.New()
	v.SetConfigName("dropdowns")
	v.SetConfigType("yaml")

	// Search in multiple locations
	possiblePaths := []string{
		"./config",
		"../../config",
		"../config",
		".",
		"../..",
	}

	for _, p := range possiblePaths {
		v.AddConfigPath(p)
	}

	if err := v.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); ok {
			return nil, fmt.Errorf("dropdowns.yaml not found in any of the search paths")
		}
		return nil, fmt.Errorf("failed to read dropdowns.yaml: %w", err)
	}

	var cfg DropdownConfig
	if err := v.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("failed to unmarshal dropdowns.yaml: %w", err)
	}

	return &cfg, nil
}

// LoadDropdownsFromFile loads dropdown configuration from a specific file path.
func LoadDropdownsFromFile(path string) (*DropdownConfig, error) {
	v := viper.New()
	v.SetConfigFile(path)
	v.SetConfigType("yaml")

	if err := v.ReadInConfig(); err != nil {
		return nil, fmt.Errorf("failed to read dropdowns file %s: %w", path, err)
	}

	var cfg DropdownConfig
	if err := v.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("failed to unmarshal dropdowns: %w", err)
	}

	return &cfg, nil
}

// ValidateValue checks if a given value is valid for the specified dropdown.
func (d *DropdownConfig) ValidateValue(dropdownName, value string) bool {
	if d == nil || d.Dropdowns == nil {
		return false
	}
	options, exists := d.Dropdowns[dropdownName]
	if !exists {
		return false
	}
	for _, opt := range options {
		if opt.Value == value {
			return true
		}
	}
	return false
}

// GetOptions returns all options for the specified dropdown.
func (d *DropdownConfig) GetOptions(dropdownName string) []DropdownOption {
	if d == nil || d.Dropdowns == nil {
		return nil
	}
	return d.Dropdowns[dropdownName]
}

// GetLabel returns the display label for a given dropdown value.
func (d *DropdownConfig) GetLabel(dropdownName, value string) string {
	if d == nil || d.Dropdowns == nil {
		return ""
	}
	options, exists := d.Dropdowns[dropdownName]
	if !exists {
		return ""
	}
	for _, opt := range options {
		if opt.Value == value {
			return opt.Label
		}
	}
	return ""
}

// GetValues returns all valid values for the specified dropdown.
func (d *DropdownConfig) GetValues(dropdownName string) []string {
	if d == nil || d.Dropdowns == nil {
		return nil
	}
	options, exists := d.Dropdowns[dropdownName]
	if !exists {
		return nil
	}
	values := make([]string, len(options))
	for i, opt := range options {
		values[i] = opt.Value
	}
	return values
}

// GetDropdownNames returns the names of all configured dropdowns.
func (d *DropdownConfig) GetDropdownNames() []string {
	if d == nil || d.Dropdowns == nil {
		return nil
	}
	names := make([]string, 0, len(d.Dropdowns))
	for name := range d.Dropdowns {
		names = append(names, name)
	}
	return names
}

// ValidateAll checks all configured dropdowns have at least one option.
func (d *DropdownConfig) ValidateAll() error {
	if d == nil || d.Dropdowns == nil {
		return fmt.Errorf("dropdowns config is nil")
	}
	for name, options := range d.Dropdowns {
		if len(options) == 0 {
			return fmt.Errorf("dropdown %q has no options", name)
		}
		for _, opt := range options {
			if opt.Value == "" {
				return fmt.Errorf("dropdown %q has option with empty value (label: %q)", name, opt.Label)
			}
			if opt.Label == "" {
				return fmt.Errorf("dropdown %q has option with empty label (value: %q)", name, opt.Value)
			}
		}
	}
	return nil
}

// FindDropdownByName performs case-insensitive lookup for a dropdown.
func (d *DropdownConfig) FindDropdownByName(name string) ([]DropdownOption, bool) {
	if d == nil || d.Dropdowns == nil {
		return nil, false
	}
	// Exact match first
	if opts, exists := d.Dropdowns[name]; exists {
		return opts, true
	}
	// Case-insensitive match
	lower := strings.ToLower(name)
	for dropdownName, opts := range d.Dropdowns {
		if strings.ToLower(dropdownName) == lower {
			return opts, true
		}
	}
	return nil, false
}

// GetPlanEntitlements returns the entitlements for a given subscription tier.
// Returns the entitlements list and true if found, nil and false otherwise.
func (d *DropdownConfig) GetPlanEntitlements(tier string) ([]string, bool) {
	if d == nil || d.PlanEntitlements == nil {
		return nil, false
	}
	// Exact match first
	if plan, exists := d.PlanEntitlements[tier]; exists {
		return plan.Entitlements, true
	}
	// Case-insensitive match
	lower := strings.ToLower(tier)
	for name, plan := range d.PlanEntitlements {
		if strings.ToLower(name) == lower {
			return plan.Entitlements, true
		}
	}
	return nil, false
}

// GetPlanLabel returns the display label for a given subscription tier.
func (d *DropdownConfig) GetPlanLabel(tier string) string {
	if d == nil || d.PlanEntitlements == nil {
		return tier
	}
	if plan, exists := d.PlanEntitlements[tier]; exists {
		return plan.Label
	}
	lower := strings.ToLower(tier)
	for name, plan := range d.PlanEntitlements {
		if strings.ToLower(name) == lower {
			return plan.Label
		}
	}
	return tier
}

// GetDropdownFilePath returns the path to dropdowns.yaml.
func GetDropdownFilePath() string {
	possiblePaths := []string{
		"./config/dropdowns.yaml",
		"../../config/dropdowns.yaml",
		"../config/dropdowns.yaml",
		"./dropdowns.yaml",
	}
	for _, p := range possiblePaths {
		if _, err := os.Stat(p); err == nil {
			abs, _ := filepath.Abs(p)
			return abs
		}
	}
	return "config/dropdowns.yaml"
}
