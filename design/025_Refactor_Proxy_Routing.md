# 026: Refactor API Console and Proxy Routing

- **Status**: Proposed
- **Author**: Matthew Burns
- **Date**: 2026-01-10

## Summary

This design reduces the cyclomatic complexity of `handleBMCProxy` (currently 33) and `parseConsolePath` (currently 24) by introducing declarative routing tables instead of nested if/else chains.

## Motivation

Two functions in the API package have high complexity due to path-based routing implemented with string manipulation and nested conditionals:

1. **`handleBMCProxy`** in `internal/api/proxy.go` (complexity 33):
   - Parses URL paths manually with `strings.Split`
   - Deep nesting for VirtualMedia, Oem/Shoal, and standard proxy paths
   - Hard to add new routes without increasing complexity

2. **`parseConsolePath`** in `internal/api/console.go` (complexity 24):
   - Similar manual path parsing
   - Multiple levels of if/else for different console operations

**Problems:**
1. **Hard to reason about**: Which paths are handled where?
2. **Error-prone modifications**: Adding routes requires careful nesting
3. **Duplicated patterns**: Both functions use similar path parsing logic

## Goals

- Reduce `handleBMCProxy` complexity to <15
- Reduce `parseConsolePath` complexity to <10
- Make route handling declarative and scannable
- Maintain identical behavior and path matching

## Non-Goals

- No changes to which paths are handled
- No changes to response formats
- No new routing framework or dependencies
- No changes to authentication flow

## Implementation Plan

### Milestone 1: Define Route Pattern Types

Create `internal/api/routes.go` with routing infrastructure:

```go
// routes.go - Declarative routing helpers for proxy and console paths

// RoutePattern defines a URL path pattern and its handler
type RoutePattern struct {
    // Pattern segments, where "*" matches any single segment
    // Example: []string{"Managers", "*", "VirtualMedia"} matches /Managers/bmc1/VirtualMedia
    Segments []string

    // Handler is called when pattern matches. Receives extracted path variables.
    Handler func(w http.ResponseWriter, r *http.Request, vars RouteVars)

    // Methods specifies allowed HTTP methods (empty = all)
    Methods []string
}

// RouteVars holds extracted path variables
type RouteVars struct {
    BMCName   string
    ManagerID string
    SystemID  string
    MediaID   string
    Extra     []string // Additional path segments after pattern
}

// matchRoute attempts to match a path against patterns
func matchRoute(path string, patterns []RoutePattern) (*RoutePattern, RouteVars, bool)
```

### Milestone 2: Refactor handleBMCProxy

**Current structure (simplified):**
```go
func (h *Handler) handleBMCProxy(w http.ResponseWriter, r *http.Request, path string) {
    parts := strings.Split(strings.Trim(path, "/"), "/")
    if len(parts) < 3 { ... }

    if parts[1] == "Managers" && len(parts) >= 3 {
        bmcName = parts[2]
        if len(parts) >= 4 && parts[3] == "VirtualMedia" {
            if len(parts) == 4 { ... }
            if len(parts) == 5 { ... }
            if len(parts) == 7 && parts[5] == "Actions" { ... }
        }
        // ... more nesting
    } else if parts[1] == "Systems" && len(parts) >= 3 {
        // ... deep nesting for Oem/Shoal paths
    }
    // ... proxy fallback
}
```

**Refactored structure:**
```go
var proxyRoutes = []RoutePattern{
    // VirtualMedia collection
    {
        Segments: []string{"v1", "Managers", "*", "VirtualMedia"},
        Handler:  func(h *Handler) RouteHandler { return h.handleVirtualMediaCollection },
    },
    // VirtualMedia resource
    {
        Segments: []string{"v1", "Managers", "*", "VirtualMedia", "*"},
        Handler:  func(h *Handler) RouteHandler { return h.handleVirtualMedia },
    },
    // VirtualMedia InsertMedia action
    {
        Segments: []string{"v1", "Managers", "*", "VirtualMedia", "*", "Actions", "VirtualMedia.InsertMedia"},
        Handler:  func(h *Handler) RouteHandler { return h.handleInsertMedia },
    },
    // VirtualMedia EjectMedia action
    {
        Segments: []string{"v1", "Managers", "*", "VirtualMedia", "*", "Actions", "VirtualMedia.EjectMedia"},
        Handler:  func(h *Handler) RouteHandler { return h.handleEjectMedia },
    },
    // Manager root (needs console properties)
    {
        Segments: []string{"v1", "Managers", "*"},
        Handler:  func(h *Handler) RouteHandler { return h.handleManagerResource },
    },
    // Provisioning Kickstart
    {
        Segments: []string{"v1", "Systems", "*", "Oem", "Shoal", "ProvisioningConfiguration", "Kickstart"},
        Handler:  func(h *Handler) RouteHandler { return h.handleProvisioningKickstart },
    },
    // Provisioning Preseed
    {
        Segments: []string{"v1", "Systems", "*", "Oem", "Shoal", "ProvisioningConfiguration", "Preseed"},
        Handler:  func(h *Handler) RouteHandler { return h.handleProvisioningPreseed },
    },
}

func (h *Handler) handleBMCProxy(w http.ResponseWriter, r *http.Request, path string) {
    // Try declarative routes first
    if pattern, vars, ok := matchRoute(path, proxyRoutes); ok {
        pattern.Handler(w, r, vars)
        return
    }

    // Fall back to standard proxy behavior
    h.proxyToBackend(w, r, path)
}
```

### Milestone 3: Implement Route Matching

```go
// matchRoute matches a path against route patterns
// Returns the first matching pattern, extracted variables, and success flag
func matchRoute(path string, patterns []RoutePattern) (*RoutePattern, RouteVars, bool) {
    parts := strings.Split(strings.Trim(path, "/"), "/")

    for i := range patterns {
        pattern := &patterns[i]
        vars, ok := matchPattern(parts, pattern.Segments)
        if ok {
            return pattern, vars, true
        }
    }

    return nil, RouteVars{}, false
}

// matchPattern checks if path parts match pattern segments
// "*" in pattern matches any single segment and captures it
func matchPattern(parts, pattern []string) (RouteVars, bool) {
    if len(parts) != len(pattern) {
        return RouteVars{}, false
    }

    var vars RouteVars
    for i, seg := range pattern {
        if seg == "*" {
            // Capture wildcard based on position
            switch {
            case i == 2 && (pattern[1] == "Managers" || pattern[1] == "Systems"):
                vars.BMCName = parts[i]
            case i == 4 && pattern[3] == "VirtualMedia":
                vars.MediaID = parts[i]
            default:
                vars.Extra = append(vars.Extra, parts[i])
            }
        } else if parts[i] != seg {
            return RouteVars{}, false
        }
    }

    return vars, true
}
```

### Milestone 4: Refactor parseConsolePath

**Current structure (simplified):**
```go
func parseConsolePath(path string) (action string, managerID string, sessionID string, err error) {
    parts := strings.Split(strings.Trim(path, "/"), "/")
    // Multiple nested if/else checking different path lengths and segments
    if len(parts) == X { ... }
    if parts[Y] == "something" { ... }
    // etc.
}
```

**Refactored structure:**
```go
// ConsoleRoute represents a parsed console path
type ConsoleRoute struct {
    Action    string // "connect-serial", "connect-graphical", "session", etc.
    ManagerID string
    SessionID string
}

var consolePatterns = []struct {
    Segments []string
    Action   string
}{
    // POST /Managers/{id}/Actions/Oem/Shoal.ConnectSerialConsole
    {[]string{"Managers", "*", "Actions", "Oem", "Shoal.ConnectSerialConsole"}, "connect-serial"},
    // POST /Managers/{id}/Actions/Oem/Shoal.ConnectGraphicalConsole
    {[]string{"Managers", "*", "Actions", "Oem", "Shoal.ConnectGraphicalConsole"}, "connect-graphical"},
    // GET/DELETE /Managers/{id}/Oem/Shoal/ConsoleSessions/{sessionId}
    {[]string{"Managers", "*", "Oem", "Shoal", "ConsoleSessions", "*"}, "session"},
    // GET /Managers/{id}/Oem/Shoal/ConsoleSessions
    {[]string{"Managers", "*", "Oem", "Shoal", "ConsoleSessions"}, "sessions-list"},
    // WebSocket /console/ws/{sessionId}
    {[]string{"console", "ws", "*"}, "websocket"},
}

func parseConsolePath(path string) (ConsoleRoute, error) {
    parts := strings.Split(strings.Trim(path, "/"), "/")

    for _, pattern := range consolePatterns {
        if vars, ok := matchConsolePattern(parts, pattern.Segments); ok {
            return ConsoleRoute{
                Action:    pattern.Action,
                ManagerID: vars.ManagerID,
                SessionID: vars.SessionID,
            }, nil
        }
    }

    return ConsoleRoute{}, fmt.Errorf("unrecognized console path: %s", path)
}
```

### Milestone 5: Extract Proxy Fallback Logic

Create a clean proxy fallback function:

```go
// proxyToBackend handles standard proxy requests that don't match special routes
func (h *Handler) proxyToBackend(w http.ResponseWriter, r *http.Request, path string) {
    bmcName, bmcPath, err := h.resolveBMCPath(path)
    if err != nil {
        h.writeErrorResponse(w, http.StatusNotFound, "Base.1.0.ResourceNotFound", "Resource not found")
        return
    }

    // Add user to context for auditing
    user, _ := h.auth.AuthenticateRequest(r)
    ctx := r.Context()
    if user != nil {
        ctx = context.WithValue(ctx, ctxkeys.User, user)
    }

    // Proxy to BMC
    resp, err := h.bmcSvc.ProxyRequest(ctx, bmcName, bmcPath, r)
    if err != nil {
        slog.Error("Failed to proxy request", "bmc", bmcName, "path", bmcPath, "error", err)
        h.writeErrorResponse(w, http.StatusBadGateway, "Base.1.0.InternalError",
            fmt.Sprintf("Failed to communicate with BMC: %v", err))
        return
    }

    h.copyProxyResponse(w, resp)
}

// resolveBMCPath extracts BMC name and backend path from aggregator path
func (h *Handler) resolveBMCPath(path string) (bmcName, bmcPath string, err error) {
    parts := strings.Split(strings.Trim(path, "/"), "/")
    if len(parts) < 3 {
        return "", "", fmt.Errorf("path too short")
    }

    switch parts[1] {
    case "Managers":
        return h.resolveManagerPath(parts)
    case "Systems":
        return h.resolveSystemPath(parts)
    default:
        return "", "", fmt.Errorf("unknown resource type: %s", parts[1])
    }
}
```

### Milestone 6: Update Handler Signatures

Update handlers to accept `RouteVars`:

**Before:**
```go
func (h *Handler) handleVirtualMediaCollection(w http.ResponseWriter, r *http.Request, bmcName string)
func (h *Handler) handleVirtualMedia(w http.ResponseWriter, r *http.Request, bmcName, mediaID string)
```

**After:**
```go
func (h *Handler) handleVirtualMediaCollection(w http.ResponseWriter, r *http.Request, vars RouteVars) {
    bmcName := vars.BMCName
    // ... rest unchanged
}

func (h *Handler) handleVirtualMedia(w http.ResponseWriter, r *http.Request, vars RouteVars) {
    bmcName := vars.BMCName
    mediaID := vars.MediaID
    // ... rest unchanged
}
```

## File Changes Summary

### New Files

| File | Lines (est.) | Purpose |
|------|--------------|---------|
| `internal/api/routes.go` | ~150 | Route pattern matching infrastructure |

### Modified Files

| File | Changes |
|------|---------|
| `internal/api/proxy.go` | Refactor to use route patterns |
| `internal/api/console.go` | Refactor `parseConsolePath` to use patterns |
| `internal/api/virtual_media.go` | Update handler signatures for `RouteVars` |

### Complexity Reduction

| Function | Before | After |
|----------|--------|-------|
| `handleBMCProxy` | 33 | ~10 |
| `parseConsolePath` | 24 | ~8 |

## Testing

1. **All existing tests must pass**
2. **Path matching tests**: Add unit tests for `matchRoute` and `matchPattern`
3. **Integration verification**: Verify all proxy paths still work
4. **Console path tests**: Verify all console operations work

## Validation

```bash
go run build.go validate
```

## Implementation Notes for AI Agents

1. **License headers required**: All new `.go` files MUST include the AGPLv3 license header as specified in AGENTS.md section 1.4. Use the Go format for all files.
2. **Start with Milestone 1**: Create routes.go with types first
3. **Milestone 3 before 2**: Implement matching before using it
4. **Test matching in isolation**: Add unit tests for `matchPattern`
5. **Preserve behavior exactly**: Same paths, same handlers, same responses
6. **Update tests if handler signatures change**

## Rollback Plan

Revert the commits for this design. The if/else chains will be restored.

## Future Considerations

- Could extend pattern syntax to support optional segments
- Could add method matching to RoutePattern
- Could generate OpenAPI docs from route patterns
