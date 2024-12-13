package requestmigrations

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Example migrations that implement ChangelogDescriber
type testMigrationOne struct{}

func (t *testMigrationOne) Migrate(data []byte, header http.Header) ([]byte, http.Header, error) {
	return data, header, nil
}

func (t *testMigrationOne) ChangeDescription() string {
	return "Split the name field into firstName and lastName"
}

type testMigrationTwo struct{}

func (t *testMigrationTwo) Migrate(data []byte, header http.Header) ([]byte, http.Header, error) {
	return data, header, nil
}

func (t *testMigrationTwo) ChangeDescription() string {
	return "Added email verification field"
}

// Migration that doesn't implement ChangelogDescriber
type testMigrationWithoutDescription struct{}

func (t *testMigrationWithoutDescription) Migrate(data []byte, header http.Header) ([]byte, http.Header, error) {
	return data, header, nil
}

func Test_GenerateChangelog(t *testing.T) {
	// Create a new RequestMigration instance
	opts := &RequestMigrationOptions{
		VersionHeader:  "X-Test-Version",
		CurrentVersion: "2023-03-01",
		VersionFormat:  DateFormat,
	}

	rm, err := NewRequestMigration(opts)
	require.NoError(t, err)

	// Register test migrations across different versions
	migrations := &MigrationStore{
		"2023-03-01": Migrations{
			&testMigrationOne{},
			&testMigrationWithoutDescription{}, // Should be ignored in changelog
		},
		"2023-04-01": Migrations{
			&testMigrationTwo{},
		},
	}

	err = rm.RegisterMigrations(*migrations)
	require.NoError(t, err)

	// Generate changelog
	changelog, err := rm.GenerateChangelog()
	require.NoError(t, err)
	require.NotNil(t, changelog)

	// We should have entries for both versions
	assert.Equal(t, 2, len(changelog))

	// Verify first version entry
	assert.Equal(t, "2023-03-01", changelog[0].Version)
	assert.Equal(t, 1, len(changelog[0].Changes))
	assert.Equal(t, "Split the name field into firstName and lastName", changelog[0].Changes[0])

	// Verify second version entry
	assert.Equal(t, "2023-04-01", changelog[1].Version)
	assert.Equal(t, 1, len(changelog[1].Changes))
	assert.Equal(t, "Added email verification field", changelog[1].Changes[0])
}