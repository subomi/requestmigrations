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
