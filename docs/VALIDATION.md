# Input Validation Guide

## Overview
All input validation is handled through the `internal/common/validation` package.

## Usage

### In Workers
```go
func (h *Handler) validateInput(input *Input) error {
    return ozzo.Validate(input.Email,
        ozzo.Required,
        validation.ValidateEmail(),
        validation.ValidateStringLength(5, 255),
    )
}
```

### In API Handlers
```go
if err := h.validator.ValidateEmail(input.Email); err != nil {
    return errors.NewValidationError("email", err.Error())
}
```

## Validation Rules

### String Validation
- Max length: 1000 characters (default)
- SQL injection prevention: Automatic
- NoSQL injection prevention: Automatic

### UUID Validation
- Format: `xxxxxxxx-xxxx-4xxx-xxxx-xxxxxxxxxxxx`
- Length: Exactly 36 characters

### Email Validation
- Format: RFC 5322 compliant
- Length: 5-255 characters

### Array Validation
- Max size: 100 items (default)
- Max depth: 10 levels (for nested objects)