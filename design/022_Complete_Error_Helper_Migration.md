# 025: Complete API Error Helper Migration

- **Status**: Proposed
- **Author**: Matthew Burns
- **Date**: 2026-01-10

## Summary

This design completes the migration started in Design 019 to unify error response handling in the API package. It removes duplicate helper functions and consolidates on a single set of error utilities.

## Motivation

Design 019 introduced `rf*`-prefixed helpers (`rfWriteErrorResponse`, `rfWriteJSONResponse`, etc.) in `errors.go` and `respond.go` to centralize error handling. However, the migration was not completed:

1. **Duplicate definitions**: `validMessageIDs` exists in both `api.go` (line 39) and `errors.go` (line 27 as `rfValidMessageIDs`)
2. **Mixed usage**: Handlers use both `h.writeErrorResponse` and `rfWriteErrorResponse`
3. **Confusing naming**: `rf*` prefix was intended as temporary during migration
4. **Incomplete migration**: Original helpers still exist and are actively used

**Current state from grep:**
- `internal/api/console.go`: 36 error response calls
- `internal/api/virtual_media.go`: 32 error response calls
- `internal/api/account_service.go`: 32 error response calls
- Most use `h.writeErrorResponse`, not the centralized helpers

## Goals

- Remove duplicate `validMessageIDs` / `rfValidMessageIDs`
- Rename `rf*` helpers to remove prefix (migration complete)
- Update all handlers to use centralized helpers
- Remove deprecated `writeErrorResponse` method from Handler
- Maintain identical external behavior

## Non-Goals

- No changes to error response format or content
- No new error codes or messages
- No changes to HTTP status code selection

## Implementation Plan

### Milestone 1: Audit Current Usage

Identify all call sites:

```bash
grep -rn "writeErrorResponse\|rfWriteErrorResponse" internal/api/
grep -rn "validMessageIDs\|rfValidMessageIDs" internal/api/
grep -rn "writeJSONResponse\|rfWriteJSONResponse" internal/api/
```

### Milestone 2: Consolidate Message IDs

In `internal/api/errors.go`:

**Before:**
```go
var rfValidMessageIDs = map[string]struct{}{
    "Base.1.0.GeneralError":            {},
    // ...
}
```

**After:**
```go
// ValidMessageIDs contains the set of valid Base message registry IDs
var ValidMessageIDs = map[string]struct{}{
    "Base.1.0.GeneralError":            {},
    // ...
}
```

Remove from `internal/api/api.go`:
```go
// DELETE these lines (38-51):
// validMessageIDs contains the set of valid Base message registry IDs
var validMessageIDs = map[string]struct{}{
    // ...
}
```

### Milestone 3: Rename Helpers (Remove rf Prefix)

In `internal/api/errors.go`:

| Before | After |
|--------|-------|
| `rfValidMessageIDs` | `ValidMessageIDs` |
| `rfWriteErrorResponse` | `WriteErrorResponse` |
| `rfSeverityForStatus` | `SeverityForStatus` |
| `rfResolutionForMessageID` | `ResolutionForMessageID` |

In `internal/api/respond.go`:

| Before | After |
|--------|-------|
| `rfWriteJSONResponse` | `WriteJSONResponse` |
| `rfWriteJSONResponseWithETag` | `WriteJSONResponseWithETag` |
| `rfWriteAllow` | `WriteAllow` |
| `rfIfNoneMatchMatches` | `IfNoneMatchMatches` |

### Milestone 4: Create Handler Wrapper

For backward compatibility during migration, add a thin wrapper on Handler:

```go
// writeErrorResponse delegates to the package-level WriteErrorResponse.
// This provides a convenient method syntax for handlers.
func (h *Handler) writeErrorResponse(w http.ResponseWriter, status int, code, message string) {
    WriteErrorResponse(w, status, code, message)
}

// writeJSONResponse delegates to the package-level WriteJSONResponse.
func (h *Handler) writeJSONResponse(w http.ResponseWriter, status int, data interface{}) {
    WriteJSONResponse(w, status, data)
}
```

This allows existing `h.writeErrorResponse(...)` calls to continue working while internally using the centralized implementation.

### Milestone 5: Update Internal References

Update all internal references in errors.go and respond.go:

```go
// In errors.go
func WriteErrorResponse(w http.ResponseWriter, status int, code, message string) {
    // ...
    if _, ok := ValidMessageIDs[code]; ok {  // was rfValidMessageIDs
        messageID = code
    }
    // ...
    WriteJSONResponse(w, status, errorResp)  // was rfWriteJSONResponse
}
```

### Milestone 6: Update Comment Headers

Remove migration notes from files:

**In errors.go, remove:**
```go
// NOTE: These helpers and message registry IDs were extracted as part of
// design 019 to centralize error response handling across handlers and middleware.
// During the transition, names are rf*-prefixed to avoid symbol duplication
// with existing helpers in api.go. Call sites can migrate to use these.
```

**Replace with:**
```go
// Error response helpers for Redfish-compliant error payloads.
// Consolidated from Design 019 and 025.
```

**In respond.go, remove:**
```go
// NOTE: These helpers were extracted as part of design 019 to centralize
// response handling for JSON, headers, and Allow. They intentionally use
// rf*-prefixed names to avoid symbol conflicts while call sites are migrated.
```

**Replace with:**
```go
// Response helpers for JSON serialization, headers, and ETag handling.
// Consolidated from Design 019 and 025.
```

### Milestone 7: Verify No External Breakage

The `rf*` functions were internal (lowercase would be unexported, but they used the `rf` prefix convention). Verify:

1. No external packages import these helpers directly
2. All tests pass with renamed functions
3. HTTP responses are byte-for-byte identical

## File Changes Summary

### Modified Files

| File | Changes |
|------|---------|
| `internal/api/errors.go` | Rename `rf*` → exported names, update comments |
| `internal/api/respond.go` | Rename `rf*` → exported names, update comments |
| `internal/api/api.go` | Remove duplicate `validMessageIDs`, add wrapper methods |

### Removed Code

| Location | Code |
|----------|------|
| `api.go` lines 38-51 | `validMessageIDs` map |

### No New Files

All changes are in existing files.

## Testing

1. **All existing tests must pass**
2. **Verify error responses**: Compare HTTP response bodies before/after
3. **Check headers**: `Content-Type`, `OData-Version`, `WWW-Authenticate` unchanged

## Validation

```bash
go run build.go validate
```

## Implementation Notes for AI Agents

1. **Milestone 2 first**: Remove duplicate from api.go before renaming
2. **Search and replace carefully**: `rfWriteErrorResponse` → `WriteErrorResponse`
3. **Keep wrapper methods**: They provide `h.method()` syntax for handlers
4. **Run tests after each change**: Catch regressions immediately
5. **No behavior changes**: Only naming and organization

## Rollback Plan

Revert the commits for this design. The `rf*` prefixed helpers and duplicates will be restored.

## Future Considerations

After this design is complete:
- Consider deriving `ValidMessageIDs` from embedded Redfish registries
- Could add structured error types for better error handling
- May want to add request ID tracking to error responses
