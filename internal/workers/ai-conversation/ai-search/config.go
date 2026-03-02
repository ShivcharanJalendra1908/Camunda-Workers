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
		RequestTimeout:  40 * time.Second,
		LLMProvider:     "ollama",
		//LLMModel:        "qwen2.5:0.5b",
		LLMModel: "qwen-franchise-extractor",
		LLMEndpoint:     ollamaURL,
		LLMTimeout:      35 * time.Second,
		LLMMaxTokens:    300,
		LLMTemperature:  0.0,
		IndexName:       "franchise_listings",
		SearchTimeout:   3 * time.Second,
		DefaultPageSize: 20,
		MaxQueryLength:  500,
	}
}
