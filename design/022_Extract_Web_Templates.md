# 022: Extract Embedded Templates from Web Package

- **Status**: Proposed
- **Author**: Matthew Burns
- **Date**: 2026-01-10

## Summary

This design extracts the ~2,600 lines of embedded HTML/CSS/JavaScript templates from `internal/web/web.go` into separate files. The goal is to reduce the context window required for AI agents and humans to work on either the Go handlers or the frontend templates independently.

## Motivation

`internal/web/web.go` is currently 2,722 lines—the largest file in the codebase (16% of all non-test Go code). The vast majority of this file consists of:

- HTML templates defined as Go string literals
- Inline CSS styling within templates
- ~40 JavaScript functions embedded in template strings

**Problems:**
1. **High context load**: Any change to handlers requires loading 2,700+ lines
2. **No syntax highlighting**: HTML/CSS/JS inside Go strings lack editor support
3. **Difficult to test**: Frontend changes require modifying Go code and rebuilding
4. **Mixed concerns**: Go handler logic interleaved with presentation markup

## Goals

- Extract HTML templates to `internal/assets/templates/*.html`
- Extract JavaScript to `static/js/*.js` files
- Move inline CSS to `static/css/` files
- Reduce `internal/web/web.go` to ~500 lines (handlers and routing only)
- Maintain identical runtime behavior and visual appearance
- Keep templates embedded in binary (use `embed.FS`)

## Non-Goals

- No changes to template logic or visual design
- No JavaScript framework introduction
- No changes to HTTP routes or handler signatures
- No changes to authentication or authorization

## Implementation Plan

### Milestone 1: Create Template Infrastructure

1. Create directory structure:
   ```
   internal/assets/
   ├── assets.go           # Already exists
   ├── templates/
   │   ├── base.html       # Base layout with nav, header, footer
   │   ├── home.html       # Dashboard content
   │   ├── bmcs/
   │   │   ├── list.html   # BMC listing
   │   │   ├── add.html    # Add BMC form
   │   │   ├── edit.html   # Edit BMC form
   │   │   └── details.html # BMC details with tabs
   │   └── users/
   │       ├── list.html   # User listing
   │       ├── add.html    # Add user form
   │       ├── edit.html   # Edit user form
   │       └── profile.html # User profile
   ```

2. Update `internal/assets/assets.go` to embed templates:
   ```go
   //go:embed templates/*.html templates/**/*.html
   var templateFS embed.FS

   func GetTemplateFS() fs.FS {
       return templateFS
   }
   ```

### Milestone 2: Extract Base Template

Extract the base HTML template (lines 100-161 of `web.go`) to `templates/base.html`:

```html
<!DOCTYPE html>
<html>
<head>
    <title>{{.Title}} - Shoal Redfish Aggregator</title>
    <meta charset="utf-8">
    <meta name="viewport" content="width=device-width, initial-scale=1">
    <link rel="stylesheet" href="/static/css/main.css">
</head>
<body>
    <div class="container">
        <div class="header">
            <h1>Shoal Redfish Aggregator</h1>
        </div>
        <nav class="nav">
            <!-- Navigation links -->
        </nav>
        {{template "content" .}}
    </div>
    <script src="/static/js/common.js"></script>
</body>
</html>
```

### Milestone 3: Extract CSS

Create `static/css/main.css` with all inline styles currently in the base template:

```css
body { font-family: Arial, sans-serif; margin: 40px; background-color: #f5f5f5; }
.container { max-width: 1200px; margin: 0 auto; /* ... */ }
/* ... remaining styles ... */
```

### Milestone 4: Extract JavaScript

Create JavaScript files for each functional area:

1. `static/js/common.js` - Shared utilities:
   - `escapeHtml(str)`
   - `formatBytes(bytes)`

2. `static/js/bmc-details.js` - BMC details page:
   - `displaySystemInfo(systemInfo)`
   - `displayNetworkInterfaces(nics)`
   - `displayStorageDevices(devices)`
   - `displaySELEntries(entries)`
   - `loadBMCDetails()`

3. `static/js/settings.js` - Settings tab:
   - `initSettingsTab(bmcName)`
   - Settings filtering, pagination, editing

4. `static/js/virtual-media.js` - Virtual media tab:
   - `initVirtualMediaTab(bmcName)`
   - Media mount/eject operations

5. `static/js/console.js` - Console tab:
   - `initConsoleTab(bmcName)`
   - Console connection handling

6. `static/js/test-connection.js` - Connection testing:
   - `testBMCConnection(bmcId, address, name)`
   - `testConnection()` for add/edit forms

### Milestone 5: Extract Page Templates

For each handler, extract the template string to a file. Example for `handleHome`:

**Before (in web.go):**
```go
func (h *Handler) handleHome(w http.ResponseWriter, r *http.Request) {
    // ... data preparation ...
    homeTemplate := `{{define "content"}}
    <h2>Dashboard</h2>
    <!-- 50+ lines of HTML -->
    {{end}}`
    // ... template execution ...
}
```

**After:**

`templates/home.html`:
```html
{{define "content"}}
<h2>Dashboard</h2>
<!-- 50+ lines of HTML -->
{{end}}
```

`web.go`:
```go
func (h *Handler) handleHome(w http.ResponseWriter, r *http.Request) {
    // ... data preparation ...
    h.renderTemplate(w, "home.html", data)
}
```

### Milestone 6: Refactor Template Loading

Update `loadTemplates()` to parse from embedded filesystem:

```go
func (h *Handler) loadTemplates() {
    templateFS := assets.GetTemplateFS()

    // Parse base template
    h.templates = template.Must(template.ParseFS(templateFS, "templates/base.html"))

    // Parse all page templates
    h.templates = template.Must(h.templates.ParseFS(templateFS,
        "templates/*.html",
        "templates/**/*.html",
    ))
}

func (h *Handler) renderTemplate(w http.ResponseWriter, name string, data PageData) {
    if err := h.templates.ExecuteTemplate(w, name, data); err != nil {
        slog.Error("Failed to execute template", "template", name, "error", err)
        http.Error(w, "Template Error", http.StatusInternalServerError)
    }
}
```

### Milestone 7: Update Script References

Update templates to reference external JS files instead of inline scripts:

```html
<!-- At end of details.html -->
<script src="/static/js/common.js"></script>
<script src="/static/js/bmc-details.js"></script>
<script src="/static/js/settings.js"></script>
<script src="/static/js/virtual-media.js"></script>
<script src="/static/js/console.js"></script>
<script>
    // Only initialization calls remain inline
    document.addEventListener('DOMContentLoaded', function() {
        loadBMCDetails();
    });
</script>
```

## File Changes Summary

### New Files
| File | Purpose |
|------|---------|
| `internal/assets/templates/base.html` | Base layout |
| `internal/assets/templates/home.html` | Dashboard |
| `internal/assets/templates/bmcs/list.html` | BMC listing |
| `internal/assets/templates/bmcs/add.html` | Add BMC form |
| `internal/assets/templates/bmcs/edit.html` | Edit BMC form |
| `internal/assets/templates/bmcs/details.html` | BMC details tabs |
| `internal/assets/templates/users/list.html` | User listing |
| `internal/assets/templates/users/add.html` | Add user form |
| `internal/assets/templates/users/edit.html` | Edit user form |
| `internal/assets/templates/users/profile.html` | Profile page |
| `internal/assets/templates/auth/login.html` | Login page |
| `static/css/main.css` | All CSS styles |
| `static/js/common.js` | Shared utilities |
| `static/js/bmc-details.js` | BMC details logic |
| `static/js/settings.js` | Settings tab logic |
| `static/js/virtual-media.js` | Virtual media logic |
| `static/js/console.js` | Console tab logic |
| `static/js/test-connection.js` | Connection testing |

### Modified Files
| File | Change |
|------|--------|
| `internal/assets/assets.go` | Add template embedding |
| `internal/web/web.go` | Remove inline templates, add `renderTemplate()` |

### Expected Size Reduction

| File | Before | After |
|------|--------|-------|
| `internal/web/web.go` | 2,722 lines | ~400 lines |
| Total template HTML | (embedded) | ~800 lines across files |
| Total JavaScript | (embedded) | ~600 lines across files |
| CSS | (inline) | ~100 lines |

## Testing

1. **Visual Regression**: Manually verify all pages render identically
2. **Existing Tests**: All `internal/web/*_test.go` tests must pass unchanged
3. **Static File Serving**: Verify `/static/js/*.js` and `/static/css/*.css` are served correctly
4. **Template Parsing**: Add test for template loading at startup

## Implementation Notes for AI Agents

1. **License headers required**: All new `.html`, `.css`, and `.js` files MUST include the AGPLv3 license header as specified in AGENTS.md section 1.4. Use the CSS/JS format (block comment style) for all web files.
2. **Milestone order**: Complete milestones sequentially - base template first, then CSS, then JS, then page templates
3. **Test after each milestone**: Verify templates render correctly before moving to next
4. **Preserve behavior**: HTML output should be byte-for-byte identical
5. **Keep template logic**: Don't change any {{.}} template variables or flow control
6. **Embedded FS**: Remember to update `assets.go` with `//go:embed` directive

## Validation

Run the full validation pipeline:
```bash
go run build.go validate
```

## Rollback Plan

Revert the commits for this design. The templates were embedded in Go strings and will be restored.

## Future Considerations

- Consider adding template caching with file watching for development mode
- Could add minification for production builds
- Template inheritance could be expanded for more complex layouts
