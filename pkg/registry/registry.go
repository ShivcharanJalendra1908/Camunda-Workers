// pkg/registry/registry.go
package registry

import (
	"encoding/json"
	"os"
)

func LoadRegistry(path string) (*ActivityRegistry, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var reg ActivityRegistry
	err = json.Unmarshal(data, &reg)
	return &reg, err
}
