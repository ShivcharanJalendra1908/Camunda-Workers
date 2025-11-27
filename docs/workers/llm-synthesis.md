
---

## 📁 **17. `docs/workers/llm-synthesis.md`**

```markdown
# LLM Synthesis Worker

## Purpose
Synthesizes final AI response using LLM.

## Task Type
`llm-synthesis`

## Input Schema
```json
{
  "question": "string",
  "internalData": "object",
  "webData": "object",
  "intent": "object"
}

## Output Schema
```json
{
  "llmResponse": "string (generated answer)",
  "confidence": "float",
  "sources": "array[string]"
}