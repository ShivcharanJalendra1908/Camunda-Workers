#!/bin/bash

echo "🚀 Setting up Ollama for AI Search Worker..."

# Check if Ollama is installed
if ! command -v ollama &> /dev/null; then
    echo "Installing Ollama..."
    curl -fsSL https://ollama.com/install.sh | sh
fi

# Start Ollama service
echo "Starting Ollama service..."
ollama serve &
sleep 5

# Pull Llama 3.2 model
echo "Pulling llama3.2 model..."
ollama pull llama3.2

# Test
echo "Testing Ollama..."
curl -X POST http://localhost:11434/api/generate -d '{
  "model": "llama3.2",
  "prompt": "Test: Extract {\"category\": \"Food\"}",
  "stream": false,
  "format": "json"
}'

echo ""
echo "✅ Ollama setup complete!"
echo "Models installed:"
ollama list