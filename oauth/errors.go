package oauth

import (
	"encoding/json"
	"fmt"
)

// AuthError is returned when the token endpoint responds with a non-2xx
// status. Plain OAuth 2.0 (RFC 6749 section 5.2) and Salesforce error
// responses use the same {"error", "error_description"} JSON shape, so one
// type covers both.
type AuthError struct {
	StatusCode int
	// Code is the OAuth "error" field, e.g. "invalid_grant" or
	// "invalid_client". Empty if the body didn't parse as OAuth-shaped JSON.
	Code string
	// Description is the "error_description" field, if present.
	Description string
	// Body is a truncated copy of the raw response body, populated only when
	// it did not parse as {"error", "error_description"} JSON.
	Body string
}

func (e *AuthError) Error() string {
	switch {
	case e.Code != "" && e.Description != "":
		return fmt.Sprintf("oauth: token request failed (%d): %s: %s", e.StatusCode, e.Code, e.Description)
	case e.Code != "":
		return fmt.Sprintf("oauth: token request failed (%d): %s", e.StatusCode, e.Code)
	default:
		return fmt.Sprintf("oauth: token request failed (%d): %s", e.StatusCode, e.Body)
	}
}

// maxErrorBodyPreview bounds how much of an unparseable error body gets
// captured, keeping log lines and error messages readable.
const maxErrorBodyPreview = 500

// parseAuthError builds an AuthError from a non-2xx token response body.
func parseAuthError(status int, body []byte) *AuthError {
	var e struct {
		Error       string `json:"error"`
		Description string `json:"error_description"`
	}
	if err := json.Unmarshal(body, &e); err == nil && e.Error != "" {
		return &AuthError{StatusCode: status, Code: e.Error, Description: e.Description}
	}

	preview := string(body)
	if len(preview) > maxErrorBodyPreview {
		preview = preview[:maxErrorBodyPreview]
	}
	return &AuthError{StatusCode: status, Body: preview}
}
