# Bulk Registration API Design

**Status:** Proposed  
**Author:** Subomi Oluwalana  
**Date:** January 2026

---

## Table of Contents

1. [Overview](#overview)
2. [Problem](#problem)
3. [Solution](#solution)
4. [API Design](#api-design)
5. [Internal Implementation](#internal-implementation)
6. [Usage Examples](#usage-examples)
7. [Error Handling](#error-handling)
8. [Design Decisions](#design-decisions)
9. [Implementation Plan](#implementation-plan)

---

## Overview

This document describes a cleaner way to register multiple type migrations for a single version atomically.

---

## Problem

Currently, registering multiple migrations for a single version requires repetitive calls:

```go
rms.Register[User](rm, "2024-01-01", &UserMigration{})
rms.Register[Address](rm, "2024-01-01", &AddressMigration{})
rms.Register[Order](rm, "2024-01-01", &OrderMigration{})
rms.Register[Payment](rm, "2024-01-01", &PaymentMigration{})
```

This is verbose and error-prone (easy to typo the version string).

---

## Solution: Struct-Based Bulk Registration

Provide a `RegisterVersion` function that accepts a structured definition of all migrations for a version.

---

## API Design

### TypedMigration Struct

```go
// TypedMigration pairs a Go type with its migration implementation.
// Used with RegisterVersion for bulk registration.
type TypedMigration struct {
    // Type is an instance of the Go type this migration handles.
    // Only the type information is used; the value is ignored.
    // Example: User{} or (*User)(nil)
    Type any
    
    // Migration is the TypeMigration implementation for this type.
    Migration TypeMigration
}
```

### VersionMigrations Struct

```go
// VersionMigrations defines all type migrations for a specific API version.
type VersionMigrations struct {
    // Version is the API version string (e.g., "2024-01-01" or "v2.0.0").
    Version string
    
    // Migrations is the list of type migrations for this version.
    Migrations []TypedMigration
}
```

### RegisterVersion Function

```go
// RegisterVersion registers all type migrations for a specific version atomically.
// If any migration fails to register, no migrations are registered and an error is returned.
//
// Example:
//
//     err := rms.RegisterVersion(rm, &rms.VersionMigrations{
//         Version: "2024-01-01",
//         Migrations: []rms.TypedMigration{
//             {Type: User{}, Migration: &UserMigration{}},
//             {Type: Address{}, Migration: &AddressMigration{}},
//             {Type: Order{}, Migration: &OrderMigration{}},
//         },
//     })
//
func RegisterVersion(rm *RequestMigration, vm *VersionMigrations) error
```

---

## Internal Implementation

```go
func RegisterVersion(rm *RequestMigration, vm *VersionMigrations) error {
    if vm == nil {
        return errors.New("version migrations cannot be nil")
    }
    
    if vm.Version == "" {
        return errors.New("version cannot be empty")
    }
    
    if len(vm.Migrations) == 0 {
        return errors.New("migrations list cannot be empty")
    }
    
    // Validate all migrations first (atomic: fail before any registration)
    types := make([]reflect.Type, len(vm.Migrations))
    for i, tm := range vm.Migrations {
        if tm.Type == nil {
            return fmt.Errorf("migration %d: type cannot be nil", i)
        }
        if tm.Migration == nil {
            return fmt.Errorf("migration %d: migration cannot be nil", i)
        }
        types[i] = reflect.TypeOf(tm.Type)
    }
    
    // Check for duplicate types within the same registration
    seen := make(map[reflect.Type]bool)
    for i, t := range types {
        if seen[t] {
            return fmt.Errorf("migration %d: duplicate type %v", i, t)
        }
        seen[t] = true
    }
    
    // All validations passed - register atomically
    rm.mu.Lock()
    defer rm.mu.Unlock()
    
    for i, tm := range vm.Migrations {
        // Use internal registration (already holds lock)
        if err := rm.registerTypeMigrationLocked(vm.Version, types[i], tm.Migration); err != nil {
            // Rollback: remove any migrations we just added for this version
            for j := 0; j < i; j++ {
                rm.unregisterTypeMigrationLocked(vm.Version, types[j])
            }
            return fmt.Errorf("migration %d (%v): %w", i, types[i], err)
        }
    }
    
    return nil
}
```

---

## Usage Examples

### Before (Verbose)

```go
rms.Register[User](rm, "2024-01-01", &UserMigration{})
rms.Register[Address](rm, "2024-01-01", &AddressMigration{})
rms.Register[Order](rm, "2024-01-01", &OrderMigration{})
rms.Register[Payment](rm, "2024-01-01", &PaymentMigration{})

rms.Register[User](rm, "2024-06-01", &UserMigrationV2{})
rms.Register[Workspace](rm, "2024-06-01", &WorkspaceMigration{})
```

### After (Clean)

```go
err := rms.RegisterVersion(rm, &rms.VersionMigrations{
    Version: "2024-01-01",
    Migrations: []rms.TypedMigration{
        {Type: User{}, Migration: &UserMigration{}},
        {Type: Address{}, Migration: &AddressMigration{}},
        {Type: Order{}, Migration: &OrderMigration{}},
        {Type: Payment{}, Migration: &PaymentMigration{}},
    },
})
if err != nil {
    log.Fatal(err)
}

err = rms.RegisterVersion(rm, &rms.VersionMigrations{
    Version: "2024-06-01",
    Migrations: []rms.TypedMigration{
        {Type: User{}, Migration: &UserMigrationV2{}},
        {Type: Workspace{}, Migration: &WorkspaceMigration{}},
    },
})
if err != nil {
    log.Fatal(err)
}
```

---

## Error Handling

The function validates all migrations before registering any:

```go
// This will fail atomically - no partial registration
err := rms.RegisterVersion(rm, &rms.VersionMigrations{
    Version: "2024-01-01",
    Migrations: []rms.TypedMigration{
        {Type: User{}, Migration: &UserMigration{}},      // Valid
        {Type: nil, Migration: &AddressMigration{}},       // Invalid: nil type
        {Type: Order{}, Migration: &OrderMigration{}},     // Never reached
    },
})
// err: "migration 1: type cannot be nil"
// No migrations registered
```

---

## Design Decisions

| Decision | Choice | Rationale |
|----------|--------|-----------|
| API style | Struct-based | Clear, self-documenting, extensible |
| Type specification | `Type any` with instance | Simpler than generics for bulk ops |
| Atomicity | All-or-nothing | Prevents inconsistent state |
| Validation order | Validate all → Register all | Fail fast, clean rollback |

---

## Implementation Plan

1. Add `TypedMigration` struct
2. Add `VersionMigrations` struct
3. Add internal `registerTypeMigrationLocked()` (assumes lock held)
4. Add internal `unregisterTypeMigrationLocked()` for rollback
5. Implement `RegisterVersion()` with validation and atomic registration
6. Add tests for successful bulk registration
7. Add tests for atomic failure (rollback on error)
8. Add tests for duplicate type detection
9. Update examples to use bulk registration

---

## Open Questions

1. Should `RegisterVersion` support registering the same type multiple times for different versions in a single call? (Current design: no, use separate calls)

2. Should we add a `RegisterVersions` (plural) function that accepts multiple `VersionMigrations` for even more bulk registration?
