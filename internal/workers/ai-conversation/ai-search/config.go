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
	// ✅ READ OLLAMA_URL FROM ENVIRONMENT
	ollamaURL := os.Getenv("OLLAMA_URL")
	if ollamaURL == "" {
		ollamaURL = "http://ollama:11434" // Default for Docker
	}

	return &Config{
		WorkerID:       "ai-search-worker",
		TaskType:       "ai-search",
		MaxJobs:        50,
		PollInterval:   25 * time.Millisecond,
		RequestTimeout: 30 * time.Second,
		LLMProvider:    "ollama",
		// LLMModel:        "llama3.2",
		//LLMModel:        "tinyllama", // ✅ CHANGED
		// ✅ CHANGED: Qwen 2.5 instead of TinyLlama
		LLMModel:        "qwen2.5:1.5b",
		LLMEndpoint:     "http://ollama:11434",
		LLMTimeout:      3 * time.Second,
		LLMMaxTokens:    200,
		LLMTemperature:  0.0,
		IndexName:       "franchise_listings", // ✅ FIXED: Matches actual ES index
		SearchTimeout:   5 * time.Second,
		DefaultPageSize: 20,
		MaxQueryLength:  500,
	}
}
