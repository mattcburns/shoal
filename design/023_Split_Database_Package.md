# 024: Split Database Package by Domain

- **Status**: Proposed
- **Author**: Matthew Burns
- **Date**: 2026-01-10

## Summary

This design splits `internal/database/database.go` (1,765 lines, 59 functions) into domain-focused modules. This improves navigation and reduces the context window required to work on specific database operations.

## Motivation

`internal/database/database.go` contains all database operations for:
- BMC CRUD operations
- Connection method management
- User management
- Session management
- Setting descriptors
- Virtual media resources and operations
- Console capabilities and sessions
- Provisioning templates
- Migrations
- Core DB utilities (encryption, settings)

**Problems:**
1. **Large context**: 1,765 lines to load for any DB change
2. **Mixed concerns**: Unrelated domains in one file
3. **Difficult navigation**: 59 functions to scan through
4. **Test organization**: Tests are also in one large file

## Goals

- Split database.go into domain-focused files (~200-300 lines each)
- Keep `DB` struct and core utilities in database.go
- Maintain identical public API and behavior
- Improve findability of specific operations

## Non-Goals

- No schema changes
- No query optimization
- No new functionality
- No changes to transaction handling

## Implementation Plan

### Milestone 1: Create Core Database File

Keep in `internal/database/database.go`:

```go
// database.go - Core database types and utilities

// DB wraps the database connection and provides methods for data access
type DB struct {
    conn      *sql.DB
    encryptor *crypto.Encryptor
}

// New creates a new database connection without encryption
func New(dbPath string) (*DB, error)

// NewWithEncryption creates a new database connection with optional encryption
func NewWithEncryption(dbPath string, encryptionKey string) (*DB, error)

// Close closes the database connection
func (db *DB) Close() error

// encryptIfConfigured encrypts plaintext when an encryptor is configured
func (db *DB) encryptIfConfigured(plaintext string) (string, error)

// decryptIfNeeded attempts to decrypt an encrypted value
func (db *DB) decryptIfNeeded(s string) string

// DisableForeignKeys disables foreign key checks (for testing)
func (db *DB) DisableForeignKeys() error
```

**Estimated size:** ~120 lines

### Milestone 2: Extract Migrations

Create `internal/database/migrate.go`:

```go
// migrate.go - Database schema migrations

// Migrate runs database migrations to ensure schema is current
func (db *DB) Migrate(ctx context.Context) error
```

This contains the entire `Migrate` function with all table creation statements.

**Move from database.go:**
- `Migrate` function (lines 114-305)

**Estimated size:** ~200 lines

### Milestone 3: Extract Settings Operations

Create `internal/database/settings.go`:

```go
// settings.go - Application settings and setting descriptors

// GetSetting retrieves a setting value by key
func (db *DB) GetSetting(ctx context.Context, key string) (string, error)

// SetSetting stores a setting value
func (db *DB) SetSetting(ctx context.Context, key, value string) error

// EnsureServiceUUID ensures a service UUID exists and returns it
func (db *DB) EnsureServiceUUID(ctx context.Context) (string, error)

// UpsertSettingDescriptors stores setting descriptors for a BMC
func (db *DB) UpsertSettingDescriptors(ctx context.Context, bmcName string, descs []models.SettingDescriptor) error

// GetSettingsDescriptors retrieves setting descriptors for a BMC
func (db *DB) GetSettingsDescriptors(ctx context.Context, bmcName, resourceFilter string) ([]models.SettingDescriptor, error)

// GetSettingDescriptor retrieves a single setting descriptor
func (db *DB) GetSettingDescriptor(ctx context.Context, bmcName, descriptorID string) (*models.SettingDescriptor, error)
```

**Move from database.go:**
- `GetSetting` (lines 307-319)
- `SetSetting` (lines 320-328)
- `EnsureServiceUUID` (lines 330-351)
- `crypto_randRead` helper (line 353)
- `UpsertSettingDescriptors` (lines 358-472)
- `GetSettingsDescriptors` (lines 474-563)
- `GetSettingDescriptor` (lines 565-649)

**Estimated size:** ~350 lines

### Milestone 4: Extract BMC Operations

Create `internal/database/bmc.go`:

```go
// bmc.go - BMC CRUD operations

// GetBMCs retrieves all BMCs
func (db *DB) GetBMCs(ctx context.Context) ([]models.BMC, error)

// GetBMC retrieves a BMC by ID
func (db *DB) GetBMC(ctx context.Context, id int64) (*models.BMC, error)

// GetBMCByName retrieves a BMC by name
func (db *DB) GetBMCByName(ctx context.Context, name string) (*models.BMC, error)

// CreateBMC creates a new BMC
func (db *DB) CreateBMC(ctx context.Context, bmc *models.BMC) error

// UpdateBMC updates an existing BMC
func (db *DB) UpdateBMC(ctx context.Context, bmc *models.BMC) error

// DeleteBMC deletes a BMC by ID
func (db *DB) DeleteBMC(ctx context.Context, id int64) error

// UpdateBMCLastSeen updates the last seen timestamp for a BMC
func (db *DB) UpdateBMCLastSeen(ctx context.Context, id int64) error
```

**Move from database.go:**
- `GetBMCs` (lines 651-677)
- `GetBMC` (lines 679-699)
- `GetBMCByName` (lines 701-721)
- `CreateBMC` (lines 723-748)
- `UpdateBMC` (lines 750-766)
- `DeleteBMC` (lines 768-778)
- `UpdateBMCLastSeen` (lines 780-792)

**Estimated size:** ~150 lines

### Milestone 5: Extract Session Operations

Create `internal/database/session.go`:

```go
// session.go - User session management

// CreateSession creates a new session
func (db *DB) CreateSession(ctx context.Context, session *models.Session) error

// GetSessionByToken retrieves a session by token
func (db *DB) GetSessionByToken(ctx context.Context, token string) (*models.Session, error)

// DeleteSession deletes a session by token
func (db *DB) DeleteSession(ctx context.Context, token string) error

// CleanupExpiredSessions removes expired sessions
func (db *DB) CleanupExpiredSessions(ctx context.Context) error

// GetSession retrieves a session by ID
func (db *DB) GetSession(ctx context.Context, id string) (*models.Session, error)

// GetSessions retrieves all sessions
func (db *DB) GetSessions(ctx context.Context) ([]models.Session, error)

// DeleteSessionByID deletes a session by ID
func (db *DB) DeleteSessionByID(ctx context.Context, id string) error
```

**Move from database.go:**
- `CreateSession` (lines 794-804)
- `GetSessionByToken` (lines 806-822)
- `DeleteSession` (lines 824-835)
- `CleanupExpiredSessions` (lines 837-847)
- `GetSession` (lines 849-864)
- `GetSessions` (lines 866-885)
- `DeleteSessionByID` (lines 887-896)

**Estimated size:** ~110 lines

### Milestone 6: Extract User Operations

Create `internal/database/user.go`:

```go
// user.go - User account management

// GetUsers retrieves all users
func (db *DB) GetUsers(ctx context.Context) ([]models.User, error)

// GetUser retrieves a user by ID
func (db *DB) GetUser(ctx context.Context, id string) (*models.User, error)

// GetUserByUsername retrieves a user by username
func (db *DB) GetUserByUsername(ctx context.Context, username string) (*models.User, error)

// CreateUser creates a new user
func (db *DB) CreateUser(ctx context.Context, user *models.User) error

// UpdateUser updates an existing user
func (db *DB) UpdateUser(ctx context.Context, user *models.User) error

// DeleteUser deletes a user by ID
func (db *DB) DeleteUser(ctx context.Context, id string) error

// CountUsers returns the total number of users
func (db *DB) CountUsers(ctx context.Context) (int, error)
```

**Move from database.go:**
- `GetUsers` (lines 906-928)
- `GetUser` (lines 930-947)
- `GetUserByUsername` (lines 949-966)
- `CreateUser` (lines 968-981)
- `UpdateUser` (lines 983-993)
- `DeleteUser` (lines 995-1005)
- `CountUsers` (lines 1007-1020)

**Estimated size:** ~120 lines

### Milestone 7: Extract Connection Method Operations

Create `internal/database/connection_method.go`:

```go
// connection_method.go - Aggregation service connection method operations

// GetConnectionMethods retrieves all connection methods
func (db *DB) GetConnectionMethods(ctx context.Context) ([]models.ConnectionMethod, error)

// GetConnectionMethod retrieves a connection method by ID
func (db *DB) GetConnectionMethod(ctx context.Context, id string) (*models.ConnectionMethod, error)

// CreateConnectionMethod creates a new connection method
func (db *DB) CreateConnectionMethod(ctx context.Context, method *models.ConnectionMethod) error

// UpdateConnectionMethodAggregatedData updates cached aggregated data
func (db *DB) UpdateConnectionMethodAggregatedData(ctx context.Context, id string, managers, systems string) error

// DeleteConnectionMethod deletes a connection method by ID
func (db *DB) DeleteConnectionMethod(ctx context.Context, id string) error

// UpdateConnectionMethodLastSeen updates the last seen timestamp
func (db *DB) UpdateConnectionMethodLastSeen(ctx context.Context, id string) error
```

**Move from database.go:**
- `GetConnectionMethods` (lines 1022-1048)
- `GetConnectionMethod` (lines 1050-1070)
- `CreateConnectionMethod` (lines 1072-1092)
- `UpdateConnectionMethodAggregatedData` (lines 1094-1104)
- `DeleteConnectionMethod` (lines 1106-1117)
- `UpdateConnectionMethodLastSeen` (lines 1119-1150)

**Estimated size:** ~140 lines

### Milestone 8: Extract Virtual Media Operations

Note: Some virtual media types already exist in `internal/database/virtual_media_test.go`. Create `internal/database/virtual_media.go`:

```go
// virtual_media.go - Virtual media resource and operation management

// VirtualMediaResource represents a virtual media slot
type VirtualMediaResource struct { ... }

// VirtualMediaOperation represents a media operation
type VirtualMediaOperation struct { ... }

// GetVirtualMediaResources retrieves virtual media resources
func (db *DB) GetVirtualMediaResources(ctx context.Context, connectionMethodID, managerID string) ([]VirtualMediaResource, error)

// GetVirtualMediaResource retrieves a single virtual media resource
func (db *DB) GetVirtualMediaResource(ctx context.Context, connectionMethodID, managerID, resourceID string) (*VirtualMediaResource, error)

// UpsertVirtualMediaResource creates or updates a virtual media resource
func (db *DB) UpsertVirtualMediaResource(ctx context.Context, ...) error

// CreateVirtualMediaOperation creates a new operation
func (db *DB) CreateVirtualMediaOperation(ctx context.Context, op *VirtualMediaOperation) error

// GetVirtualMediaOperation retrieves an operation by ID
func (db *DB) GetVirtualMediaOperation(ctx context.Context, id int64) (*VirtualMediaOperation, error)

// GetVirtualMediaOperations retrieves operations for a resource
func (db *DB) GetVirtualMediaOperations(ctx context.Context, resourceID int64) ([]VirtualMediaOperation, error)

// UpdateVirtualMediaOperationStatus updates operation status
func (db *DB) UpdateVirtualMediaOperationStatus(ctx context.Context, id int64, status, errorMessage string) error
```

**Move from database.go:**
- `VirtualMediaResource` struct (if defined inline)
- `VirtualMediaOperation` struct (if defined inline)
- `GetVirtualMediaResources` (lines 1152-1205)
- `GetVirtualMediaResource` (lines 1207-1253)
- `UpsertVirtualMediaResource` (lines 1255-1300)
- `CreateVirtualMediaOperation` (lines 1302-1323)
- `GetVirtualMediaOperation` (lines 1325-1360)
- `GetVirtualMediaOperations` (lines 1362-1405)
- `UpdateVirtualMediaOperationStatus` (lines 1407-1429)

**Estimated size:** ~280 lines

### Milestone 9: Extract Provisioning Operations

Create `internal/database/provisioning.go`:

```go
// provisioning.go - Provisioning template management

// ProvisioningTemplate represents a kickstart/preseed template
type ProvisioningTemplate struct { ... }

// GetProvisioningTemplate retrieves a provisioning template
func (db *DB) GetProvisioningTemplate(ctx context.Context, systemID, templateType string) (*ProvisioningTemplate, error)

// UpsertProvisioningTemplate creates or updates a provisioning template
func (db *DB) UpsertProvisioningTemplate(ctx context.Context, systemID, templateType, content string) error

// DeleteProvisioningTemplate deletes a provisioning template
func (db *DB) DeleteProvisioningTemplate(ctx context.Context, systemID, templateType string) error
```

**Move from database.go:**
- `ProvisioningTemplate` struct
- `GetProvisioningTemplate` (lines 1431-1455)
- `UpsertProvisioningTemplate` (lines 1457-1471)
- `DeleteProvisioningTemplate` (lines 1473-1485)

**Estimated size:** ~60 lines

### Milestone 10: Extract Console Operations

Create `internal/database/console.go`:

```go
// console.go - Console capability and session management

// UpsertConsoleCapability stores console capability for a manager
func (db *DB) UpsertConsoleCapability(ctx context.Context, cap *models.ConsoleCapability) error

// GetConsoleCapabilities retrieves console capabilities
func (db *DB) GetConsoleCapabilities(ctx context.Context, connectionMethodID, managerID string) ([]models.ConsoleCapability, error)

// GetConsoleCapability retrieves a single console capability
func (db *DB) GetConsoleCapability(ctx context.Context, connectionMethodID, managerID string, consoleType models.ConsoleType) (*models.ConsoleCapability, error)

// CreateConsoleSession creates a new console session
func (db *DB) CreateConsoleSession(ctx context.Context, session *models.ConsoleSession) error

// GetConsoleSession retrieves a console session by ID
func (db *DB) GetConsoleSession(ctx context.Context, sessionID string) (*models.ConsoleSession, error)

// GetConsoleSessions retrieves console sessions with optional filters
func (db *DB) GetConsoleSessions(ctx context.Context, connectionMethodID string, state models.ConsoleSessionState) ([]models.ConsoleSession, error)

// UpdateConsoleSessionState updates console session state
func (db *DB) UpdateConsoleSessionState(ctx context.Context, sessionID string, state models.ConsoleSessionState, errorMessage string) error

// DeleteConsoleSession deletes a console session
func (db *DB) DeleteConsoleSession(ctx context.Context, sessionID string) error
```

**Move from database.go:**
- `UpsertConsoleCapability` (lines 1487-1510)
- `GetConsoleCapabilities` (lines 1512-1544)
- `GetConsoleCapability` (lines 1546-1571)
- `CreateConsoleSession` (lines 1573-1599)
- `GetConsoleSession` (lines 1601-1646)
- `GetConsoleSessions` (lines 1648-1719)
- `UpdateConsoleSessionState` (lines 1721-1756)
- `DeleteConsoleSession` (lines 1758-1765)

**Estimated size:** ~290 lines

## File Changes Summary

### New Files
| File | Lines (est.) | Purpose |
|------|--------------|---------|
| `internal/database/migrate.go` | ~200 | Schema migrations |
| `internal/database/settings.go` | ~350 | Settings and descriptors |
| `internal/database/bmc.go` | ~150 | BMC CRUD |
| `internal/database/session.go` | ~110 | User sessions |
| `internal/database/user.go` | ~120 | User accounts |
| `internal/database/connection_method.go` | ~140 | Connection methods |
| `internal/database/virtual_media.go` | ~280 | Virtual media |
| `internal/database/provisioning.go` | ~60 | Provisioning templates |
| `internal/database/console.go` | ~290 | Console sessions |

### Modified Files
| File | Before | After |
|------|--------|-------|
| `internal/database/database.go` | 1,765 lines | ~120 lines |

## Testing

1. **All existing tests must pass**: `internal/database/*_test.go`
2. **Consider splitting test file** similarly (optional follow-up)
3. **Verify no import cycles**: All files are in same package

## Validation

```bash
go run build.go validate
```

## Implementation Notes for AI Agents

1. **License headers required**: All new `.go` files MUST include the AGPLv3 license header as specified in AGENTS.md section 1.4. Use the Go format for all files.
2. **Order matters**: Start with `database.go` core, then `migrate.go`
3. **Same package**: No imports needed between files in `internal/database`
4. **Keep function signatures**: Only file location changes
5. **Move related types**: Structs like `VirtualMediaResource` move with their functions
6. **Run tests frequently**: After each milestone

## Rollback Plan

Revert the commits for this design. All code will be restored to database.go.
