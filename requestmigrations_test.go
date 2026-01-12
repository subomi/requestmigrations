package requestmigrations

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// v1 - 2023-02-01
type profile struct {
	Name    string `json:"name"`
	Address string `json:"address"`
}

// v2 - 2023-03-01
type profilev2 struct {
	FirstName string `json:"firstName"`
	LastName  string `json:"lastName"`
	Address   *address
}

type address struct {
	StreetName string `json:"streetName"`
	Country    string `json:"country"`
	State      string `json:"state"`
	Postcode   string `json:"postCode"`
}

type addressMigration struct{}

func (m *addressMigration) MigrateForward(data any) (any, error) {
	return nil, nil
}

func (m *addressMigration) MigrateBackward(data any) (any, error) {
	a, ok := data.(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("bad type")
	}

	addrString := fmt.Sprintf("%s %s %s %s", a["streetName"],
		a["state"], a["country"], a["postCode"])

	return []byte(addrString), nil
}

func newRequestMigration(t *testing.T) *RequestMigration {
	opts := &RequestMigrationOptions{
		VersionHeader:  "X-Test-Version",
		CurrentVersion: "2023-03-01",
		VersionFormat:  DateFormat,
	}

	rm, err := NewRequestMigration(opts)
	if err != nil {
		t.Fatal(err)
	}

	return rm
}

func registerVersions(t *testing.T, rm *RequestMigration) {
	// Register migrations for version 2023-03-01
	err := Register[address](rm, "2023-03-01", &addressMigration{})

	if err != nil {
		t.Fatal(err)
	}
}

func Test_Marshal(t *testing.T) {
	rm := newRequestMigration(t)
	registerVersions(t, rm)

	tests := map[string]struct {
		assert require.ErrorAssertionFunc
	}{
		"no_transformation": {
			assert: require.NoError,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/profile", strings.NewReader(""))
			req.Header.Add("X-Test-Version", "2023-02-01")

			pStruct := profilev2{
				Address: &address{
					State:    "London",
					Postcode: "CR0 1GB",
				},
			}
			bytesW, err := rm.WithUserVersion(req).Marshal(&pStruct)

			_ = bytesW
			tc.assert(t, err)
		})
	}
}

func Test_Unmarshal(t *testing.T) {}

type AddressString string

type addressStringMigration struct{}

func (m *addressStringMigration) MigrateForward(data any) (any, error) {
	s, ok := data.(string)
	if !ok {
		return nil, fmt.Errorf("bad type")
	}
	return AddressString("Migrated: " + s), nil
}

func (m *addressStringMigration) MigrateBackward(data any) (any, error) {
	s, ok := data.(string)
	if !ok {
		return nil, fmt.Errorf("bad type")
	}
	return "Backward: " + s, nil
}

type CustomUser struct {
	Address AddressString `json:"address"`
}

func Test_CustomPrimitive(t *testing.T) {
	opts := &RequestMigrationOptions{
		VersionHeader:  "X-Test-Version",
		CurrentVersion: "2023-03-01",
		VersionFormat:  DateFormat,
	}
	rm, _ := NewRequestMigration(opts)
	Register[AddressString](rm, "2023-03-01", &addressStringMigration{})

	t.Run("Marshal custom primitive", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Add("X-Test-Version", "2023-02-01")

		u := CustomUser{Address: "Main St"}
		data, err := rm.WithUserVersion(req).Marshal(&u)
		require.NoError(t, err)

		var res map[string]interface{}
		json.Unmarshal(data, &res)
		require.Equal(t, "Backward: Main St", res["address"])
	})

	t.Run("Unmarshal custom primitive", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/", nil)
		req.Header.Add("X-Test-Version", "2023-02-01")

		jsonData := `{"address": "Main St"}`
		var u CustomUser
		err := rm.WithUserVersion(req).Unmarshal([]byte(jsonData), &u)
		require.NoError(t, err)
		require.Equal(t, AddressString("Migrated: Main St"), u.Address)
	})
}

type CyclicUser struct {
	ID        string           `json:"id"`
	Workspace *CyclicWorkspace `json:"workspace"`
}

type CyclicWorkspace struct {
	ID    string        `json:"id"`
	Users []*CyclicUser `json:"users"`
}

func Test_Cycles(t *testing.T) {
	opts := &RequestMigrationOptions{
		VersionHeader:  "X-Test-Version",
		CurrentVersion: "2023-03-01",
		VersionFormat:  DateFormat,
	}
	rm, _ := NewRequestMigration(opts)

	t.Run("Build graph with cycles", func(t *testing.T) {
		_, err := rm.buildTypeGraph(reflect.TypeOf(CyclicUser{}), &Version{Format: DateFormat, Value: "2023-01-01"})
		require.NoError(t, err)
	})
}

func Test_RootSlice(t *testing.T) {
	opts := &RequestMigrationOptions{
		VersionHeader:  "X-Test-Version",
		CurrentVersion: "2023-03-01",
		VersionFormat:  DateFormat,
	}
	rm, _ := NewRequestMigration(opts)
	Register[AddressString](rm, "2023-03-01", &addressStringMigration{})

	t.Run("Marshal root slice", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Add("X-Test-Version", "2023-02-01")

		users := []CustomUser{
			{Address: "Main St"},
			{Address: "Second St"},
		}
		data, err := rm.WithUserVersion(req).Marshal(&users)
		require.NoError(t, err)

		var res []map[string]interface{}
		json.Unmarshal(data, &res)
		require.Len(t, res, 2)
		require.Equal(t, "Backward: Main St", res[0]["address"])
		require.Equal(t, "Backward: Second St", res[1]["address"])
	})

	t.Run("Unmarshal root slice", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/", nil)
		req.Header.Add("X-Test-Version", "2023-02-01")

		jsonData := `[{"address": "Main St"}, {"address": "Second St"}]`
		var users []CustomUser
		err := rm.WithUserVersion(req).Unmarshal([]byte(jsonData), &users)
		require.NoError(t, err)
		require.Len(t, users, 2)
		require.Equal(t, AddressString("Migrated: Main St"), users[0].Address)
		require.Equal(t, AddressString("Migrated: Second St"), users[1].Address)
	})
}

type chainMigrationV2 struct{}

func (m *chainMigrationV2) MigrateForward(data any) (any, error) {
	s := data.(string)
	return s + " -> v2", nil
}
func (m *chainMigrationV2) MigrateBackward(data any) (any, error) {
	s := data.(string)
	return s + " -> v1", nil
}

type chainMigrationV3 struct{}

func (m *chainMigrationV3) MigrateForward(data any) (any, error) {
	s := data.(string)
	return s + " -> v3", nil
}
func (m *chainMigrationV3) MigrateBackward(data any) (any, error) {
	s := data.(string)
	return s + " -> v2", nil
}

func Test_VersionChain(t *testing.T) {
	opts := &RequestMigrationOptions{
		VersionHeader:  "X-Test-Version",
		CurrentVersion: "2023-03-01",
		VersionFormat:  DateFormat,
	}
	rm, _ := NewRequestMigration(opts)

	// v1: 2023-01-01 (initial version, no migrations)
	// v2: 2023-02-01
	Register[AddressString](rm, "2023-02-01", &chainMigrationV2{})
	// v3: 2023-03-01
	Register[AddressString](rm, "2023-03-01", &chainMigrationV3{})

	t.Run("Marshal chain v1 to v3", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Add("X-Test-Version", "2023-01-01")

		type User struct {
			Address AddressString `json:"address"`
		}
		u := User{Address: "start"}
		data, err := rm.WithUserVersion(req).Marshal(&u)
		require.NoError(t, err)

		var res map[string]interface{}
		json.Unmarshal(data, &res)
		require.Equal(t, "start -> v2 -> v1", res["address"])
	})

	t.Run("Unmarshal chain v1 to v3", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/", nil)
		req.Header.Add("X-Test-Version", "2023-01-01")

		type User struct {
			Address AddressString `json:"address"`
		}
		jsonData := `{"address": "start"}`
		var u User
		err := rm.WithUserVersion(req).Unmarshal([]byte(jsonData), &u)
		require.NoError(t, err)
		require.Equal(t, AddressString("start -> v2 -> v3"), u.Address)
	})
}
