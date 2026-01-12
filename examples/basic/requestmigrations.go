package main

import (
	"strings"
)

// UserMigration handles migrations for the User type
type UserMigration struct{}

func (m *UserMigration) MigrateForward(data any) (any, error) {
	d, ok := data.(map[string]any)
	if !ok {
		return data, nil
	}

	// If we have full_name but no first_name/last_name, split it
	if fullName, ok := d["full_name"].(string); ok {
		parts := strings.Split(fullName, " ")
		if len(parts) > 0 {
			d["first_name"] = parts[0]
		}
		if len(parts) > 1 {
			d["last_name"] = parts[1]
		}
		delete(d, "full_name")
	}

	return d, nil
}

func (m *UserMigration) MigrateBackward(data any) (any, error) {
	d, ok := data.(map[string]any)
	if !ok {
		return data, nil
	}

	// Join first_name and last_name into full_name for older versions
	firstName, _ := d["first_name"].(string)
	lastName, _ := d["last_name"].(string)
	d["full_name"] = strings.TrimSpace(firstName + " " + lastName)

	return d, nil
}

// ProfileMigration handles migrations for the profile type if needed
type ProfileMigration struct{}

func (m *ProfileMigration) MigrateForward(data any) (any, error) {
	// If it's just a string (old format), we can't fully reconstruct the profile
	// but we can at least put the UID in.
	if uid, ok := data.(string); ok {
		return map[string]any{
			"uid": uid,
		}, nil
	}
	return data, nil
}

func (m *ProfileMigration) MigrateBackward(data any) (any, error) {
	d, ok := data.(map[string]any)
	if !ok {
		return data, nil
	}
	// In older versions, profile was just the UID string
	return d["uid"], nil
}
