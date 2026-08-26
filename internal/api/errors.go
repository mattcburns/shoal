package api

import (
	"log/slog"
	"net/http"
	"strings"
)

// upstreamErrorMessage is the generic, client-facing message returned for any
// failure caused by a configured backend dependency (BMC/Redfish, the
// observe/telemetry service, the secrets backend, NetBox, ...). It
// intentionally omits detail: the underlying error may contain hostnames,
// paths, or other internal information that must never reach an API caller.
const upstreamErrorMessage = "upstream request failed"

// writeUpstreamError logs the real error server-side via slog (msg plus any
// extra key/value args for debugging context) and writes the standard 502
// Bad Gateway JSON error body to the client.
//
// Use this for the "a configured backend dependency failed" class of error --
// BMC/Redfish calls, observe service calls, secrets backend calls -- never
// for client-input (400), not-found (404), or auth (401/403) failures, which
// represent a different situation and keep their own status codes. Never
// write err.Error() directly into a response body for this class of failure;
// this helper is the only place that should decide what the client sees.
func writeUpstreamError(w http.ResponseWriter, log *slog.Logger, msg string, err error, args ...any) {
	if log != nil {
		logArgs := make([]any, 0, len(args)+2)
		logArgs = append(logArgs, "err", err.Error())
		logArgs = append(logArgs, args...)
		log.Error(msg, logArgs...)
	}
	writeJSON(w, http.StatusBadGateway, map[string]any{"error": upstreamErrorMessage})
}

// writeUpstreamErrorBody behaves like writeUpstreamError but merges extra
// fields into the response body alongside the generic error message. Use it
// when a handler legitimately needs to return partial results gathered
// before the upstream failure (e.g. a poll that wrote some telemetry before
// the BMC call failed). extra must never contain raw error detail.
func writeUpstreamErrorBody(w http.ResponseWriter, log *slog.Logger, msg string, err error, extra map[string]any, args ...any) {
	if log != nil {
		logArgs := make([]any, 0, len(args)+2)
		logArgs = append(logArgs, "err", err.Error())
		logArgs = append(logArgs, args...)
		log.Error(msg, logArgs...)
	}
	body := map[string]any{"error": upstreamErrorMessage}
	for k, v := range extra {
		body[k] = v
	}
	writeJSON(w, http.StatusBadGateway, body)
}

// isNotConfiguredErr reports whether err represents an optional dependency
// that simply isn't wired up (e.g. observe's Telemetry store being nil
// because SHOAL_TELEMETRY_DATABASE_URL is unset) rather than a configured
// dependency that failed at call time. This is a genuinely different
// situation from writeUpstreamError's class of failure: it mirrors the 503
// "not configured" responses handlers already return when a Server field
// (s.observe, s.jobs, ...) is nil outright, so callers should respond the
// same way (503) even when the nil-ness is discovered one layer down inside
// an otherwise-configured service.
func isNotConfiguredErr(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "not configured")
}
