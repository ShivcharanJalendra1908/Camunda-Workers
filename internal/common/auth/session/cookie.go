package session

import (
	"net/http"
	"time"
)

const (
	CookieName = "__Host-session"
)

// CookieOptions defines how session cookies are issued.
type CookieOptions struct {
	Path     string
	HttpOnly bool
	Secure   bool
	SameSite http.SameSite
	Domain   string // should usually be empty for __Host- cookies
}

// normalize applies safe defaults without breaking callers
// func (o CookieOptions) normalize() CookieOptions {
// 	if o.Path == "" {
// 		o.Path = "/"
// 	}
// 	// Enforce security (NOT optional)
// 	o.HttpOnly = true
// 	o.Secure = true
// 	if o.SameSite == 0 {
// 		// o.SameSite = http.SameSiteLaxMode
// 		o.SameSite = http.SameSiteStrictMode
// 	}
// 	return o
// }

// --- TEST ONLY ---
func (o CookieOptions) normalize() CookieOptions {
	if o.Path == "" {
		o.Path = "/"
	}
	o.HttpOnly = true
	o.Secure = true
	o.SameSite = http.SameSiteNoneMode
	// IMPORTANT: do NOT set Domain for now
	o.Domain = ""
	return o
}

// SetCookie issues the session cookie to the client.
func SetCookie(
	w http.ResponseWriter,
	sessionID string,
	expiresAt time.Time,
	opts CookieOptions,
) {
	opts = opts.normalize()

	http.SetCookie(w, &http.Cookie{
		Name:     CookieName,
		Value:    sessionID,
		Path:     opts.Path,
		Domain:   opts.Domain,
		Expires:  expiresAt,
		HttpOnly: opts.HttpOnly,
		Secure:   opts.Secure,
		SameSite: opts.SameSite,
	})
}

// ClearCookie removes the session cookie from the client.
func ClearCookie(
	w http.ResponseWriter,
	opts CookieOptions,
) {
	opts = opts.normalize()

	http.SetCookie(w, &http.Cookie{
		Name:     CookieName,
		Value:    "",
		Path:     opts.Path,
		Domain:   opts.Domain,
		MaxAge:   -1,
		HttpOnly: opts.HttpOnly,
		Secure:   opts.Secure,
		SameSite: opts.SameSite,
	})
}

// BuildSetCookieHeader creates a Set-Cookie header string
func BuildSetCookieHeader(sessionID string, expiresAt time.Time, secure bool, sameSite string) string {
	maxAge := int(time.Until(expiresAt).Seconds())

	// cookie := &http.Cookie{
	// 	Name:     "session_id",
	// 	Value:    sessionID,
	// 	Path:     "/",
	// 	HttpOnly: true,
	// 	Secure:   secure,
	// 	MaxAge:   maxAge,
	// }

	// cookie := &http.Cookie{
	// 	Name:     "session_id",
	// 	Value:    sessionID,
	// 	Path:     "/",
	// 	HttpOnly: true,
	// 	Secure:   true,
	// 	SameSite: http.SameSiteNoneMode,
	// 	MaxAge:   maxAge,
	// }

	// switch sameSite {
	// case "Strict":
	// 	cookie.SameSite = http.SameSiteStrictMode
	// case "None":
	// 	cookie.SameSite = http.SameSiteNoneMode
	// default:
	// 	cookie.SameSite = http.SameSiteLaxMode
	// }

	cookie := &http.Cookie{
		Name:     "AUTH_SESSION_ID",
		Value:    sessionID,
		Path:     "/",
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteNoneMode,
		MaxAge:   maxAge,
	}

	return cookie.String()
}

// BuildClearCookieHeader creates a cookie deletion header
func BuildClearCookieHeader(secure bool, sameSite string) string {
	// cookie := &http.Cookie{
	// 	Name:     "session_id",
	// 	Value:    "",
	// 	Path:     "/",
	// 	HttpOnly: true,
	// 	Secure:   secure,
	// 	MaxAge:   -1,
	// }

	// switch sameSite {
	// case "Strict":
	// 	cookie.SameSite = http.SameSiteStrictMode
	// case "None":
	// 	cookie.SameSite = http.SameSiteNoneMode
	// default:
	// 	cookie.SameSite = http.SameSiteLaxMode
	// }

	cookie := &http.Cookie{
		Name:     "AUTH_SESSION_ID",
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteNoneMode,
		MaxAge:   -1,
	}

	return cookie.String()
}
