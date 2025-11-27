// internal/workers/application/create-application-record/handler_test.go
package createapplicationrecord

import (
	"context"
	"errors"
	"testing"
	"time"

	"camunda-workers/internal/common/logger"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
)

// ==========================
// Test Helper Functions
// ==========================

func createTestConfig() *Config {
	return &Config{}
}

func createTestInput() *Input {
	return &Input{
		SeekerID:    "seeker-001",
		FranchiseID: "franchise-001",
		ApplicationData: map[string]interface{}{
			"name":     "John Doe",
			"email":    "john@example.com",
			"location": "New York",
		},
		ReadinessScore: 85,
		Priority:       "high",
	}
}

// Create a test logger that implements your logger.Logger interface
type testLogger struct {
	t *testing.T
}

func (tl *testLogger) Debug(msg string, fields map[string]interface{}) {
	tl.t.Logf("DEBUG: %s %v", msg, fields)
}

func (tl *testLogger) Info(msg string, fields map[string]interface{}) {
	tl.t.Logf("INFO: %s %v", msg, fields)
}

func (tl *testLogger) Warn(msg string, fields map[string]interface{}) {
	tl.t.Logf("WARN: %s %v", msg, fields)
}

func (tl *testLogger) Error(msg string, fields map[string]interface{}) {
	tl.t.Logf("ERROR: %s %v", msg, fields)
}

func (tl *testLogger) WithFields(fields map[string]interface{}) logger.Logger {
	return tl // Simple implementation for testing
}

func (tl *testLogger) WithError(err error) logger.Logger {
	return tl.WithFields(map[string]interface{}{"error": err})
}

func (t *testLogger) With(fields map[string]interface{}) logger.Logger {
	return t
}

func newTestLogger(t *testing.T) logger.Logger {
	return &testLogger{t: t}
}

// ==========================
// Core Functionality Tests
// ==========================

func TestHandler_Execute_Success(t *testing.T) {
	db, mock, err := sqlmock.New()
	assert.NoError(t, err)
	defer db.Close()

	// Mock duplicate check - no existing application
	mock.ExpectQuery(`SELECT EXISTS`).
		WithArgs("seeker-001", "franchise-001").
		WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(false))

	// Mock application insert
	mock.ExpectExec(`INSERT INTO applications`).
		WithArgs(
			sqlmock.AnyArg(), // application ID (UUID)
			"seeker-001",
			"franchise-001",
			sqlmock.AnyArg(), // JSON bytes
			85,
			"high",
			"submitted",
			sqlmock.AnyArg(), // created_at
		).
		WillReturnResult(sqlmock.NewResult(1, 1))

	// Mock audit log insert
	mock.ExpectExec(`INSERT INTO audit_log`).
		WithArgs(
			"application_created",
			"application",
			sqlmock.AnyArg(),
			sqlmock.AnyArg(),
			sqlmock.AnyArg(),
		).
		WillReturnResult(sqlmock.NewResult(1, 1))

	config := createTestConfig()
	handler := NewHandler(config, db, newTestLogger(t))

	input := createTestInput()
	output, err := handler.Execute(context.Background(), input)

	assert.NoError(t, err)
	assert.NotNil(t, output)
	assert.NotEmpty(t, output.ApplicationID)
	assert.Equal(t, "submitted", output.ApplicationStatus)
	assert.NotEmpty(t, output.CreatedAt)

	// Verify timestamp format
	_, err = time.Parse(time.RFC3339, output.CreatedAt)
	assert.NoError(t, err)

	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestHandler_Execute_DuplicateApplication(t *testing.T) {
	db, mock, err := sqlmock.New()
	assert.NoError(t, err)
	defer db.Close()

	// Mock duplicate check - application already exists
	mock.ExpectQuery(`SELECT EXISTS`).
		WithArgs("seeker-001", "franchise-001").
		WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(true))

	config := createTestConfig()
	handler := NewHandler(config, db, newTestLogger(t))

	input := createTestInput()
	output, err := handler.Execute(context.Background(), input)

	assert.Error(t, err)
	assert.True(t, errors.Is(err, ErrDuplicateApplication))
	assert.Contains(t, err.Error(), "application already exists")
	assert.Nil(t, output)

	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestHandler_Execute_DuplicateCheckError(t *testing.T) {
	db, mock, err := sqlmock.New()
	assert.NoError(t, err)
	defer db.Close()

	// Mock duplicate check error
	mock.ExpectQuery(`SELECT EXISTS`).
		WithArgs("seeker-001", "franchise-001").
		WillReturnError(errors.New("database connection failed"))

	config := createTestConfig()
	handler := NewHandler(config, db, newTestLogger(t))

	input := createTestInput()
	output, err := handler.Execute(context.Background(), input)

	assert.Error(t, err)
	assert.True(t, errors.Is(err, ErrDatabaseInsertFailed))
	assert.Contains(t, err.Error(), "duplicate check failed")
	assert.Nil(t, output)

	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestHandler_Execute_InsertError(t *testing.T) {
	db, mock, err := sqlmock.New()
	assert.NoError(t, err)
	defer db.Close()

	// Mock duplicate check - no existing application
	mock.ExpectQuery(`SELECT EXISTS`).
		WithArgs("seeker-001", "franchise-001").
		WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(false))

	// Mock application insert error
	mock.ExpectExec(`INSERT INTO applications`).
		WithArgs(
			sqlmock.AnyArg(),
			"seeker-001",
			"franchise-001",
			sqlmock.AnyArg(),
			85,
			"high",
			"submitted",
			sqlmock.AnyArg(),
		).
		WillReturnError(errors.New("insert failed"))

	config := createTestConfig()
	handler := NewHandler(config, db, newTestLogger(t))

	input := createTestInput()
	output, err := handler.Execute(context.Background(), input)

	assert.Error(t, err)
	assert.True(t, errors.Is(err, ErrDatabaseInsertFailed))
	assert.Contains(t, err.Error(), "insert failed")
	assert.Nil(t, output)

	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestHandler_Execute_AuditLogError(t *testing.T) {
	db, mock, err := sqlmock.New()
	assert.NoError(t, err)
	defer db.Close()

	// Mock duplicate check - no existing application
	mock.ExpectQuery(`SELECT EXISTS`).
		WithArgs("seeker-001", "franchise-001").
		WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(false))

	// Mock application insert success
	mock.ExpectExec(`INSERT INTO applications`).
		WithArgs(
			sqlmock.AnyArg(),
			"seeker-001",
			"franchise-001",
			sqlmock.AnyArg(),
			85,
			"high",
			"submitted",
			sqlmock.AnyArg(),
		).
		WillReturnResult(sqlmock.NewResult(1, 1))

	// Mock audit log insert error
	mock.ExpectExec(`INSERT INTO audit_log`).
		WithArgs(
			"application_created",
			"application",
			sqlmock.AnyArg(),
			sqlmock.AnyArg(),
			sqlmock.AnyArg(),
		).
		WillReturnError(errors.New("audit log failed"))

	config := createTestConfig()
	handler := NewHandler(config, db, newTestLogger(t))

	input := createTestInput()
	output, err := handler.Execute(context.Background(), input)

	// Should still succeed even if audit log fails
	assert.NoError(t, err)
	assert.NotNil(t, output)
	assert.NotEmpty(t, output.ApplicationID)
	assert.Equal(t, "submitted", output.ApplicationStatus)

	assert.NoError(t, mock.ExpectationsWereMet())
}

// ==========================
// Unit Tests
// ==========================

func TestHandler_Execute_MinimalInput(t *testing.T) {
	db, mock, err := sqlmock.New()
	assert.NoError(t, err)
	defer db.Close()

	// Mock duplicate check - no existing application
	mock.ExpectQuery(`SELECT EXISTS`).
		WithArgs("seeker-002", "franchise-002").
		WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(false))

	// Mock application insert
	mock.ExpectExec(`INSERT INTO applications`).
		WithArgs(
			sqlmock.AnyArg(),
			"seeker-002",
			"franchise-002",
			sqlmock.AnyArg(), // empty map will be JSON: {}
			0,
			"",
			"submitted",
			sqlmock.AnyArg(),
		).
		WillReturnResult(sqlmock.NewResult(1, 1))

	// Mock audit log insert
	mock.ExpectExec(`INSERT INTO audit_log`).
		WithArgs(
			"application_created",
			"application",
			sqlmock.AnyArg(),
			sqlmock.AnyArg(),
			sqlmock.AnyArg(),
		).
		WillReturnResult(sqlmock.NewResult(1, 1))

	config := createTestConfig()
	handler := NewHandler(config, db, newTestLogger(t))

	input := &Input{
		SeekerID:        "seeker-002",
		FranchiseID:     "franchise-002",
		ApplicationData: map[string]interface{}{},
		ReadinessScore:  0,
		Priority:        "",
	}

	output, err := handler.Execute(context.Background(), input)

	assert.NoError(t, err)
	assert.NotNil(t, output)
	assert.NotEmpty(t, output.ApplicationID)
	assert.Equal(t, "submitted", output.ApplicationStatus)

	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestHandler_Execute_ComplexApplicationData(t *testing.T) {
	db, mock, err := sqlmock.New()
	assert.NoError(t, err)
	defer db.Close()

	complexData := map[string]interface{}{
		"personalInfo": map[string]interface{}{
			"firstName": "John",
			"lastName":  "Doe",
			"age":       35,
			"address": map[string]interface{}{
				"street":  "123 Main St",
				"city":    "New York",
				"state":   "NY",
				"zipCode": "10001",
			},
		},
		"financialInfo": map[string]interface{}{
			"liquidCapital": 500000,
			"netWorth":      1000000,
			"creditScore":   750,
		},
		"experience": []interface{}{
			"management",
			"retail",
			"customer_service",
		},
	}

	// Mock duplicate check
	mock.ExpectQuery(`SELECT EXISTS`).
		WithArgs("seeker-003", "franchise-003").
		WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(false))

	// Mock application insert
	mock.ExpectExec(`INSERT INTO applications`).
		WithArgs(
			sqlmock.AnyArg(),
			"seeker-003",
			"franchise-003",
			sqlmock.AnyArg(),
			90,
			"high",
			"submitted",
			sqlmock.AnyArg(),
		).
		WillReturnResult(sqlmock.NewResult(1, 1))

	// Mock audit log insert
	mock.ExpectExec(`INSERT INTO audit_log`).
		WithArgs(
			"application_created",
			"application",
			sqlmock.AnyArg(),
			sqlmock.AnyArg(),
			sqlmock.AnyArg(),
		).
		WillReturnResult(sqlmock.NewResult(1, 1))

	config := createTestConfig()
	handler := NewHandler(config, db, newTestLogger(t))

	input := &Input{
		SeekerID:        "seeker-003",
		FranchiseID:     "franchise-003",
		ApplicationData: complexData,
		ReadinessScore:  90,
		Priority:        "high",
	}

	output, err := handler.Execute(context.Background(), input)

	assert.NoError(t, err)
	assert.NotNil(t, output)
	assert.NotEmpty(t, output.ApplicationID)

	assert.NoError(t, mock.ExpectationsWereMet())
}

// ==========================
// Edge Cases
// ==========================

func TestHandler_Execute_NilApplicationData(t *testing.T) {
	db, mock, err := sqlmock.New()
	assert.NoError(t, err)
	defer db.Close()

	// Mock duplicate check
	mock.ExpectQuery(`SELECT EXISTS`).
		WithArgs("seeker-004", "franchise-004").
		WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(false))

	// Mock application insert with nil application data
	mock.ExpectExec(`INSERT INTO applications`).
		WithArgs(
			sqlmock.AnyArg(),
			"seeker-004",
			"franchise-004",
			sqlmock.AnyArg(), // nil will be JSON: null
			75,
			"medium",
			"submitted",
			sqlmock.AnyArg(),
		).
		WillReturnResult(sqlmock.NewResult(1, 1))

	// Mock audit log insert
	mock.ExpectExec(`INSERT INTO audit_log`).
		WithArgs(
			"application_created",
			"application",
			sqlmock.AnyArg(),
			sqlmock.AnyArg(),
			sqlmock.AnyArg(),
		).
		WillReturnResult(sqlmock.NewResult(1, 1))

	config := createTestConfig()
	handler := NewHandler(config, db, newTestLogger(t))

	input := &Input{
		SeekerID:        "seeker-004",
		FranchiseID:     "franchise-004",
		ApplicationData: nil,
		ReadinessScore:  75,
		Priority:        "medium",
	}

	output, err := handler.Execute(context.Background(), input)

	assert.NoError(t, err)
	assert.NotNil(t, output)
	assert.NotEmpty(t, output.ApplicationID)

	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestHandler_Execute_ContextTimeout(t *testing.T) {
	db, mock, err := sqlmock.New()
	assert.NoError(t, err)
	defer db.Close()

	// Mock duplicate check that times out
	mock.ExpectQuery(`SELECT EXISTS`).
		WithArgs("seeker-005", "franchise-005").
		WillReturnError(context.DeadlineExceeded)

	config := createTestConfig()
	handler := NewHandler(config, db, newTestLogger(t))

	input := createTestInput()
	input.SeekerID = "seeker-005"
	input.FranchiseID = "franchise-005"

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Millisecond)
	defer cancel()

	output, err := handler.Execute(ctx, input)

	assert.Error(t, err)
	assert.True(t, errors.Is(err, ErrDatabaseInsertFailed))
	assert.Nil(t, output)

	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestHandler_Execute_SpecialCharactersInIDs(t *testing.T) {
	db, mock, err := sqlmock.New()
	assert.NoError(t, err)
	defer db.Close()

	specialSeekerID := "seeker-@#$%^&*()"
	specialFranchiseID := "franchise-!@#$%^&*()"

	// Mock duplicate check
	mock.ExpectQuery(`SELECT EXISTS`).
		WithArgs(specialSeekerID, specialFranchiseID).
		WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(false))

	// Mock application insert
	mock.ExpectExec(`INSERT INTO applications`).
		WithArgs(
			sqlmock.AnyArg(),
			specialSeekerID,
			specialFranchiseID,
			sqlmock.AnyArg(),
			80,
			"high",
			"submitted",
			sqlmock.AnyArg(),
		).
		WillReturnResult(sqlmock.NewResult(1, 1))

	// Mock audit log insert
	mock.ExpectExec(`INSERT INTO audit_log`).
		WithArgs(
			"application_created",
			"application",
			sqlmock.AnyArg(),
			sqlmock.AnyArg(),
			sqlmock.AnyArg(),
		).
		WillReturnResult(sqlmock.NewResult(1, 1))

	config := createTestConfig()
	handler := NewHandler(config, db, newTestLogger(t))

	input := &Input{
		SeekerID:        specialSeekerID,
		FranchiseID:     specialFranchiseID,
		ApplicationData: map[string]interface{}{"test": "data"},
		ReadinessScore:  80,
		Priority:        "high",
	}

	output, err := handler.Execute(context.Background(), input)

	assert.NoError(t, err)
	assert.NotNil(t, output)
	assert.NotEmpty(t, output.ApplicationID)

	assert.NoError(t, mock.ExpectationsWereMet())
}

// ==========================
// Integration Test
// ==========================

func TestHandler_FullWorkflow(t *testing.T) {
	db, mock, err := sqlmock.New()
	assert.NoError(t, err)
	defer db.Close()

	// Test data
	applicationData := map[string]interface{}{
		"user": map[string]interface{}{
			"name":  "Jane Smith",
			"email": "jane@example.com",
			"phone": "+1-555-0123",
		},
		"preferences": map[string]interface{}{
			"investmentRange": "500k-1M",
			"locations":       []string{"NY", "CA", "TX"},
			"categories":      []string{"food", "retail"},
		},
	}

	// Mock duplicate check
	mock.ExpectQuery(`SELECT EXISTS`).
		WithArgs("seeker-full", "franchise-full").
		WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(false))

	// Mock application insert
	mock.ExpectExec(`INSERT INTO applications`).
		WithArgs(
			sqlmock.AnyArg(),
			"seeker-full",
			"franchise-full",
			sqlmock.AnyArg(),
			95,
			"high",
			"submitted",
			sqlmock.AnyArg(),
		).
		WillReturnResult(sqlmock.NewResult(1, 1))

	// Mock audit log insert
	mock.ExpectExec(`INSERT INTO audit_log`).
		WithArgs(
			"application_created",
			"application",
			sqlmock.AnyArg(),
			sqlmock.AnyArg(),
			sqlmock.AnyArg(),
		).
		WillReturnResult(sqlmock.NewResult(1, 1))

	config := createTestConfig()
	handler := NewHandler(config, db, newTestLogger(t))

	input := &Input{
		SeekerID:        "seeker-full",
		FranchiseID:     "franchise-full",
		ApplicationData: applicationData,
		ReadinessScore:  95,
		Priority:        "high",
	}

	output, err := handler.Execute(context.Background(), input)

	assert.NoError(t, err)
	assert.NotNil(t, output)
	assert.NotEmpty(t, output.ApplicationID)
	assert.Equal(t, "submitted", output.ApplicationStatus)
	assert.NotEmpty(t, output.CreatedAt)

	// Verify UUID format
	assert.True(t, len(output.ApplicationID) > 0)
	assert.Contains(t, output.ApplicationID, "-")

	// Verify timestamp is valid RFC3339
	createdTime, err := time.Parse(time.RFC3339, output.CreatedAt)
	assert.NoError(t, err)
	assert.WithinDuration(t, time.Now(), createdTime, 5*time.Second)

	assert.NoError(t, mock.ExpectationsWereMet())
}

// ==========================
// Benchmark Tests
// ==========================

func BenchmarkHandler_Execute(b *testing.B) {
	db, mock, err := sqlmock.New()
	if err != nil {
		b.Fatal(err)
	}
	defer db.Close()

	config := createTestConfig()
	handler := NewHandler(config, db, newTestLogger(&testing.T{}))

	input := createTestInput()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// Setup mock expectations for each iteration
		mock.ExpectQuery(`SELECT EXISTS`).
			WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(false))

		mock.ExpectExec(`INSERT INTO applications`).
			WillReturnResult(sqlmock.NewResult(1, 1))

		mock.ExpectExec(`INSERT INTO audit_log`).
			WillReturnResult(sqlmock.NewResult(1, 1))

		handler.Execute(context.Background(), input)
	}
}

// // internal/workers/application/create-application-record/handler_test.go
// package createapplicationrecord

// import (
// 	"context"
// 	"errors"
// 	"testing"
// 	"time"

// 	"github.com/DATA-DOG/go-sqlmock"
// 	"github.com/stretchr/testify/assert"
// 	"go.uber.org/zap/zaptest"
// )

// // ==========================
// // Test Helper Functions
// // ==========================

// func createTestConfig() *Config {
// 	return &Config{}
// }

// func createTestInput() *Input {
// 	return &Input{
// 		SeekerID:    "seeker-001",
// 		FranchiseID: "franchise-001",
// 		ApplicationData: map[string]interface{}{
// 			"name":     "John Doe",
// 			"email":    "john@example.com",
// 			"location": "New York",
// 		},
// 		ReadinessScore: 85,
// 		Priority:       "high",
// 	}
// }

// // ==========================
// // Core Functionality Tests
// // ==========================

// func TestHandler_Execute_Success(t *testing.T) {
// 	db, mock, err := sqlmock.New()
// 	assert.NoError(t, err)
// 	defer db.Close()

// 	// Mock duplicate check - no existing application
// 	mock.ExpectQuery(`SELECT EXISTS`).
// 		WithArgs("seeker-001", "franchise-001").
// 		WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(false))

// 	// Mock application insert
// 	mock.ExpectExec(`INSERT INTO applications`).
// 		WithArgs(
// 			sqlmock.AnyArg(), // application ID (UUID)
// 			"seeker-001",
// 			"franchise-001",
// 			sqlmock.AnyArg(), // JSON bytes
// 			85,
// 			"high",
// 			"submitted",
// 			sqlmock.AnyArg(), // created_at
// 		).
// 		WillReturnResult(sqlmock.NewResult(1, 1))

// 	// Mock audit log insert
// 	mock.ExpectExec(`INSERT INTO audit_log`).
// 		WithArgs(
// 			"application_created",
// 			"application",
// 			sqlmock.AnyArg(),
// 			sqlmock.AnyArg(),
// 			sqlmock.AnyArg(),
// 		).
// 		WillReturnResult(sqlmock.NewResult(1, 1))

// 	config := createTestConfig()
// 	handler := NewHandler(config, db, zaptest.NewLogger(t))

// 	input := createTestInput()
// 	output, err := handler.execute(context.Background(), input)

// 	assert.NoError(t, err)
// 	assert.NotNil(t, output)
// 	assert.NotEmpty(t, output.ApplicationID)
// 	assert.Equal(t, "submitted", output.ApplicationStatus)
// 	assert.NotEmpty(t, output.CreatedAt)

// 	// Verify timestamp format
// 	_, err = time.Parse(time.RFC3339, output.CreatedAt)
// 	assert.NoError(t, err)

// 	assert.NoError(t, mock.ExpectationsWereMet())
// }

// func TestHandler_Execute_DuplicateApplication(t *testing.T) {
// 	db, mock, err := sqlmock.New()
// 	assert.NoError(t, err)
// 	defer db.Close()

// 	// Mock duplicate check - application already exists
// 	mock.ExpectQuery(`SELECT EXISTS`).
// 		WithArgs("seeker-001", "franchise-001").
// 		WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(true))

// 	config := createTestConfig()
// 	handler := NewHandler(config, db, zaptest.NewLogger(t))

// 	input := createTestInput()
// 	output, err := handler.execute(context.Background(), input)

// 	assert.Error(t, err)
// 	assert.True(t, errors.Is(err, ErrDuplicateApplication))
// 	assert.Contains(t, err.Error(), "application already exists")
// 	assert.Nil(t, output)

// 	assert.NoError(t, mock.ExpectationsWereMet())
// }

// func TestHandler_Execute_DuplicateCheckError(t *testing.T) {
// 	db, mock, err := sqlmock.New()
// 	assert.NoError(t, err)
// 	defer db.Close()

// 	// Mock duplicate check error
// 	mock.ExpectQuery(`SELECT EXISTS`).
// 		WithArgs("seeker-001", "franchise-001").
// 		WillReturnError(errors.New("database connection failed"))

// 	config := createTestConfig()
// 	handler := NewHandler(config, db, zaptest.NewLogger(t))

// 	input := createTestInput()
// 	output, err := handler.execute(context.Background(), input)

// 	assert.Error(t, err)
// 	assert.True(t, errors.Is(err, ErrDatabaseInsertFailed))
// 	assert.Contains(t, err.Error(), "duplicate check failed")
// 	assert.Nil(t, output)

// 	assert.NoError(t, mock.ExpectationsWereMet())
// }

// func TestHandler_Execute_InsertError(t *testing.T) {
// 	db, mock, err := sqlmock.New()
// 	assert.NoError(t, err)
// 	defer db.Close()

// 	// Mock duplicate check - no existing application
// 	mock.ExpectQuery(`SELECT EXISTS`).
// 		WithArgs("seeker-001", "franchise-001").
// 		WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(false))

// 	// Mock application insert error
// 	mock.ExpectExec(`INSERT INTO applications`).
// 		WithArgs(
// 			sqlmock.AnyArg(),
// 			"seeker-001",
// 			"franchise-001",
// 			sqlmock.AnyArg(),
// 			85,
// 			"high",
// 			"submitted",
// 			sqlmock.AnyArg(),
// 		).
// 		WillReturnError(errors.New("insert failed"))

// 	config := createTestConfig()
// 	handler := NewHandler(config, db, zaptest.NewLogger(t))

// 	input := createTestInput()
// 	output, err := handler.execute(context.Background(), input)

// 	assert.Error(t, err)
// 	assert.True(t, errors.Is(err, ErrDatabaseInsertFailed))
// 	assert.Contains(t, err.Error(), "insert failed")
// 	assert.Nil(t, output)

// 	assert.NoError(t, mock.ExpectationsWereMet())
// }

// func TestHandler_Execute_AuditLogError(t *testing.T) {
// 	db, mock, err := sqlmock.New()
// 	assert.NoError(t, err)
// 	defer db.Close()

// 	// Mock duplicate check - no existing application
// 	mock.ExpectQuery(`SELECT EXISTS`).
// 		WithArgs("seeker-001", "franchise-001").
// 		WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(false))

// 	// Mock application insert success
// 	mock.ExpectExec(`INSERT INTO applications`).
// 		WithArgs(
// 			sqlmock.AnyArg(),
// 			"seeker-001",
// 			"franchise-001",
// 			sqlmock.AnyArg(),
// 			85,
// 			"high",
// 			"submitted",
// 			sqlmock.AnyArg(),
// 		).
// 		WillReturnResult(sqlmock.NewResult(1, 1))

// 	// Mock audit log insert error
// 	mock.ExpectExec(`INSERT INTO audit_log`).
// 		WithArgs(
// 			"application_created",
// 			"application",
// 			sqlmock.AnyArg(),
// 			sqlmock.AnyArg(),
// 			sqlmock.AnyArg(),
// 		).
// 		WillReturnError(errors.New("audit log failed"))

// 	config := createTestConfig()
// 	handler := NewHandler(config, db, zaptest.NewLogger(t))

// 	input := createTestInput()
// 	output, err := handler.execute(context.Background(), input)

// 	// Should still succeed even if audit log fails
// 	assert.NoError(t, err)
// 	assert.NotNil(t, output)
// 	assert.NotEmpty(t, output.ApplicationID)
// 	assert.Equal(t, "submitted", output.ApplicationStatus)

// 	assert.NoError(t, mock.ExpectationsWereMet())
// }

// // ==========================
// // Unit Tests
// // ==========================

// func TestHandler_Execute_MinimalInput(t *testing.T) {
// 	db, mock, err := sqlmock.New()
// 	assert.NoError(t, err)
// 	defer db.Close()

// 	// Mock duplicate check - no existing application
// 	mock.ExpectQuery(`SELECT EXISTS`).
// 		WithArgs("seeker-002", "franchise-002").
// 		WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(false))

// 	// Mock application insert
// 	mock.ExpectExec(`INSERT INTO applications`).
// 		WithArgs(
// 			sqlmock.AnyArg(),
// 			"seeker-002",
// 			"franchise-002",
// 			sqlmock.AnyArg(), // empty map will be JSON: {}
// 			0,
// 			"",
// 			"submitted",
// 			sqlmock.AnyArg(),
// 		).
// 		WillReturnResult(sqlmock.NewResult(1, 1))

// 	// Mock audit log insert
// 	mock.ExpectExec(`INSERT INTO audit_log`).
// 		WithArgs(
// 			"application_created",
// 			"application",
// 			sqlmock.AnyArg(),
// 			sqlmock.AnyArg(),
// 			sqlmock.AnyArg(),
// 		).
// 		WillReturnResult(sqlmock.NewResult(1, 1))

// 	config := createTestConfig()
// 	handler := NewHandler(config, db, zaptest.NewLogger(t))

// 	input := &Input{
// 		SeekerID:        "seeker-002",
// 		FranchiseID:     "franchise-002",
// 		ApplicationData: map[string]interface{}{},
// 		ReadinessScore:  0,
// 		Priority:        "",
// 	}

// 	output, err := handler.execute(context.Background(), input)

// 	assert.NoError(t, err)
// 	assert.NotNil(t, output)
// 	assert.NotEmpty(t, output.ApplicationID)
// 	assert.Equal(t, "submitted", output.ApplicationStatus)

// 	assert.NoError(t, mock.ExpectationsWereMet())
// }

// func TestHandler_Execute_ComplexApplicationData(t *testing.T) {
// 	db, mock, err := sqlmock.New()
// 	assert.NoError(t, err)
// 	defer db.Close()

// 	complexData := map[string]interface{}{
// 		"personalInfo": map[string]interface{}{
// 			"firstName": "John",
// 			"lastName":  "Doe",
// 			"age":       35,
// 			"address": map[string]interface{}{
// 				"street":  "123 Main St",
// 				"city":    "New York",
// 				"state":   "NY",
// 				"zipCode": "10001",
// 			},
// 		},
// 		"financialInfo": map[string]interface{}{
// 			"liquidCapital": 500000,
// 			"netWorth":      1000000,
// 			"creditScore":   750,
// 		},
// 		"experience": []interface{}{
// 			"management",
// 			"retail",
// 			"customer_service",
// 		},
// 	}

// 	// Mock duplicate check
// 	mock.ExpectQuery(`SELECT EXISTS`).
// 		WithArgs("seeker-003", "franchise-003").
// 		WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(false))

// 	// Mock application insert
// 	mock.ExpectExec(`INSERT INTO applications`).
// 		WithArgs(
// 			sqlmock.AnyArg(),
// 			"seeker-003",
// 			"franchise-003",
// 			sqlmock.AnyArg(),
// 			90,
// 			"high",
// 			"submitted",
// 			sqlmock.AnyArg(),
// 		).
// 		WillReturnResult(sqlmock.NewResult(1, 1))

// 	// Mock audit log insert
// 	mock.ExpectExec(`INSERT INTO audit_log`).
// 		WithArgs(
// 			"application_created",
// 			"application",
// 			sqlmock.AnyArg(),
// 			sqlmock.AnyArg(),
// 			sqlmock.AnyArg(),
// 		).
// 		WillReturnResult(sqlmock.NewResult(1, 1))

// 	config := createTestConfig()
// 	handler := NewHandler(config, db, zaptest.NewLogger(t))

// 	input := &Input{
// 		SeekerID:        "seeker-003",
// 		FranchiseID:     "franchise-003",
// 		ApplicationData: complexData,
// 		ReadinessScore:  90,
// 		Priority:        "high",
// 	}

// 	output, err := handler.execute(context.Background(), input)

// 	assert.NoError(t, err)
// 	assert.NotNil(t, output)
// 	assert.NotEmpty(t, output.ApplicationID)

// 	assert.NoError(t, mock.ExpectationsWereMet())
// }

// // ==========================
// // Edge Cases
// // ==========================

// func TestHandler_Execute_NilApplicationData(t *testing.T) {
// 	db, mock, err := sqlmock.New()
// 	assert.NoError(t, err)
// 	defer db.Close()

// 	// Mock duplicate check
// 	mock.ExpectQuery(`SELECT EXISTS`).
// 		WithArgs("seeker-004", "franchise-004").
// 		WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(false))

// 	// Mock application insert with nil application data
// 	mock.ExpectExec(`INSERT INTO applications`).
// 		WithArgs(
// 			sqlmock.AnyArg(),
// 			"seeker-004",
// 			"franchise-004",
// 			sqlmock.AnyArg(), // nil will be JSON: null
// 			75,
// 			"medium",
// 			"submitted",
// 			sqlmock.AnyArg(),
// 		).
// 		WillReturnResult(sqlmock.NewResult(1, 1))

// 	// Mock audit log insert
// 	mock.ExpectExec(`INSERT INTO audit_log`).
// 		WithArgs(
// 			"application_created",
// 			"application",
// 			sqlmock.AnyArg(),
// 			sqlmock.AnyArg(),
// 			sqlmock.AnyArg(),
// 		).
// 		WillReturnResult(sqlmock.NewResult(1, 1))

// 	config := createTestConfig()
// 	handler := NewHandler(config, db, zaptest.NewLogger(t))

// 	input := &Input{
// 		SeekerID:        "seeker-004",
// 		FranchiseID:     "franchise-004",
// 		ApplicationData: nil,
// 		ReadinessScore:  75,
// 		Priority:        "medium",
// 	}

// 	output, err := handler.execute(context.Background(), input)

// 	assert.NoError(t, err)
// 	assert.NotNil(t, output)
// 	assert.NotEmpty(t, output.ApplicationID)

// 	assert.NoError(t, mock.ExpectationsWereMet())
// }

// func TestHandler_Execute_ContextTimeout(t *testing.T) {
// 	db, mock, err := sqlmock.New()
// 	assert.NoError(t, err)
// 	defer db.Close()

// 	// Mock duplicate check that times out
// 	mock.ExpectQuery(`SELECT EXISTS`).
// 		WithArgs("seeker-005", "franchise-005").
// 		WillReturnError(context.DeadlineExceeded)

// 	config := createTestConfig()
// 	handler := NewHandler(config, db, zaptest.NewLogger(t))

// 	input := createTestInput()
// 	input.SeekerID = "seeker-005"
// 	input.FranchiseID = "franchise-005"

// 	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Millisecond)
// 	defer cancel()

// 	output, err := handler.execute(ctx, input)

// 	assert.Error(t, err)
// 	assert.True(t, errors.Is(err, ErrDatabaseInsertFailed))
// 	assert.Nil(t, output)

// 	assert.NoError(t, mock.ExpectationsWereMet())
// }

// func TestHandler_Execute_SpecialCharactersInIDs(t *testing.T) {
// 	db, mock, err := sqlmock.New()
// 	assert.NoError(t, err)
// 	defer db.Close()

// 	specialSeekerID := "seeker-@#$%^&*()"
// 	specialFranchiseID := "franchise-!@#$%^&*()"

// 	// Mock duplicate check
// 	mock.ExpectQuery(`SELECT EXISTS`).
// 		WithArgs(specialSeekerID, specialFranchiseID).
// 		WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(false))

// 	// Mock application insert
// 	mock.ExpectExec(`INSERT INTO applications`).
// 		WithArgs(
// 			sqlmock.AnyArg(),
// 			specialSeekerID,
// 			specialFranchiseID,
// 			sqlmock.AnyArg(),
// 			80,
// 			"high",
// 			"submitted",
// 			sqlmock.AnyArg(),
// 		).
// 		WillReturnResult(sqlmock.NewResult(1, 1))

// 	// Mock audit log insert
// 	mock.ExpectExec(`INSERT INTO audit_log`).
// 		WithArgs(
// 			"application_created",
// 			"application",
// 			sqlmock.AnyArg(),
// 			sqlmock.AnyArg(),
// 			sqlmock.AnyArg(),
// 		).
// 		WillReturnResult(sqlmock.NewResult(1, 1))

// 	config := createTestConfig()
// 	handler := NewHandler(config, db, zaptest.NewLogger(t))

// 	input := &Input{
// 		SeekerID:        specialSeekerID,
// 		FranchiseID:     specialFranchiseID,
// 		ApplicationData: map[string]interface{}{"test": "data"},
// 		ReadinessScore:  80,
// 		Priority:        "high",
// 	}

// 	output, err := handler.execute(context.Background(), input)

// 	assert.NoError(t, err)
// 	assert.NotNil(t, output)
// 	assert.NotEmpty(t, output.ApplicationID)

// 	assert.NoError(t, mock.ExpectationsWereMet())
// }

// // ==========================
// // Integration Test
// // ==========================

// func TestHandler_FullWorkflow(t *testing.T) {
// 	db, mock, err := sqlmock.New()
// 	assert.NoError(t, err)
// 	defer db.Close()

// 	// Test data
// 	applicationData := map[string]interface{}{
// 		"user": map[string]interface{}{
// 			"name":  "Jane Smith",
// 			"email": "jane@example.com",
// 			"phone": "+1-555-0123",
// 		},
// 		"preferences": map[string]interface{}{
// 			"investmentRange": "500k-1M",
// 			"locations":       []string{"NY", "CA", "TX"},
// 			"categories":      []string{"food", "retail"},
// 		},
// 	}

// 	// Mock duplicate check
// 	mock.ExpectQuery(`SELECT EXISTS`).
// 		WithArgs("seeker-full", "franchise-full").
// 		WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(false))

// 	// Mock application insert
// 	mock.ExpectExec(`INSERT INTO applications`).
// 		WithArgs(
// 			sqlmock.AnyArg(),
// 			"seeker-full",
// 			"franchise-full",
// 			sqlmock.AnyArg(),
// 			95,
// 			"high",
// 			"submitted",
// 			sqlmock.AnyArg(),
// 		).
// 		WillReturnResult(sqlmock.NewResult(1, 1))

// 	// Mock audit log insert
// 	mock.ExpectExec(`INSERT INTO audit_log`).
// 		WithArgs(
// 			"application_created",
// 			"application",
// 			sqlmock.AnyArg(),
// 			sqlmock.AnyArg(),
// 			sqlmock.AnyArg(),
// 		).
// 		WillReturnResult(sqlmock.NewResult(1, 1))

// 	config := createTestConfig()
// 	handler := NewHandler(config, db, zaptest.NewLogger(t))

// 	input := &Input{
// 		SeekerID:        "seeker-full",
// 		FranchiseID:     "franchise-full",
// 		ApplicationData: applicationData,
// 		ReadinessScore:  95,
// 		Priority:        "high",
// 	}

// 	output, err := handler.execute(context.Background(), input)

// 	assert.NoError(t, err)
// 	assert.NotNil(t, output)
// 	assert.NotEmpty(t, output.ApplicationID)
// 	assert.Equal(t, "submitted", output.ApplicationStatus)
// 	assert.NotEmpty(t, output.CreatedAt)

// 	// Verify UUID format
// 	assert.True(t, len(output.ApplicationID) > 0)
// 	assert.Contains(t, output.ApplicationID, "-")

// 	// Verify timestamp is valid RFC3339
// 	createdTime, err := time.Parse(time.RFC3339, output.CreatedAt)
// 	assert.NoError(t, err)
// 	assert.WithinDuration(t, time.Now(), createdTime, 5*time.Second)

// 	assert.NoError(t, mock.ExpectationsWereMet())
// }

// // ==========================
// // Benchmark Tests
// // ==========================

// func BenchmarkHandler_Execute(b *testing.B) {
// 	db, mock, err := sqlmock.New()
// 	if err != nil {
// 		b.Fatal(err)
// 	}
// 	defer db.Close()

// 	config := createTestConfig()
// 	handler := NewHandler(config, db, zaptest.NewLogger(b))

// 	input := createTestInput()

// 	b.ResetTimer()
// 	for i := 0; i < b.N; i++ {
// 		// Setup mock expectations for each iteration
// 		mock.ExpectQuery(`SELECT EXISTS`).
// 			WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(false))

// 		mock.ExpectExec(`INSERT INTO applications`).
// 			WillReturnResult(sqlmock.NewResult(1, 1))

// 		mock.ExpectExec(`INSERT INTO audit_log`).
// 			WillReturnResult(sqlmock.NewResult(1, 1))

// 		handler.execute(context.Background(), input)
// 	}
// }
