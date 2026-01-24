package requestmigrations

import (
	"reflect"
	"strings"
)

// IsStringEmpty checks if the given string s is empty or not
func isStringEmpty(s string) bool { return len(strings.TrimSpace(s)) == 0 }

// dereferenceToLastPtr dereferences nested pointers down to the last pointer level.
// For example: ***T -> *T, **T -> *T, *T -> *T, T -> T
func dereferenceToLastPtr(t reflect.Type) reflect.Type {
	if t.Kind() == reflect.Ptr && t.Elem().Kind() == reflect.Ptr {
		return dereferenceToLastPtr(t.Elem())
	}
	return t
}

// typeHasInterfaceFields checks if a type has any interface fields (direct or nested).
// Types with interface fields need runtime value inspection and cannot use cached graphs directly.
func typeHasInterfaceFields(t reflect.Type) bool {
	return typeHasInterfaceFieldsRecursive(t, make(map[reflect.Type]bool))
}

func typeHasInterfaceFieldsRecursive(t reflect.Type, visited map[reflect.Type]bool) bool {
	// Dereference pointers
	for t.Kind() == reflect.Ptr {
		t = t.Elem()
	}

	// Prevent infinite recursion for cyclic types
	if visited[t] {
		return false
	}
	visited[t] = true

	switch t.Kind() {
	case reflect.Interface:
		return true
	case reflect.Struct:
		for i := 0; i < t.NumField(); i++ {
			if typeHasInterfaceFieldsRecursive(t.Field(i).Type, visited) {
				return true
			}
		}
	case reflect.Slice, reflect.Array:
		return typeHasInterfaceFieldsRecursive(t.Elem(), visited)
	case reflect.Map:
		return typeHasInterfaceFieldsRecursive(t.Key(), visited) ||
			typeHasInterfaceFieldsRecursive(t.Elem(), visited)
	}

	return false
}
