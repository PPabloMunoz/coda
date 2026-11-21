// Package errors provides the application's error foundation.
//
// Service code returns these errors without knowing about HTTP, Gin,
// databases, or other infrastructure. The server layer translates the
// stable Code into responses. Messages are safe to show; Cause is
// internal-only and never rendered.
package errors
