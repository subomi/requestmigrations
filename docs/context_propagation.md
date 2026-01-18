# Context Propagation Design

**Status:** Implemented  
**Author:** Subomi Oluwalana  
**Date:** January 2026

---

## Table of Contents

1. [Overview](#overview)
2. [Problem](#problem)
3. [Solution: `rm.For(r)` Pattern](#solution-rmforr-pattern)
   - [Type Definitions](#type-definitions)
   - [API Surface](#api-surface)
   - [Implementation](#implementation)
   - [Usage Examples](#usage-examples)
4. [Implementation Plan](#implementation-plan)
5. [Open Questions](#open-questions)

---

## Overview

This document describes how to allow migrations to access request-scoped data (HTTP request, context, user version) without changing the external `Unmarshal` and `Marshal` API signatures. The internal `TypeMigration` interface may be modified if it provides a cleaner design.

---

## Problem

Migrations may need access to request-scoped data for:
- Logging with request context
- Tenant-specific migration logic
- Feature flags based on user/request headers
- Cancellation via `context.Context`

The current `TypeMigration` interface mirrors `encoding/json`'s simplicity:

```go
type TypeMigration interface {
    MigrateForward(data any) (any, error)
    MigrateBackward(data any) (any, error)
}
```

The current external API uses optional method chaining:

```go
// Current API - request is optional (problematic)
rm.WithUserVersion(r).Unmarshal(data, &v)
rm.Marshal(v)  // Works but skips migrations silently
```

We want to redesign the API so that:
1. Request context is **required** (not optional)
2. `Marshal`/`Unmarshal` signatures still mirror `encoding/json`
3. Context is propagated to migrations

---

## Solution: `rm.For(r)` Pattern

### Overview

Introduce a `Migrator` type that holds request-scoped state. The `For(r)` method creates a `Migrator` for a specific request, and `Marshal`/`Unmarshal` live on `Migrator`.

```go
// Usage
rm.For(r).Unmarshal(data, &v)
rm.For(r).Marshal(v)
```

This makes the request **required by design** - you can't call `Marshal`/`Unmarshal` without first calling `For(r)`.

### Type Definitions

```go
// RequestMigration holds configuration and registered migrations.
// It does NOT have Marshal/Unmarshal methods directly.
type RequestMigration struct {
    opts         *RequestMigrationOptions
    versions     []*Version
    mu           *sync.Mutex
    migrations   map[reflect.Type]map[string]TypeMigration
    graphBuilder *TypeGraphBuilder
    // ... other config fields
}

// Migrator is a request-scoped handle for performing migrations.
// Created via RequestMigration.For(r).
type Migrator struct {
    rm          *RequestMigration
    ctx         context.Context
    userVersion *Version
}

// Context key for user version.
// Uses unexported type to prevent key collisions (standard Go pattern).
type userVersionKey struct{}

// UserVersionFromContext retrieves the user's API version from a migration context.
// Returns nil if not present.
func UserVersionFromContext(ctx context.Context) *Version {
    if v, ok := ctx.Value(userVersionKey{}).(*Version); ok {
        return v
    }
    return nil
}

// withUserVersion returns a new context with the user version attached.
// This is internal - callers don't need to use this directly.
func withUserVersion(ctx context.Context, version *Version) context.Context {
    return context.WithValue(ctx, userVersionKey{}, version)
}
```

### API Surface

```go
// RequestMigration methods (configuration)
func NewRequestMigration(opts *RequestMigrationOptions) (*RequestMigration, error)
func Register[T any](rm *RequestMigration, version string, m TypeMigration) error
func (rm *RequestMigration) RegisterMetrics(reg *prometheus.Registry)
func (rm *RequestMigration) WriteVersionHeader() func(next http.Handler) http.Handler

// The key method - creates a request-scoped Migrator
func (rm *RequestMigration) For(r *http.Request) (*Migrator, error)

// Bind is an alias for For
func (rm *RequestMigration) Bind(r *http.Request) (*Migrator, error)

// Migrator methods (json-like API)
func (m *Migrator) Marshal(v interface{}) ([]byte, error)
func (m *Migrator) Unmarshal(data []byte, v interface{}) error

// Context helper function (for use inside migrations)
func UserVersionFromContext(ctx context.Context) *Version
```

### Implementation

#### For() Method

```go
func (rm *RequestMigration) For(r *http.Request) (*Migrator, error) {
    if r == nil {
        return nil, errors.New("request cannot be nil")
    }
    
    userVersion, err := rm.getUserVersion(r)
    if err != nil {
        return nil, err
    }
    
    // Use request's context directly, only add user version
    ctx := withUserVersion(r.Context(), userVersion)
    
    return &Migrator{
        rm:          rm,
        ctx:         ctx,
        userVersion: userVersion,
    }, nil
}
```

#### Migrator.Unmarshal()

```go
func (m *Migrator) Unmarshal(data []byte, v interface{}) error {
    t := reflect.TypeOf(v)
    if t.Kind() != reflect.Ptr {
        return errors.New("v must be a pointer")
    }
    
    graph, err := m.rm.graphBuilder.Build(t, m.userVersion)
    if err != nil {
        return err
    }
    
    if !graph.HasMigrations() {
        return json.Unmarshal(data, v)
    }
    
    var intermediate any
    if err := json.Unmarshal(data, &intermediate); err != nil {
        return err
    }
    
    // Pass context.Context to the graph for migrations to access
    if err := graph.MigrateForward(m.ctx, &intermediate); err != nil {
        return err
    }
    
    data, err = json.Marshal(intermediate)
    if err != nil {
        return err
    }
    
    return json.Unmarshal(data, v)
}
```

#### Migrator.Marshal()

```go
func (m *Migrator) Marshal(v interface{}) ([]byte, error) {
    graph, err := m.rm.graphBuilder.BuildFromValue(reflect.ValueOf(v), m.userVersion)
    if err != nil {
        return nil, err
    }
    
    if !graph.HasMigrations() {
        return json.Marshal(v)
    }
    
    data, err := json.Marshal(v)
    if err != nil {
        return nil, err
    }
    
    var intermediate any
    if err := json.Unmarshal(data, &intermediate); err != nil {
        return nil, err
    }
    
    // Pass context.Context to the graph for migrations to access
    if err := graph.MigrateBackward(m.ctx, &intermediate); err != nil {
        return nil, err
    }
    
    return json.Marshal(intermediate)
}
```

#### Updated TypeMigration Interface

```go
// TypeMigration defines how to migrate a specific type.
// The context.Context parameter provides:
// - Cancellation/deadline from the original request (r.Context())
// - User version via UserVersionFromContext(ctx)
// - Any custom values the caller adds via context.WithValue()
type TypeMigration interface {
    MigrateForward(ctx context.Context, data any) (any, error)
    MigrateBackward(ctx context.Context, data any) (any, error)
}
```

#### Updated TypeGraph Methods

```go
func (g *TypeGraph) MigrateForward(ctx context.Context, data *any) error {
    val := *data
    if val == nil {
        return nil
    }
    
    // Handle nested fields first
    switch v := val.(type) {
    case map[string]interface{}:
        for fieldName, fieldGraph := range g.Fields {
            if fieldName == "__elem" {
                continue
            }
            fieldData, ok := v[fieldName]
            if !ok || fieldData == nil {
                continue
            }
            if err := fieldGraph.MigrateForward(ctx, &fieldData); err != nil {
                return err
            }
            v[fieldName] = fieldData
        }
    case []interface{}:
        elemGraph := g.Fields["__elem"]
        if elemGraph != nil {
            for i := range v {
                if err := elemGraph.MigrateForward(ctx, &v[i]); err != nil {
                    return err
                }
            }
        }
    }
    
    // Apply migrations with context
    for _, m := range g.Migrations {
        migratedData, err := m.MigrateForward(ctx, *data)
        if err != nil {
            return err
        }
        *data = migratedData
    }
    
    return nil
}
```

### Usage Examples

#### Handler Code

```go
func CreateUserHandler(rm *RequestMigration) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        // Create request-scoped migrator
        migrator, err := rm.For(r)
        if err != nil {
            http.Error(w, err.Error(), http.StatusBadRequest)
            return
        }
        
        // Unmarshal request body
        var req CreateUserRequest
        body, _ := io.ReadAll(r.Body)
        if err := migrator.Unmarshal(body, &req); err != nil {
            http.Error(w, err.Error(), http.StatusBadRequest)
            return
        }
        
        // ... business logic ...
        
        // Marshal response
        resp := CreateUserResponse{ID: "123", Name: req.Name}
        data, err := migrator.Marshal(resp)
        if err != nil {
            http.Error(w, err.Error(), http.StatusInternalServerError)
            return
        }
        
        w.Header().Set("Content-Type", "application/json")
        w.Write(data)
    }
}
```

#### Migration Code

```go
type UserV2Migration struct{}

func (m *UserV2Migration) MigrateForward(ctx context.Context, data any) (any, error) {
    d := data.(map[string]any)
    
    // Access user version via helper function
    userVersion := requestmigrations.UserVersionFromContext(ctx)
    log.Printf("Migrating user for version %s", userVersion.String())
    
    // Check for cancellation (standard context pattern)
    select {
    case <-ctx.Done():
        return nil, ctx.Err()
    default:
    }
    
    // Transform: split "name" into "first_name" and "last_name"
    if name, ok := d["name"].(string); ok {
        parts := strings.SplitN(name, " ", 2)
        d["first_name"] = parts[0]
        if len(parts) > 1 {
            d["last_name"] = parts[1]
        }
        delete(d, "name")
    }
    
    return d, nil
}

func (m *UserV2Migration) MigrateBackward(ctx context.Context, data any) (any, error) {
    d := data.(map[string]any)
    
    // Transform: combine "first_name" and "last_name" into "name"
    firstName, _ := d["first_name"].(string)
    lastName, _ := d["last_name"].(string)
    d["name"] = strings.TrimSpace(firstName + " " + lastName)
    delete(d, "first_name")
    delete(d, "last_name")
    
    return d, nil
}
```

#### Inter-Migration Communication

Migrations can pass data to downstream migrations by adding values to the context. 
Use private key types to avoid collisions (standard `context.WithValue` pattern).

**Note:** Since `context.Context` is immutable, inter-migration communication requires
a different approach than a mutable map. Options include:

1. **Store state in the data itself** (recommended for most cases)
2. **Use a shared mutable container passed via context** (for complex cases)

```go
// Option 1: Store computed values in the data being migrated
type ComputedFieldMigration struct{}

func (m *ComputedFieldMigration) MigrateForward(ctx context.Context, data any) (any, error) {
    d := data.(map[string]any)
    
    // Store computed value directly in the data
    d["__computed_hash"] = computeHash(d)
    
    return d, nil
}

type DependentMigration struct{}

func (m *DependentMigration) MigrateForward(ctx context.Context, data any) (any, error) {
    d := data.(map[string]any)
    
    // Read value set by earlier migration
    if hash, ok := d["__computed_hash"]; ok {
        d["hash"] = hash
        delete(d, "__computed_hash") // Clean up internal field
    }
    
    return d, nil
}
```

```go
// Option 2: Use a shared container for complex inter-migration state
type migrationStateKey struct{}

type MigrationState struct {
    mu     sync.Mutex
    values map[string]any
}

func (s *MigrationState) Set(key string, value any) {
    s.mu.Lock()
    defer s.mu.Unlock()
    s.values[key] = value
}

func (s *MigrationState) Get(key string) any {
    s.mu.Lock()
    defer s.mu.Unlock()
    return s.values[key]
}

// In Migrator.For():
state := &MigrationState{values: make(map[string]any)}
ctx = context.WithValue(ctx, migrationStateKey{}, state)

// In migrations:
func (m *MyMigration) MigrateForward(ctx context.Context, data any) (any, error) {
    state := ctx.Value(migrationStateKey{}).(*MigrationState)
    state.Set("computed_hash", computeHash(data))
    return data, nil
}
```

---

## Implementation Plan

### Phase 1: Core Types

1. Add context key type (`userVersionKey`)
2. Add helper functions: `UserVersionFromContext()`, `withUserVersion()` (internal)
3. Add `Migrator` struct with `rm *RequestMigration`, `ctx context.Context`, `userVersion *Version`
4. Update `TypeMigration` interface to accept `context.Context` parameter

### Phase 2: API Changes

5. Add `For(r *http.Request) (*Migrator, error)` method to `RequestMigration`
6. Move `Marshal()` from `RequestMigration` to `Migrator`
7. Move `Unmarshal()` from `RequestMigration` to `Migrator`
8. Remove `WithUserVersion()` method (superseded by `For()`)
9. Remove `request` field from `RequestMigration` struct

### Phase 3: Internal Updates

10. Update `TypeGraph.MigrateForward()` to accept `context.Context`
11. Update `TypeGraph.MigrateBackward()` to accept `context.Context`
12. Update all internal migration invocations to pass context

### Phase 4: Examples and Tests

13. Update all example migrations to new `TypeMigration` signature
14. Update all tests to use `rm.For(r).Marshal/Unmarshal` pattern
15. Add tests for `UserVersionFromContext()` in migrations
16. Add tests verifying `For(nil)` returns error
17. Add tests for context cancellation propagation

### Phase 5: Documentation

18. Update README with new API
19. Add migration guide for upgrading from `WithUserVersion` to `For`
20. Document `UserVersionFromContext()` and custom context value patterns

---

## Open Questions

1. **Inter-migration state**: Should we provide a built-in `MigrationState` container for complex inter-migration communication, or leave this to users?

2. **Reusability**: Should `Migrator` be safe to reuse across multiple `Marshal`/`Unmarshal` calls for the same request? (Current design: yes, context is immutable so this is safe)
