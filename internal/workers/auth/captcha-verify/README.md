# Captcha Verify Worker

## Purpose
Validates CAPTCHA challenges to prevent bot submissions during user authentication flows.

## Task Type
`security.captcha.verify`

## Input Schema
- `captchaId` (string, required): Unique CAPTCHA identifier (format: `cap_{timestamp}_{random}`)
- `captchaValue` (string, required): User-provided CAPTCHA solution (4-8 uppercase alphanumeric chars)
- `clientIP` (string, required): IPv4 address of the client
- `userAgent` (string, required): User agent string (10-500 chars)

## Output Schema
- `valid` (boolean): Verification result
- `score` (number, optional): Confidence score (0.0-1.0)
- `retryCount` (integer, optional): Number of verification attempts
- `errorCode` (string, optional): Error code if verification failed
- `errorMessage` (string, optional): Human-readable error message

## Error Codes
- `CAPTCHA_NOT_FOUND`: CAPTCHA ID not found or already used
- `CAPTCHA_EXPIRED`: CAPTCHA challenge has expired
- `MAX_ATTEMPTS_EXCEEDED`: Maximum verification attempts exceeded
- `INVALID_CAPTCHA`: CAPTCHA value doesn't match

## Configuration
See `configs/config.yaml` for worker-specific settings.

## Security
- Implements rate limiting (10 requests/minute per IP)
- Sanitizes all user inputs
- Validates IP address format
- Uses case-insensitive comparison