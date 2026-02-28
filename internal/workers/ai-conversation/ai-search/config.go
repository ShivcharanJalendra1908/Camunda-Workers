// ============================================================
// FILE: internal/workers/ai-conversation/ai-search/config.go
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
	ollamaURL := os.Getenv("OLLAMA_URL")
	if ollamaURL == "" {
		ollamaURL = "http://ollama:11434"
	}

	return &Config{
		WorkerID:        "ai-search-worker",
		TaskType:        "ai-search",
		MaxJobs:         50,
		PollInterval:    25 * time.Millisecond,
		RequestTimeout:  30 * time.Second, // ✅ CHANGE: Was 30s - KEEP IT
		LLMProvider:     "ollama",
		LLMModel:        "qwen2.5:3b",
		LLMEndpoint:     "http://ollama:11434",
		LLMTimeout:      25 * time.Second, // ✅ CHANGE: 20s se 25s
		LLMMaxTokens:    80,               // ✅ CHANGE: 100 se 80
		LLMTemperature:  0.0,
		IndexName:       "franchise_listings",
		SearchTimeout:   3 * time.Second, // ✅ CHANGE: Was 3s - KEEP IT
		DefaultPageSize: 20,
		MaxQueryLength:  500,
	}
}

// func NewDefaultConfig() *Config {
// 	ollamaURL := os.Getenv("OLLAMA_URL")
// 	if ollamaURL == "" {
// 		ollamaURL = "http://ollama:11434" // Default for Docker
// 	}

// 	return &Config{
// 		WorkerID:        "ai-search-worker",
// 		TaskType:        "ai-search",
// 		MaxJobs:         50,
// 		PollInterval:    25 * time.Millisecond,
// 		RequestTimeout:  30 * time.Second,
// 		LLMProvider:     "ollama",
// 		LLMModel:        "qwen2.5:0.5b",
// 		LLMEndpoint:     "http://ollama:11434",
// 		LLMTimeout:      20 * time.Second,
// 		LLMMaxTokens:    100,
// 		LLMTemperature:  0.0,
// 		IndexName:       "franchise_listings",
// 		SearchTimeout:   3 * time.Second,
// 		DefaultPageSize: 20,
// 		MaxQueryLength:  500,
// 	}
// }
