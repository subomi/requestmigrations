package requestmigrations

import "strings"

// IsStringEmpty checks if the given string s is empty or not
func isStringEmpty(s string) bool { return len(strings.TrimSpace(s)) == 0 }
