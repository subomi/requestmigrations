package requestmigrations

import (
	"sort"
)

// ChangelogDescriber is an interface that migrations must implement
// to be included in the changelog
type ChangelogDescriber interface {
	// ChangeDescription returns a human-readable description of what the migration does
	ChangeDescription() string
}

// ChangelogEntry represents changes in a specific API version
type ChangelogEntry struct {
	Version  string   `json:"version"`
	Changes  []string `json:"changes"`
}

// GenerateChangelog generates a list of changes between versions
func (rm *RequestMigration) GenerateChangelog() ([]*ChangelogEntry, error) {
	rm.mu.Lock()
	defer rm.mu.Unlock()
	
	// Sort versions to ensure chronological order
	versions := make([]*Version, len(rm.versions))
	copy(versions, rm.versions)
	
	switch rm.opts.VersionFormat {
	case SemverFormat:
		sort.Slice(versions, semVerSorter(versions))
	case DateFormat:
		sort.Slice(versions, dateVersionSorter(versions))
	default:
		return nil, ErrInvalidVersionFormat
	}

	// Create changelog entries for each version
	var changelog []*ChangelogEntry
	for _, version := range versions {
		migrations, ok := rm.migrations[version.String()]
		if !ok {
			continue
		}
		
		var changes []string
		for _, migration := range migrations {
			if describer, ok := migration.(ChangelogDescriber); ok {
				changes = append(changes, describer.ChangeDescription())
			}
		}

		if len(changes) > 0 {
			entry := &ChangelogEntry{
				Version: version.String(),
				Changes: changes,
			}
			changelog = append(changelog, entry)
		}
	}

	return changelog, nil
} 