# AI-Powered Franchise Search Worker

## Overview
Natural language search worker that extracts structured parameters using lightweight LLM (Llama 3.2) and executes Elasticsearch queries.

## Features
- ✅ Natural language query understanding
- ✅ Parameter extraction (category, location, ROI, investment, etc.)
- ✅ Smart ROI range matching
- ✅ Elasticsearch query generation
- ✅ Production-ready error handling
- ✅ Lightweight & fast (Llama 3.2 - 1B parameters)

## Setup

### 1. Install Ollama
```bash
# Install Ollama
curl -fsSL https://ollama.com/install.sh | sh

# Start Ollama
ollama serve

# Pull model
ollama pull llama3.2
```

### 2. Run Worker
```bash
# Build
go build -o ai-search-worker cmd/worker-manager/main.go

# Run
export LLM_ENDPOINT=http://localhost:11434
export LLM_MODEL=llama3.2
./ai-search-worker
```

### 3. Test
```bash
# Start a process instance
curl -X POST http://localhost:8080/api/search \
  -H "Content-Type: application/json" \
  -d '{
    "query": "List of food franchises in bangalore with RoI of 8%"
  }'
```

## Example Queries

| Query | Extracted Parameters |
|-------|---------------------|
| "food franchises in bangalore with 8% ROI" | category: Food, location: Bangalore, roi: 7-9% |
| "education franchise under 10 lakhs in delhi" | category: Education, location: Delhi, investment: 0-1000000 |
| "verified fashion brands with 4+ rating" | category: Fashion, verified: true, rating: 4+ |

## Architecture
1. **Input**: Natural language query
2. **LLM Extraction**: Llama 3.2 extracts structured params (JSON)
3. **Query Building**: Converts params to Elasticsearch query
4. **Search**: Executes search with filters
5. **Output**: Ranked results with metadata

## Performance
- LLM Response: ~500ms (CPU), ~100ms (GPU)
- ES Search: ~50-200ms
- Total: ~600ms end-to-end

## Models
- **Default**: llama3.2 (1B params, fast)
- **Alternatives**: mistral (7B), phi-3 (3.8B)

## Configuration
See `configs/ai-search-config.yaml`