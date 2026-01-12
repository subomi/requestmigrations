# RequestMigrations v2: Type-Based Migration System

## Design Document

**Status:** In Progress  
**Author:** Subomi Oluwalana  
**Last Updated:** January 2026

---

## Table of Contents

1. [Overview](#overview)
2. [Motivation](#motivation)
3. [Design Goals](#design-goals)
4. [Architecture](#architecture)
5. [Core Concepts](#core-concepts)
6. [API Reference](#api-reference)
7. [Migration Flow](#migration-flow)
8. [Performance & Caching](#performance--caching)
9. [Usage Examples](#usage-examples)
10. [Current State](#current-state)
11. [Remaining Work](#remaining-work)

---

## Overview

RequestMigrations v2 introduces a **type-based migration system** that replaces the original handler-based approach. Instead of defining migrations per API handler (e.g., `createUserRequestMigration`), migrations are now defined per **Go type** (e.g., `AddressMigration` for the `Address` struct).

The system automatically builds a **type dependency graph** to handle nested structs and cycles, applying migrations recursively through the object graph with cycle detection.

---

## Motivation

### Problems with v1 (Handler-Based Approach)

```go
// v1: Handler-name-based migrations
migrations := MigrationStore{
    "2023-05-01": []Migration{
        &ListUserResponseMigration{},    // Tied to "ListUser" handler
        &GetUserResponseMigration{},     // Tied to "GetUser" handler  
        &CreateUserRequestMigration{},   // Tied to "createUser" handler
    },
}
```

**Issues:**

1. **Duplication**: If `User` type changes, you need separate migrations for every handler that returns a `User` (`GetUser`, `ListUsers`, `SearchUsers`, etc.)

2. **Naming Convention Fragility**: Migrations are matched by reflection on struct names (e.g., `createUserRequestSplitNameMigration` must match handler name `createUser` + type `request`)

3. **No Nested Type Support**: If `User` contains an `Address` struct that also needs migration, you must handle it manually in every handler migration

4. **Tight Coupling**: Business logic (handlers) and data transformation (migrations) are tightly coupled

### Solution: Type-Based Migrations

```go
// v2: Type-based migrations
migrations := []TypeMigration{
    &AddressMigration{},  // Migrates Address type everywhere it appears
    &UserMigration{},     // Migrates User type everywhere it appears
}
```

**Benefits:**

1. **DRY**: Define migration once per type, applied everywhere that type appears
2. **Automatic Nesting**: System builds type graph and migrates nested types automatically
3. **Decoupled**: Migrations are tied to data structures, not handlers
4. **Composable**: Complex types with nested structs "just work"

---

## Design Goals

1. **Type-Centric**: Migrations should be defined per Go type, not per handler
2. **Automatic Graph Resolution**: Nested types should be migrated automatically
3. **Bidirectional**: Support both forward (request) and backward (response) migrations
4. **Drop-in JSON Replacement**: `Marshal`/`Unmarshal` API mirrors `encoding/json`
5. **Version-Aware**: Migrations only apply when user version differs from current version
6. **Observable**: Prometheus metrics for migration latency

---

## Architecture

```
┌─────────────────────────────────────────────────────────────────┐
│                         RequestMigration                         │
│  ┌─────────────────┐  ┌─────────────────┐  ┌─────────────────┐  │
│  │ Options         │  │ Versions []     │  │ Graph Cache     │  │
│  │ - VersionHeader │  │ [v0, v1, v2...] │  │ map[T+V]Graph   │  │
│  │ - CurrentVersion│  │                 │  └─────────────────┘  │
│  │ - VersionFormat │  └─────────────────┘           │           │
│  └─────────────────┘                                │           │
│           │                                         │           │
│           ▼                                         ▼           │
│  ┌───────────────────────────────────────────────────────────┐  │
│  │ Internal Type-Centric Storage                             │  │
│  │ map[reflect.Type]map[string]TypeMigration                 │  │
│  └───────────────────────────────────────────────────────────┘  │
│           │                                                     │
│           ▼                                                     │
│  ┌───────────────────────────────────────────────────────────┐  │
│  │ Request Context (optional)                                │  │
│  │ - *http.Request                                           │  │
│  └───────────────────────────────────────────────────────────┘  │
└─────────────────────────────────────────────────────────────────┘
```

The system is now unified into a single `RequestMigration` struct. Long-lived instances (created via `NewRequestMigration`) hold global configuration and internal storage. Request-scoped instances (created via `WithUserVersion`) are shallow copies that bind a specific `*http.Request` context for `Marshal` and `Unmarshal` operations.

---

## Core Concepts

### 1. TypeMigration Interface

The fundamental building block. Each migration handles one Go type:

```go
type TypeMigration interface {
    // MigrateForward transforms data from old version to new (for requests)
    MigrateForward(data any) (any, error)

    // MigrateBackward transforms data from new version to old (for responses)
    MigrateBackward(data any) (any, error)
}
```

**Key Insight**: The interface no longer requires a `Type()` method. The association between a Go type and its migration is now handled at the registration level using generics.

### 2. Registration with Generics

We use a generic helper function to register migrations. This removes boilerplate from migration structs and provides compile-time type safety.

```go
// Registration API
rms.Register[User](rm, "2024-01-01", &UserMigration{})
```

**Benefits**:
- **Zero Boilerplate**: Migration structs focus only on transformation.
- **Explicit Mapping**: The relationship between type and migration is visible at the registration site.
- **Type Safety**: No need to pass empty instances or use string-based names.

---

### 3. TypeGraph

A graph structure representing type dependencies with cycle detection and support for custom types:

```go
type TypeGraph struct {
    Type       reflect.Type           // The Go type (e.g., User)
    Fields     map[string]*TypeGraph  // Nested fields. Uses JSON tags for keys. 
                                      // Special key "__elem" used for slice elements.
    Migrations []TypeMigration        // Migrations for this type (applied in sequence)
}
```

**Type Detection Logic:**
- **Any Type**: The system detects migrations for structs, slices, and custom primitive types (e.g., `type Email string`).
- **Leaf Nodes**: If a field's type has a registered migration (checked via `findMigrationsForType`), it is included in the graph even if it isn't a struct.
- **Recursive filtering**: A node is only added to the `Fields` map if `HasMigrations()` is true for its subtree.
- **JSON Tag Support**: Field keys in the graph are derived from `json` struct tags to match JSON-decoded maps.

**Traversal Techniques Applied:**
- **Depth-First Search (DFS)**: Recursive traversal through struct fields and custom types.
- **Cycle Detection & Memoization**: Uses a `visited` map (`map[reflect.Type]*TypeGraph`) during graph construction to handle circular references (e.g., `User → Workspace → User`) and ensure each type is processed once.
- **In-place Updates**: Uses `*any` pointers during migration traversal to allow direct modification of the decoded data.

**Example**: For this type hierarchy with cycles and custom types:

```go
type AddressString string // Custom primitive with migration

type User struct {
    ID        string
    Address   AddressString // Leaf node migration
    Workspace *Workspace    // Nested struct
}

type Workspace struct {
    ID    string
    Users []*User  // Cycle back to User!
}
```

The TypeGraph handles both cases:

```
Order (no migration)
└── Customer → User
    ├── Address → AddressString (has AddressMigration) ✓ Leaf node!
    └── Workspace → Workspace
        └── Users → User (cycle! skipped, already visited) ✓ Cycle!
```

### 4. Fluent API

`RequestMigration` provides a fluent API for request-scoped operations:

```go
// Bind a request context
scopedRM := rm.WithUserVersion(req)

// Perform migrations
data, err := scopedRM.Marshal(&myStruct)
```

This enables the fluent API: `rm.WithUserVersion(req).Marshal(&data)`. If `Marshal` or `Unmarshal` are called directly on a global instance without a request context, they default to using the `CurrentVersion`.

---

## Performance & Caching

The system uses a two-tier optimization strategy to ensure low-latency migrations:

### 1. The Internal Pivot (Write-time)
While the public API is **Version-Centric** (registering changes by release date), the internal storage is **Type-Centric**. 
- **Registration**: `rms.Register[User](v1, mig)` immediately inserts the migration into a map indexed by `reflect.TypeOf(User{})`.
- **Advantage**: During graph construction, we only look up the types present in the struct once, immediately retrieving their entire migration history.

### 2. Graph Caching (Request-time)
Since Go types and registered migrations are static after startup, the `TypeGraph` for any given `(Type, Version)` pair is deterministic.
- **Mechanism**: A thread-safe cache (`map[graphCacheKey]*TypeGraph`) stores compiled graphs.
- **Workflow**: 
    1. Check cache for `(Type, UserVersion)`.
    2. On hit: Reuse graph (O(1)).
    3. On miss: Build graph via DFS, then cache it.

---

## Graph Traversal Techniques

The TypeGraph uses **Depth-First Search (DFS)** with cycle detection for traversing type relationships:

### Primary Traversal: DFS with Cycle Detection
- **Algorithm**: Recursive depth-first traversal through struct fields
- **Cycle Prevention**: `visitedTypes map[reflect.Type]bool` prevents revisiting types
- **Memoization**: Each type is processed only once per request, ensuring consistent migration
- **Termination**: Stops when all reachable types are processed or cycles are detected

### Migration Order Guarantees
- **Forward Migration (Unmarshal)**: Bottom-up - nested types migrated before parent types. Migrations for a single type are applied oldest to newest.
- **Backward Migration (Marshal)**: Top-down - parent types migrated before nested types. Migrations for a single type are applied newest to oldest.

### Example Traversal Path
```
Start: Order struct
├── DFS: Order.Customer (User type)
│   ├── Check visited: User not visited → process User
│   │   ├── DFS: User.Workspace (Workspace type)
│   │   │   ├── Check visited: Workspace not visited → process Workspace
│   │   │   │   ├── DFS: Workspace.Users ([]User type)
│   │   │   │   │   ├── Check visited: User already visited → skip (cycle!)
│   │   │   │   │   └── Return to Workspace
│   │   │   └── Return to User
│   └── Return to Order
└── Continue with other Order fields...
```

---

## API Reference

### Initialization

```go
rm, err := NewRequestMigration(&RequestMigrationOptions{
    VersionHeader:  "X-API-Version",     // Header to read user version from
    CurrentVersion: "2024-01-01",        // Your API's current version
    VersionFormat:  DateFormat,          // or SemverFormat
})
```

### Registering Migrations

```go
// Register migrations for a specific version
rms.Register[Address](rm, "2024-01-01", &AddressMigration{})
rms.Register[User](rm, "2024-01-01", &UserMigration{})
```

### Using in Handlers

```go
func GetUser(w http.ResponseWriter, r *http.Request) {
    user := fetchUser()  // Your business logic returns current version struct
    
    // Marshal with automatic backward migration for older clients
    data, err := rm.WithUserVersion(r).Marshal(&user)
    if err != nil {
        http.Error(w, err.Error(), 500)
        return
    }
    
    w.Write(data)
}

func CreateUser(w http.ResponseWriter, r *http.Request) {
    body, _ := io.ReadAll(r.Body)
    
    var user User
    // Unmarshal with automatic forward migration from older clients
    err := rm.WithUserVersion(r).Unmarshal(body, &user)
    if err != nil {
        http.Error(w, err.Error(), 400)
        return
    }
    
    // user is now in current version format
    saveUser(user)
}
```

---

## Migration Flow

### Marshal (Response → Client)

For sending responses to clients on older API versions:

```
Current Version Struct
        │
        ▼
   json.Marshal()
        │
        ▼
  map[string]any
        │
        ▼
┌───────────────────┐
│  Build TypeGraph  │ ← Finds all nested types with migrations
└───────────────────┘
        │
        ▼
┌───────────────────┐
│ migrateBackward() │ ← Applies migrations top-down
│  1. Current type  │    (parent first, then children)
│  2. Nested fields │
└───────────────────┘
        │
        ▼
   json.Marshal()
        │
        ▼
    []byte (old format)
```

### Unmarshal (Client Request →)

For receiving requests from clients on older API versions:

```
    []byte (old format)
        │
        ▼
  json.Unmarshal()
        │
        ▼
  map[string]any
        │
        ▼
┌───────────────────┐
│  Build TypeGraph  │
└───────────────────┘
        │
        ▼
┌───────────────────┐
│ migrateForward()  │ ← Applies migrations bottom-up
│  1. Nested fields │    (children first, then parent)
│  2. Current type  │
└───────────────────┘
        │
        ▼
   json.Marshal()
        │
        ▼
  json.Unmarshal()
        │
        ▼
Current Version Struct
```

---

## Usage Examples

### Example 1: Simple Field Rename

**Scenario**: In v2, `full_name` was split into `first_name` and `last_name`

```go
// v1 (old)
type UserV1 struct {
    Email    string `json:"email"`
    FullName string `json:"full_name"`
}

// v2 (current)
type User struct {
    Email     string `json:"email"`
    FirstName string `json:"first_name"`
    LastName  string `json:"last_name"`
}

type UserMigration struct{}

func (m *UserMigration) MigrateForward(data any) (any, error) {
    d := data.(map[string]interface{})
    
    // Convert old format to new
    if fullName, ok := d["full_name"].(string); ok {
        parts := strings.Split(fullName, " ")
        d["first_name"] = parts[0]
        d["last_name"] = parts[1]
        delete(d, "full_name")
    }
    
    return d, nil
}

func (m *UserMigration) MigrateBackward(data any) (any, error) {
    d := data.(map[string]interface{})
    
    // Convert new format to old
    firstName, _ := d["first_name"].(string)
    lastName, _ := d["last_name"].(string)
    d["full_name"] = firstName + " " + lastName
    delete(d, "first_name")
    delete(d, "last_name")
    
    return d, nil
}
```

### Example 2: Nested Type Migration

**Scenario**: `Address` changed from a string to a structured object

```go
// v1: Address was a string
type UserV1 struct {
    Name    string `json:"name"`
    Address string `json:"address"`  // "123 Main St, London, UK"
}

// v2: Address is now a struct
type User struct {
    Name    string   `json:"name"`
    Address *Address `json:"address"`
}

type Address struct {
    Street  string `json:"street"`
    City    string `json:"city"`
    Country string `json:"country"`
}

type AddressMigration struct{}

func (m *AddressMigration) MigrateForward(data any) (any, error) {
    // If it's a string (old format), parse it
    if str, ok := data.(string); ok {
        parts := strings.Split(str, ", ")
        return map[string]interface{}{
            "street":  parts[0],
            "city":    parts[1],
            "country": parts[2],
        }, nil
    }
    return data, nil
}

func (m *AddressMigration) MigrateBackward(data any) (any, error) {
    d := data.(map[string]interface{})
    
    // Convert struct back to string for old clients
    return fmt.Sprintf("%s, %s, %s", 
        d["street"], d["city"], d["country"]), nil
}
```

---

## Current State

### ✅ Implemented

- [x] `TypeMigration` interface
- [x] Unified `RequestMigration` struct
- [x] `TypeGraph` construction via `buildTypeGraph()`
- [x] `Marshal()` method with backward migration
- [x] `Unmarshal()` method with forward migration
- [x] `Register[T]()` generic function for type-based registration
- [x] Nested struct field traversal with JSON tag support
- [x] Array/slice handling for nested types and root types (via `__elem` mapping)
- [x] Cycle detection & memoization in graph construction
- [x] Support for custom primitive types (e.g., `type Address string`)
- [x] Version range support (chaining multiple versions)
- [x] Version sorting (date and semver)

### 🐛 Known Bugs

*No critical bugs known at this time.*

### 🚧 Remaining Work

1. **🔴 Implementation Pivot**: Refactor internal storage to `map[reflect.Type]map[string]TypeMigration` and implement the `graphCache`.

2. **Pointer Field Handling**: Ensure nil pointer fields in structs are handled gracefully during traversal.

3. **Test Coverage**:
   - Multi-version chain tests
   - Error case tests for invalid JSON
   - **Performance benchmarks** for cache hits vs misses

4. **Documentation**: Update README.md with the new v2 API examples and migration guide.

5. **Backward Compatibility**: Decide on the final status of the v1 handler-based API (deprecate or remove).

---

## Migration Path from v1 to v2

For existing users of v1:

```go
// v1 (old way)
err, vw, rollback := rm.Migrate(r, "getUser")
defer rollback(w)
// ... handler logic ...
vw.Write(body)

// v2 (new way)
// ... handler logic ...
body, err := rm.WithUserVersion(r).Marshal(&user)
w.Write(body)
```

---

## Open Questions

1. **Should we support partial migrations?**
   - Only migrate specific fields, leave others unchanged?

2. **Cycle handling refinement**: Should we warn users about detected cycles, or handle them silently? Should there be configuration options for cycle behavior?

---

## References

- [Stripe API Versioning](https://stripe.com/blog/api-versioning)
- [Ruby request_migrations](https://github.com/keygen-sh/request_migrations)
- [Convoy](https://github.com/frain-dev/convoy) - Production user of v1
