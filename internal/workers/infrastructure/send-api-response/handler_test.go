package send_api_response

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"camunda-workers/internal/common/logger"
	"camunda-workers/pkg/registry"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

// MockResponseHandler for testing
type MockResponseHandler struct {
	mock.Mock
}

func (m *MockResponseHandler) ReceiveWorkflowResponse(correlationKey string, response map[string]interface{}) error {
	args := m.Called(correlationKey, response)
	return args.Error(0)
}

// TestLogger creates a simple logger for testing
type TestLogger struct {
	t *testing.T
}

func (tl *TestLogger) Debug(msg string, fields map[string]interface{}) {
	tl.t.Logf("[DEBUG] %s: %v", msg, fields)
}

func (tl *TestLogger) Info(msg string, fields map[string]interface{}) {
	tl.t.Logf("[INFO] %s: %v", msg, fields)
}

func (tl *TestLogger) Warn(msg string, fields map[string]interface{}) {
	tl.t.Logf("[WARN] %s: %v", msg, fields)
}

func (tl *TestLogger) Error(msg string, fields map[string]interface{}) {
	tl.t.Logf("[ERROR] %s: %v", msg, fields)
}

func (tl *TestLogger) Fatal(msg string, fields map[string]interface{}) {
	tl.t.Fatalf("[FATAL] %s: %v", msg, fields)
}

func (tl *TestLogger) With(fields map[string]interface{}) logger.Logger {
	return tl // Simple implementation for testing
}

func (tl *TestLogger) WithFields(fields map[string]interface{}) logger.Logger {
	return tl // Simple implementation for testing
}

// ✅ ADD THIS MISSING METHOD
func (tl *TestLogger) WithError(err error) logger.Logger {
	return tl.WithFields(map[string]interface{}{"error": err.Error()})
}

func (tl *TestLogger) Sync() error {
	return nil
}

func createTestHandler(t *testing.T, mockHandler *MockResponseHandler) *Handler {
	return NewHandler(&Config{Timeout: 5 * time.Second}, &TestLogger{t: t}, mockHandler)
}

func createTestInput(correlationKey string, response map[string]interface{}) *Input {
	return &Input{
		CorrelationKey: correlationKey,
		Response:       response,
	}
}

func TestHandler_ExecuteWorker_Success(t *testing.T) {
	mockHandler := &MockResponseHandler{}
	handler := createTestHandler(t, mockHandler)

	responseData := map[string]interface{}{
		"success": true,
		"data":    map[string]interface{}{"message": "Hello World"},
	}

	input := createTestInput("req-123", responseData)

	// Setup mock expectation
	mockHandler.On("ReceiveWorkflowResponse", "req-123", responseData).Return(nil)

	output, err := handler.ExecuteWorker(context.Background(), input)

	assert.NoError(t, err)
	assert.NotNil(t, output)
	assert.True(t, output.Success)
	assert.True(t, output.SentToAPI)
	assert.Empty(t, output.Error)
	assert.Empty(t, output.ApiError)

	mockHandler.AssertExpectations(t)
}

func TestHandler_ExecuteWorker_APIError(t *testing.T) {
	mockHandler := &MockResponseHandler{}
	handler := createTestHandler(t, mockHandler)

	input := createTestInput("req-456", map[string]interface{}{"test": "data"})

	// Setup mock to return error
	mockHandler.On("ReceiveWorkflowResponse", "req-456", mock.Anything).
		Return(errors.New("API gateway timeout"))

	output, err := handler.ExecuteWorker(context.Background(), input)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "API gateway error")
	assert.NotNil(t, output)
	assert.False(t, output.Success)
	assert.False(t, output.SentToAPI)
	assert.Contains(t, output.ApiError, "timeout")

	mockHandler.AssertExpectations(t)
}

func TestHandler_ExecuteWorker_NoHandler(t *testing.T) {
	// Handler without response handler
	handler := NewHandler(&Config{Timeout: 5 * time.Second}, &TestLogger{t: t}, nil)

	input := createTestInput("req-789", map[string]interface{}{"test": "data"})

	output, err := handler.ExecuteWorker(context.Background(), input)

	assert.NoError(t, err) // Should not error, just log warning
	assert.NotNil(t, output)
	assert.True(t, output.Success)
	assert.False(t, output.SentToAPI) // Not sent because no handler
}

func TestHandler_ValidateInput(t *testing.T) {
	mockHandler := &MockResponseHandler{}
	handler := createTestHandler(t, mockHandler)

	tests := []struct {
		name          string
		input         *Input
		expectedError string
	}{
		{
			name: "valid input",
			input: createTestInput("req-123",
				map[string]interface{}{"data": "test"}),
			expectedError: "",
		},
		{
			name: "missing correlation key",
			input: &Input{
				CorrelationKey: "",
				Response:       map[string]interface{}{},
			},
			expectedError: "correlationKey is required",
		},
		{
			name: "nil response",
			input: &Input{
				CorrelationKey: "req-123",
				Response:       nil,
			},
			expectedError: "response object is required",
		},
		{
			name: "empty but valid response",
			input: &Input{
				CorrelationKey: "req-123",
				Response:       map[string]interface{}{},
			},
			expectedError: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := handler.validateInput(tt.input)

			if tt.expectedError != "" {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tt.expectedError)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestJSONSerialization(t *testing.T) {
	output := &Output{
		Success:   true,
		SentToAPI: true,
		Error:     "",
		ApiError:  "",
	}

	jsonData, err := json.Marshal(output)
	assert.NoError(t, err)

	var decoded Output
	err = json.Unmarshal(jsonData, &decoded)
	assert.NoError(t, err)

	assert.Equal(t, output.Success, decoded.Success)
	assert.Equal(t, output.SentToAPI, decoded.SentToAPI)
}

func TestHandler_RegistryExecute(t *testing.T) {
	mockHandler := &MockResponseHandler{}
	handler := createTestHandler(t, mockHandler)

	responseData := map[string]interface{}{
		"success": true,
		"data":    "test",
	}

	// Create a registry Task
	task := &registry.Task{
		Variables: map[string]interface{}{
			"correlationKey": "test-correlation",
			"response":       responseData,
		},
	}

	// Setup mock expectation
	mockHandler.On("ReceiveWorkflowResponse", "test-correlation", responseData).Return(nil)

	result, err := handler.Execute(context.Background(), task)

	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.True(t, result["success"].(bool))
	assert.True(t, result["sentToApi"].(bool))

	mockHandler.AssertExpectations(t)
}

func TestHandler_RegistryExecute_MissingCorrelationKey(t *testing.T) {
	mockHandler := &MockResponseHandler{}
	handler := createTestHandler(t, mockHandler)

	// Create a registry Task without correlationKey
	task := &registry.Task{
		Variables: map[string]interface{}{
			"response": map[string]interface{}{"test": "data"},
		},
	}

	result, err := handler.Execute(context.Background(), task)

	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "correlationKey is required")
}
