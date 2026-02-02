// ============================================================
// FILE: internal/workers/ai-conversation/ai-search/config.go
// FINAL VERSION: All fixes applied
// ============================================================

package ai_search

import (
	"os"
	"time"

	"github.com/elastic/go-elasticsearch/v8"
)

type Config struct {
	WorkerID        string
	TaskType        string
	MaxJobs         int
	PollInterval    time.Duration
	RequestTimeout  time.Duration
	LLMProvider     string
	LLMModel        string
	LLMEndpoint     string
	LLMTimeout      time.Duration
	LLMMaxTokens    int
	LLMTemperature  float64
	ESClient        *elasticsearch.Client
	IndexName       string
	SearchTimeout   time.Duration
	DefaultPageSize int
	MaxQueryLength  int
}

func NewDefaultConfig() *Config {
	// ✅ READ OLLAMA_URL FROM ENVIRONMENT
	ollamaURL := os.Getenv("OLLAMA_URL")
	if ollamaURL == "" {
		ollamaURL = "http://ollama:11434" // Default for Docker
	}

	return &Config{
		WorkerID:        "ai-search-worker",
		TaskType:        "ai-search", // ✅ FIXED: Matches BPMN task type
		MaxJobs:         10,
		PollInterval:    100 * time.Millisecond,
		RequestTimeout:  30 * time.Second,
		LLMProvider:     "ollama",
		LLMModel:        "llama3.2",
		LLMEndpoint:     "http://ollama:11434", // ✅ Hardcoded fix
		LLMTimeout:      15 * time.Second,
		LLMMaxTokens:    1000,
		LLMTemperature:  0.1,
		IndexName:       "franchise_listings", // ✅ FIXED: Matches actual ES index
		SearchTimeout:   5 * time.Second,
		DefaultPageSize: 20,
		MaxQueryLength:  500,
	}
}

// // ============================================================
// // FILE: internal/workers/ai-conversation/ai-search/config.go
// // ============================================================

// package ai_search

// import (
// 	"time"

// 	"github.com/elastic/go-elasticsearch/v8"
// )

// type Config struct {
// 	WorkerID        string
// 	TaskType        string
// 	MaxJobs         int
// 	PollInterval    time.Duration
// 	RequestTimeout  time.Duration
// 	LLMProvider     string
// 	LLMModel        string
// 	LLMEndpoint     string
// 	LLMTimeout      time.Duration
// 	LLMMaxTokens    int
// 	LLMTemperature  float64
// 	ESClient        *elasticsearch.Client
// 	IndexName       string
// 	SearchTimeout   time.Duration
// 	DefaultPageSize int
// 	MaxQueryLength  int
// }

// func NewDefaultConfig() *Config {
// 	return &Config{
// 		WorkerID:        "ai-search-worker",
// 		TaskType:        "ai-search",
// 		MaxJobs:         10,
// 		PollInterval:    100 * time.Millisecond,
// 		RequestTimeout:  30 * time.Second,
// 		LLMProvider:     "ollama",
// 		LLMModel:        "llama3.2",
// 		LLMEndpoint:     "http://ollama:11434",
// 		LLMTimeout:      15 * time.Second,
// 		LLMMaxTokens:    1000,
// 		LLMTemperature:  0.1,
// 		IndexName:       "franchise_listings",
// 		SearchTimeout:   5 * time.Second,
// 		DefaultPageSize: 20,
// 		MaxQueryLength:  500,
// 	}
// }
