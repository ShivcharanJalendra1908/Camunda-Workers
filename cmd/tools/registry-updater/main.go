// cmd/tools/registry-updater/main.go
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"camunda-workers/pkg/registry"
)

var registryPath string

func main() {
	addCmd := flag.NewFlagSet("add", flag.ExitOnError)
	updateCmd := flag.NewFlagSet("update", flag.ExitOnError)
	validateCmd := flag.NewFlagSet("validate", flag.ExitOnError)

	// Add command flags
	idAdd := addCmd.String("id", "", "Activity ID (e.g., validate-subscription)")
	displayName := addCmd.String("displayName", "", "Display Name (e.g., Validate Subscription)")
	description := addCmd.String("description", "", "Description")
	category := addCmd.String("category", "", "Category (e.g., infrastructure)")
	taskType := addCmd.String("taskType", "", "Camunda Task Type (e.g., validate-subscription)")
	version := addCmd.String("version", "1.0.0", "Version")
	implStatus := addCmd.String("status", "planned", "Implementation Status (planned, in-progress, completed, verified)")

	// Update command flags
	idUpdate := updateCmd.String("id", "", "Activity ID to update")
	field := updateCmd.String("field", "", "Field to update (status, version, etc.)")
	value := updateCmd.String("value", "", "New value for the field")

	// Validate command flags
	validateCmd.StringVar(&registryPath, "path", "configs/activity-registry.json", "Path to registry file")

	if len(os.Args) < 2 {
		help()
		os.Exit(1)
	}

	switch os.Args[1] {
	case "add":
		addCmd.Parse(os.Args[2:])
		if *idAdd == "" || *displayName == "" || *description == "" || *category == "" || *taskType == "" {
			fmt.Println("Error: id, displayName, description, category, and taskType are required for add.")
			addCmd.Usage()
			os.Exit(1)
		}
		activity := registry.Activity{
			ID:                   *idAdd,
			DisplayName:          *displayName,
			Description:          *description,
			Category:             *category,
			Version:              *version,
			TaskType:             *taskType,
			ImplementationStatus: *implStatus,
			InputSchema:          map[string]interface{}{},
			OutputSchema:         map[string]interface{}{},
			ErrorCodes:           []string{},
			Timeout:              "10s",
			Retries:              0,
			Workflows:            []string{},
			Tags:                 []string{},
		}
		err := addActivity(&activity)
		if err != nil {
			fmt.Printf("Error adding activity: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Added activity: %s\n", *idAdd)

	case "update":
		updateCmd.Parse(os.Args[2:])
		if *idUpdate == "" || *field == "" || *value == "" {
			fmt.Println("Error: id, field, and value are required for update.")
			updateCmd.Usage()
			os.Exit(1)
		}
		err := updateActivity(*idUpdate, *field, *value)
		if err != nil {
			fmt.Printf("Error updating activity: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Updated activity %s, field %s to %s\n", *idUpdate, *field, *value)

	case "validate":
		validateCmd.Parse(os.Args[2:])
		err := validateRegistry()
		if err != nil {
			fmt.Printf("Registry validation failed: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("Registry validation passed.")

	case "help":
		fallthrough
	default:
		help()
	}
}

func addActivity(activity *registry.Activity) error {
	reg, err := registry.LoadRegistry(registryPath)
	if err != nil {
		// If file doesn't exist, create new registry
		if os.IsNotExist(err) {
			reg = &registry.ActivityRegistry{
				Version:     "1.0.0",
				LastUpdated: time.Now().Format(time.RFC3339),
				Activities:  []registry.Activity{},
			}
		} else {
			return fmt.Errorf("failed to load registry: %w", err)
		}
	}

	// Check if activity already exists
	for _, existing := range reg.Activities {
		if existing.ID == activity.ID {
			return fmt.Errorf("activity with ID %s already exists", activity.ID)
		}
	}

	// Add new activity
	reg.Activities = append(reg.Activities, *activity)
	reg.LastUpdated = time.Now().Format(time.RFC3339)

	// Save registry
	return saveRegistry(reg, registryPath)
}

func updateActivity(id, field, value string) error {
	reg, err := registry.LoadRegistry(registryPath)
	if err != nil {
		return fmt.Errorf("failed to load registry: %w", err)
	}

	found := false
	for i := range reg.Activities {
		if reg.Activities[i].ID == id {
			found = true
			switch field {
			case "status":
				reg.Activities[i].ImplementationStatus = value
			case "version":
				reg.Activities[i].Version = value
			case "displayName":
				reg.Activities[i].DisplayName = value
			case "description":
				reg.Activities[i].Description = value
			case "category":
				reg.Activities[i].Category = value
			case "taskType":
				reg.Activities[i].TaskType = value
			case "timeout":
				reg.Activities[i].Timeout = value
			case "retries":
				retries, err := strconv.Atoi(value)
				if err != nil {
					return fmt.Errorf("invalid retries value: %w", err)
				}
				reg.Activities[i].Retries = retries
			default:
				return fmt.Errorf("unknown field: %s", field)
			}
			break
		}
	}

	if !found {
		return fmt.Errorf("activity with ID %s not found", id)
	}

	reg.LastUpdated = time.Now().Format(time.RFC3339)
	return saveRegistry(reg, registryPath)
}

func validateRegistry() error {
	reg, err := registry.LoadRegistry(registryPath)
	if err != nil {
		return fmt.Errorf("failed to load registry: %w", err)
	}

	if len(reg.Activities) == 0 {
		return fmt.Errorf("registry contains no activities")
	}

	ids := make(map[string]bool)
	for _, activity := range reg.Activities {
		if ids[activity.ID] {
			return fmt.Errorf("duplicate activity ID: %s", activity.ID)
		}
		ids[activity.ID] = true

		if activity.ID == "" {
			return fmt.Errorf("activity missing required field: ID")
		}
		if activity.DisplayName == "" {
			return fmt.Errorf("activity %s missing required field: DisplayName", activity.ID)
		}
		if activity.TaskType == "" {
			return fmt.Errorf("activity %s missing required field: TaskType", activity.ID)
		}
		if activity.Category == "" {
			return fmt.Errorf("activity %s missing required field: Category", activity.ID)
		}
	}

	fmt.Printf("Registry validation passed. Found %d activities.\n", len(reg.Activities))
	return nil
}

// saveRegistry handles saving the registry to file
func saveRegistry(reg *registry.ActivityRegistry, path string) error {
	data, err := json.MarshalIndent(reg, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal registry: %w", err)
	}

	// Create directory if it doesn't exist
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create directory: %w", err)
	}

	err = os.WriteFile(path, data, 0644)
	if err != nil {
		return fmt.Errorf("failed to write registry file: %w", err)
	}

	return nil
}

func help() {
	fmt.Println(`
Usage: registry-updater <command> [flags]

Commands:
  add     Add a new activity to the registry
  update  Update an existing activity's field
  validate Validate the registry file
  help    Show this help message

Examples:
  registry-updater add -id validate-subscription -displayName "Validate Subscription" -description "Validates user subscription status" -category infrastructure -taskType validate-subscription
  registry-updater update -id validate-subscription -field status -value completed
  registry-updater validate -path configs/activity-registry.json

Use 'registry-updater <command> -h' for more information about a command.
`)
}
