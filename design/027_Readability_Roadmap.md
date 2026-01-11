# 027: Code Readability Improvement Roadmap

- **Status**: Proposed
- **Author**: Matthew Burns
- **Date**: 2026-01-10

## Summary

This document provides an overview of the code readability improvements planned for the Shoal codebase. It serves as a roadmap and index for related design documents (022-026).

## Motivation

An analysis of the codebase identified several areas where large file sizes and high cyclomatic complexity make the code difficult for both AI agents and humans to work with:

| File | Lines | % of Codebase | Primary Issue |
|------|-------|---------------|---------------|
| `internal/web/web.go` | 2,722 | 16% | Embedded templates |
| `internal/bmc/service.go` | 1,892 | 11% | Complexity 77 function |
| `internal/database/database.go` | 1,765 | 11% | 59 functions in one file |

Together, these three files account for **38% of the non-test codebase** (6,379 of 16,738 lines).

## Design Documents

### [022: Complete API Error Helper Migration](022_Complete_Error_Helper_Migration.md)

**Target:** `internal/api/errors.go`, `respond.go`, `api.go`

Completes the migration started in Design 019:
- Removes duplicate `validMessageIDs` definitions
- Renames `rf*`-prefixed helpers (transition complete)
- Updates all handlers to use centralized helpers

**Milestones:** 7 | **Estimated effort:** Small

---

### [023: Split Database Package by Domain](023_Split_Database_Package.md)

**Target:** `internal/database/database.go` (1,765 → ~120 lines)

Organizes 59 database functions into domain-specific files:
- `migrate.go` - Schema migrations
- `settings.go` - Settings and descriptors
- `bmc.go` - BMC CRUD
- `session.go` - User sessions
- `user.go` - User accounts
- `connection_method.go` - Connection methods
- `virtual_media.go` - Virtual media
- `provisioning.go` - Provisioning templates
- `console.go` - Console sessions

**Milestones:** 10 | **Estimated effort:** Medium

---

### [024: Split BMC Service into Focused Modules](024_Split_BMC_Service.md)

**Target:** `internal/bmc/service.go` (1,892 → ~400 lines)

Splits the monolithic service file into domain-focused modules:
- `http.go` - HTTP request helpers
- `discovery.go` - ID discovery with caching
- `settings.go` - Settings discovery (refactored)
- `registry.go` - Attribute registry enrichment
- `status.go` - Detailed status retrieval
- `update.go` - Setting update operations

**Key improvement:** Reduces `DiscoverSettings` complexity from 77 to ~10

**Milestones:** 7 | **Estimated effort:** Large

---

### [025: Refactor API Console and Proxy Routing](025_Refactor_Proxy_Routing.md)

**Target:** `internal/api/proxy.go`, `console.go`

Replaces nested if/else routing with declarative pattern matching:
- Reduces `handleBMCProxy` complexity from 33 to ~10
- Reduces `parseConsolePath` complexity from 24 to ~8
- Introduces `RoutePattern` type for scannable route definitions

**Milestones:** 6 | **Estimated effort:** Medium

---

### [026: Extract Embedded Templates from Web Package](026_Extract_Web_Templates.md)

**Target:** `internal/web/web.go` (2,722 → ~400 lines)

Extracts ~2,600 lines of HTML/CSS/JavaScript from Go string literals into:
- `internal/assets/templates/*.html` - HTML templates
- `static/js/*.js` - JavaScript files
- `static/css/main.css` - Stylesheets

**Milestones:** 7 | **Estimated effort:** Large

---

## Recommended Implementation Order

The designs are numbered for reference but should be implemented in this order for minimal conflicts:

### Phase 1: Quick Wins (Low Risk)

1. **[022: Complete API Error Helper Migration](022_Complete_Error_Helper_Migration.md)**
   - Small scope, no structural changes
   - Cleans up technical debt from Design 019
   - Good warmup for larger refactors

### Phase 2: Database Split (Medium Risk)

2. **[023: Split Database Package by Domain](023_Split_Database_Package.md)**
   - All files stay in same package (no import changes)
   - Purely mechanical file moves
   - Sets pattern for other splits

### Phase 3: BMC Service Split (Medium Risk)

3. **[024: Split BMC Service into Focused Modules](024_Split_BMC_Service.md)**
   - Same-package split like database
   - Refactors `DiscoverSettings` (most complex function)
   - Should be done before template extraction

### Phase 4: Routing Refactor (Medium Risk)

4. **[025: Refactor API Console and Proxy Routing](025_Refactor_Proxy_Routing.md)**
   - Self-contained within api package
   - Reduces cognitive load for proxy/console code
   - Can be done in parallel with Phase 5

### Phase 5: Template Extraction (Higher Risk)

5. **[026: Extract Embedded Templates from Web Package](026_Extract_Web_Templates.md)**
   - Largest structural change
   - Requires manual visual testing
   - Best done last when other code is stable

## Validation

After each design is implemented, run the full validation pipeline:

```bash
go run build.go validate
```

## Success Metrics

After all designs are implemented:

| Metric | Before | After |
|--------|--------|-------|
| Largest file (lines) | 2,722 | ~400 |
| Files >1000 lines | 3 | 0 |
| Max cyclomatic complexity | 77 | ~15 |
| Functions with complexity >20 | 8+ | 0 |

## Dependencies

- Each design document is self-contained
- No external dependencies introduced
- All changes are internal refactoring

## Rollback

Each design can be rolled back independently by reverting its commits. The designs do not have hard dependencies on each other (though implementing them in order reduces merge conflicts).
