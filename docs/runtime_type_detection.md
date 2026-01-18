# Runtime Type Detection for Dynamic Fields

**Status:** Implemented  
**Author:** Subomi Oluwalana  
**Date:** January 2026

---

## Table of Contents

1. [Overview](#overview)
2. [Problem](#problem)
3. [Solution](#solution)
   - [Why Not Modify migrateForward/migrateBackward?](#why-not-modify-migrateforwardmigratebackward)
   - [Actual Implementation: Capture Types Before JSON Serialization](#actual-implementation-capture-types-before-json-serialization)
4. [Implementation](#implementation)
   - [TypeGraphBuilder.BuildFromValue](#typegraphbuilderbuildfromvalue)
   - [TypeGraphBuilder.buildFromValueRecursive](#typegraphbuilderbuildfromvaluerecursive)
   - [Migrator.Marshal Integration](#migratormarshal-integration)
5. [Key Insight](#key-insight)
6. [Usage Examples](#usage-examples)
7. [Test Cases](#test-cases)
8. [Considerations](#considerations)
   - [Performance](#performance)
   - [Type Resolution](#type-resolution)
   - [Backward Compatibility](#backward-compatibility)
9. [References](#references)

---

## Overview

This document describes how runtime type detection enables migrations to work with `interface{}` fields, where the concrete type is only known at runtime rather than compile time.

---

## Problem

The original `TypeGraphBuilder.Build` function uses static type reflection (`field.Type`) to build the migration type graph. This works for statically-typed struct fields but fails for:

1. **`interface{}` fields** - The compile-time type is `interface{}`, so nested migrations aren't discovered

### Example Case

```go
type PagedResponse struct {
    Content    interface{}  `json:"content"`
    Pagination *Pagination  `json:"pagination"`
}

// User registers migration for EndpointResponse
Register[EndpointResponse](rm, "2024-01-01", &EndpointMigration{})

// Previously, this would NOT apply EndpointResponse migrations to Content:
migrator.Marshal(&PagedResponse{Content: []EndpointResponse{...}})
```

---

## Solution

### Why Not Modify migrateForward/migrateBackward?

The initial design considered detecting runtime types during migration by iterating over data fields. However, this approach has a fundamental flaw:

```go
// This DOESN'T work:
case map[string]interface{}:
    for fieldName, fieldData := range v {
        runtimeType := reflect.TypeOf(fieldData)  // Returns map[string]interface{}, NOT EndpointResponse!
    }
```

After JSON round-tripping (`json.Marshal` → `json.Unmarshal`), the original Go type information is lost:

```
Original: EndpointResponse{Name: "test"}
    ↓ json.Marshal
JSON: {"name": "test"}
    ↓ json.Unmarshal into interface{}
Result: map[string]interface{}{"name": "test"}  ← Type information LOST
```

### Actual Implementation: Capture Types Before JSON Serialization

The solution is to inspect runtime types **before** JSON serialization, when we still have access to the original Go values.

---

## Implementation

### TypeGraphBuilder.BuildFromValue

The `TypeGraphBuilder` struct handles graph construction. The `BuildFromValue` method is the entry point for value-based type graph building:

```go
// BuildFromValue builds a type graph from a value, enabling runtime type detection for interface{} fields.
func (b *TypeGraphBuilder) BuildFromValue(v reflect.Value, userVersion *Version) (*TypeGraph, error) {
    return b.buildFromValueRecursive(v, userVersion, make(map[uintptr]bool))
}
```

### TypeGraphBuilder.buildFromValueRecursive

The recursive implementation traverses the value tree:

```go
func (b *TypeGraphBuilder) buildFromValueRecursive(v reflect.Value, userVersion *Version, visited map[uintptr]bool) (*TypeGraph, error) {
    // Dereference pointers
    for v.Kind() == reflect.Ptr {
        if v.IsNil() {
            return &TypeGraph{Fields: make(map[string]*TypeGraph)}, nil
        }
        v = v.Elem()
    }

    // Cycle detection using pointer addresses
    if v.Kind() == reflect.Struct && v.CanAddr() {
        addr := v.UnsafeAddr()
        if visited[addr] {
            return &TypeGraph{Fields: make(map[string]*TypeGraph)}, nil
        }
        visited[addr] = true
    }

    t := v.Type()
    graph := &TypeGraph{
        Type:   t,
        Fields: make(map[string]*TypeGraph),
    }
    graph.Migrations = b.finder.FindMigrationsForType(t, userVersion)

    // Handle slices - use first element to determine type
    if v.Kind() == reflect.Slice || v.Kind() == reflect.Array {
        if v.Len() > 0 {
            elemValue := v.Index(0)
            // If element is interface{}, get the concrete value
            if elemValue.Kind() == reflect.Interface && !elemValue.IsNil() {
                elemValue = elemValue.Elem()
            }
            elemGraph, err := b.buildFromValueRecursive(elemValue, userVersion, visited)
            if err != nil {
                return nil, err
            }
            if elemGraph.HasMigrations() {
                graph.Fields["__elem"] = elemGraph
            }
        }
    }

    // Handle structs
    if v.Kind() == reflect.Struct {
        for i := 0; i < t.NumField(); i++ {
            field := t.Field(i)
            fieldValue := v.Field(i)

            if !fieldValue.CanInterface() {
                continue
            }

            var fieldGraph *TypeGraph
            var err error

            // KEY: For interface{} fields, use the runtime value's type
            if field.Type.Kind() == reflect.Interface {
                if fieldValue.IsNil() {
                    continue
                }
                // Get the concrete value from the interface
                actualValue := fieldValue.Elem()  // ← This gives us the REAL type!
                fieldGraph, err = b.buildFromValueRecursive(actualValue, userVersion, visited)
            } else {
                fieldGraph, err = b.buildFromValueRecursive(fieldValue, userVersion, visited)
            }

            if err != nil {
                return nil, err
            }

            if fieldGraph.HasMigrations() {
                name := field.Name
                if tag := field.Tag.Get("json"); tag != "" {
                    name = strings.Split(tag, ",")[0]
                }
                graph.Fields[name] = fieldGraph
            }
        }
    }

    return graph, nil
}
```

### Migrator.Marshal Integration

The `Migrator.Marshal` method uses `BuildFromValue` to detect runtime types:

```go
func (m *Migrator) Marshal(v interface{}) ([]byte, error) {
    startTime := time.Now()

    // Use value-based graph building to detect interface{} fields at runtime
    graph, err := m.rm.graphBuilder.BuildFromValue(reflect.ValueOf(v), m.userVersion)
    if err != nil {
        return nil, err
    }

    if !graph.HasMigrations() {
        return json.Marshal(v)
    }

    currentVersion := m.rm.getCurrentVersion()

    data, err := json.Marshal(v)
    if err != nil {
        return nil, err
    }

    var intermediate any
    if err := json.Unmarshal(data, &intermediate); err != nil {
        return nil, err
    }

    if err := graph.MigrateBackward(m.ctx, &intermediate); err != nil {
        return nil, err
    }

    result, err := json.Marshal(intermediate)
    if err != nil {
        return nil, err
    }

    m.rm.observeRequestLatency(currentVersion, m.userVersion, startTime)

    return result, nil
}
```

---

## Key Insight

The critical insight is **when** to capture runtime type information:

| Approach | Timing | Works? |
|----------|--------|--------|
| Modify migrateForward/migrateBackward | After JSON round-trip | No - Type info lost |
| BuildFromValue | Before JSON serialization | Yes - Type info intact |

By using `reflect.Value.Elem()` on interface fields **before** JSON marshaling, we get the actual concrete type (`EndpointResponse`) rather than the generic `map[string]interface{}`.

---

## Usage Examples

### Handler Code

```go
func ListEndpointsHandler(rm *RequestMigration) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        migrator, err := rm.For(r)
        if err != nil {
            http.Error(w, err.Error(), http.StatusBadRequest)
            return
        }

        // Get endpoints from database
        endpoints := db.GetEndpoints()

        // Wrap in paged response with interface{} field
        response := &PagedResponse{
            Content:    endpoints,  // []EndpointResponse stored in interface{}
            Page:       1,
            TotalPages: 5,
        }

        // Migrations are automatically applied to Content field
        data, err := migrator.Marshal(response)
        if err != nil {
            http.Error(w, err.Error(), http.StatusInternalServerError)
            return
        }

        w.Header().Set("Content-Type", "application/json")
        w.Write(data)
    }
}
```

### Type Definitions

```go
type PagedResponse struct {
    Content    interface{} `json:"content"`
    Page       int         `json:"page"`
    TotalPages int         `json:"total_pages"`
}

type EndpointResponse struct {
    Name        string `json:"name"`
    Description string `json:"description"`
}
```

### Migration Registration

```go
rm, _ := NewRequestMigration(&RequestMigrationOptions{
    VersionHeader:  "X-API-Version",
    CurrentVersion: "2024-06-01",
    VersionFormat:  DateFormat,
})

// Register migration for EndpointResponse
Register[EndpointResponse](rm, "2024-01-01", &EndpointMigration{})
```

---

## Test Cases

All test cases passing:

```go
func Test_InterfaceFieldMigration(t *testing.T) {
    // Marshal single item in interface field
    // Marshal slice in interface field
    // Marshal with current version - no migration
    // Marshal nil interface field
    // Marshal unregistered type in interface field
}

func Test_NestedInterfaceSliceMigration(t *testing.T) {
    // Marshal pointer slice in interface field
}
```

---

## Considerations

### Performance

- Runtime type detection adds overhead for value traversal
- Uses pointer-address-based cycle detection (`map[uintptr]bool`)
- No caching for value-based graphs (each `Marshal` call builds fresh)
- This is acceptable because `Marshal` is called per-request and values change

### Type Resolution

- Since we capture Go types via `reflect.Value.Elem()` before JSON serialization, type identification is unambiguous
- No structural matching needed - we have the exact runtime type

### Backward Compatibility

- Existing behavior for statically-typed fields remains unchanged
- New behavior only activates for `interface{}` fields with runtime values
- `Unmarshal` still uses type-based graph (`Build`) since we don't have the value yet

---

## References

- **Implementation:** `TypeGraphBuilder.BuildFromValue` and `TypeGraphBuilder.buildFromValueRecursive` in `requestmigrations.go`
- **Tests:** `Test_InterfaceFieldMigration` and `Test_NestedInterfaceSliceMigration` in `requestmigrations_test.go`
- **Related:** [Context Propagation Design](context_propagation.md) for the `Migrator` pattern
