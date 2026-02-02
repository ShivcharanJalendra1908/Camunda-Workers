package registry

import (
	"camunda-workers/internal/common/circuitbreaker"
	"camunda-workers/internal/common/config"
	"camunda-workers/internal/common/database"
	"camunda-workers/internal/common/idempotency"
	"camunda-workers/internal/common/logger"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sync"
)

// WorkerHandler defines the interface for all workers
type WorkerHandler interface {
	Execute(ctx context.Context, task *Task) (map[string]interface{}, error)
}

// Task represents a worker task
type Task struct {
	JobKey             int64
	ProcessInstanceKey int64
	BpmnProcessId      string
	ElementId          string
	Retries            int32
	Variables          map[string]interface{}
}

func LoadRegistry(path string) (*ActivityRegistry, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var reg ActivityRegistry
	err = json.Unmarshal(data, &reg)
	return &reg, err
}

// ResponseHandler interface for API response handling
type ResponseHandler interface {
	ReceiveWorkflowResponse(correlationKey string, response map[string]interface{}) error
}

// Dependencies contains all dependencies workers might need
type Dependencies struct {
	Logger          logger.Logger
	Config          *config.Config
	ESClient        *database.ElasticsearchClient
	PGClient        *database.PostgresClient
	RedisClient     *database.RedisClient
	ResponseHandler ResponseHandler
	CircuitBreaker  *circuitbreaker.Manager
	Idempotency     idempotency.Checker
}

var (
	workerRegistry = make(map[string]func(*Dependencies) (WorkerHandler, error))
	globalDeps     *Dependencies
	registryMutex  sync.RWMutex
)

// RegisterWorker registers a worker factory function
func RegisterWorker(workerType string, factory func(*Dependencies) (WorkerHandler, error)) {
	registryMutex.Lock()
	defer registryMutex.Unlock()
	workerRegistry[workerType] = factory
}

// GetWorkerHandler creates a worker handler instance
func GetWorkerHandler(workerType string) (WorkerHandler, error) {
	registryMutex.RLock()
	factory, exists := workerRegistry[workerType]
	registryMutex.RUnlock()

	if !exists {
		return nil, fmt.Errorf("worker type not registered: %s", workerType)
	}

	registryMutex.RLock()
	deps := globalDeps
	registryMutex.RUnlock()

	if deps == nil {
		return nil, fmt.Errorf("dependencies not initialized")
	}

	return factory(deps)
}

// SetGlobalDependencies sets the global dependencies
func SetGlobalDependencies(deps *Dependencies) {
	registryMutex.Lock()
	defer registryMutex.Unlock()
	globalDeps = deps
}

// GetGlobalDependencies returns the global dependencies
func GetGlobalDependencies() *Dependencies {
	registryMutex.RLock()
	defer registryMutex.RUnlock()
	return globalDeps
}

// GetRegisteredWorkers returns list of registered worker types
func GetRegisteredWorkers() []string {
	registryMutex.RLock()
	defer registryMutex.RUnlock()

	workers := make([]string, 0, len(workerRegistry))
	for workerType := range workerRegistry {
		workers = append(workers, workerType)
	}
	return workers
}

// // pkg/registry/registry.go
// package registry

// import (
// 	"encoding/json"
// 	"os"
// )

// func LoadRegistry(path string) (*ActivityRegistry, error) {
// 	data, err := os.ReadFile(path)
// 	if err != nil {
// 		return nil, err
// 	}
// 	var reg ActivityRegistry
// 	err = json.Unmarshal(data, &reg)
// 	return &reg, err
// }
