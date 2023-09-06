package requestmigrations

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"sync"
	"time"

	"github.com/Masterminds/semver/v3"
)

type VersionFormat string

const (
	SemverFormat VersionFormat = "semver"
	DateFormat   VersionFormat = "date"
)

var (
	ErrorInvalidVersion       = errors.New("Invalid version number")
	ErrorInvalidVersionFormat = errors.New("Invalid version format")
)

//	migrations := map[string][]Migration{
//		"2023-02-28": []Migration{
//			Migration{},
//			Migration{},
//		},
//	}
type Migrations map[string][]Migration

type Migration interface {
	GetName() string

	ShouldMigrateRequest() bool
	MigrateRequest(req *http.Request) error

	ShouldMigrateResponse() bool
	MigrateResponse(res *http.Response) error
}

type RequestMigrationOptions struct {
	VersionHeader  string
	CurrentVersion string
	DefaultVersion string
	VersionFormat  VersionFormat
}

type RequestMigration struct {
	opts *RequestMigrationOptions

	mu         sync.Mutex
	migrations Migrations
	versions   []*Version
}

func NewRequestMigration(opts *RequestMigrationOptions) *RequestMigration {
	return &RequestMigration{opts: opts}
}

func (rm *RequestMigration) RegisterMigrations(migrations Migrations) error {
	rm.mu.Lock()
	defer rm.mu.Unlock()

	rm.migrations = migrations

	for k, _ := range rm.migrations {
		rm.versions = append(rm.versions, &Version{Format: rm.opts.VersionFormat, Value: k})
	}

	switch rm.opts.VersionFormat {
	case SemverFormat:
		sort.Slice(rm.versions, SemVerSorter(rm.versions))
	case DateFormat:
		sort.Slice(rm.versions, DateVersionSorter(rm.versions))
	default:
		return ErrorInvalidVersionFormat
	}

	return nil
}

func (rm *RequestMigration) VersionAPI(next http.HandlerFunc) http.HandlerFunc {
	return func(resp http.ResponseWriter, req *http.Request) {
		// apply migrations
		from, err := rm.getUserVersion(req)
		if err != nil {
			// bypass versioning entirely
			next.ServeHTTP(resp, req)
			return
		}

		to := rm.getCurrentVersion()
		m, err := NewMigrator(from, to, rm.versions, rm.migrations)
		if err != nil {
			// bypass versioning entirely
			next.ServeHTTP(resp, req)
			return
		}

		err = m.applyMigrations(req)
		if err != nil {
			// bypass versioning entirely
			next.ServeHTTP(resp, req)
			return
		}

		// set up reverse migrations
		wresp := httptest.NewRecorder()
		defer m.reverseMigrations(wresp, resp)

		next.ServeHTTP(wresp, req)
	}
}

func (rm *RequestMigration) getUserVersion(req *http.Request) (*Version, error) {
	vh := req.Header.Get(rm.opts.VersionHeader)

	if IsStringEmpty(vh) {
		vh = rm.opts.DefaultVersion
	}

	return &Version{
		Format: rm.opts.VersionFormat,
		Value:  vh,
	}, nil
}

func (rm *RequestMigration) getCurrentVersion() *Version {
	return &Version{
		Format: rm.opts.VersionFormat,
		Value:  rm.opts.CurrentVersion,
	}
}

type Migrator struct {
	to         *Version
	from       *Version
	versions   []*Version
	migrations Migrations
}

func NewMigrator(from, to *Version, avs []*Version, migrations Migrations) (*Migrator, error) {
	if !from.IsValid() || !to.IsValid() {
		return nil, ErrorInvalidVersion
	}

	var versions []*Version
	for i, v := range avs {
		if v.Equal(from) {
			versions = avs[i:]
			break
		}
	}

	return &Migrator{
		to:         to,
		from:       from,
		versions:   versions,
		migrations: migrations,
	}, nil
}

func (m *Migrator) applyMigrations(req *http.Request) error {
	if m.versions == nil {
		return nil
	}

	for _, version := range m.versions {
		migrations, ok := m.migrations[version.String()]
		if !ok {
			return ErrorInvalidVersion
		}

		for _, migration := range migrations {
			if !migration.ShouldMigrateRequest() {
				continue
			}

			err := migration.MigrateRequest(req)
			if err != nil {
				return err
			}
		}
	}

	return nil
}

func (m *Migrator) reverseMigrations(rr *httptest.ResponseRecorder, w http.ResponseWriter) {
	// TODO(subomi): Clone this object.
	res := rr.Result()
	ores := res

	for i := len(m.versions); i > 0; i-- {
		v := m.versions[i-1].String()
		migrations, ok := m.migrations[v]
		if !ok {
			// skip migrations entirely
			m.finalResponder(w, ores)
			return
		}

		for _, migration := range migrations {
			if !migration.ShouldMigrateResponse() {
				continue
			}

			err := migration.MigrateResponse(res)
			if err != nil {
				// skip migrations entirely
				m.finalResponder(w, ores)
				return
			}
		}
	}

	err := m.finalResponder(w, res)
	if err != nil {
		// log error.
	}
}

func (m *Migrator) finalResponder(w http.ResponseWriter, res *http.Response) error {
	body, err := io.ReadAll(res.Body)
	if err != nil {
		return err
	}

	_, err = w.Write(body)
	if err != nil {
		return err
	}

	return nil
}

type Version struct {
	Format VersionFormat
	Value  interface{}
}

func (v *Version) IsValid() bool {
	switch v.Format {
	case SemverFormat:
		_, err := semver.NewVersion(v.Value.(string))
		if err != nil {
			return false
		}

	case DateFormat:
		_, err := time.Parse(time.DateOnly, v.Value.(string))
		if err != nil {
			return false
		}
	}

	return true
}

func (v *Version) Equal(vv *Version) bool {
	switch v.Format {
	case SemverFormat:
		sv, err := semver.NewVersion(v.Value.(string))
		if err != nil {
			return false
		}

		svv, err := semver.NewVersion(vv.Value.(string))
		if err != nil {
			return false
		}

		return sv.Equal(svv)

	case DateFormat:
		tv, err := time.Parse(time.DateOnly, v.Value.(string))
		if err != nil {
			return false
		}

		tvv, err := time.Parse(time.DateOnly, vv.Value.(string))
		if err != nil {
			return false
		}

		return tv.Equal(tvv)
	}

	return false
}
func (v *Version) String() string {
	return v.Value.(string)
}

func DateVersionSorter(versions []*Version) func(i, j int) bool {
	return func(i, j int) bool {
		it, err := time.Parse(time.DateOnly, versions[i].Value.(string))
		if err != nil {
			return false
		}

		jt, err := time.Parse(time.DateOnly, versions[j].Value.(string))
		if err != nil {
			return false
		}

		return it.Before(jt)
	}
}

func SemVerSorter(versions []*Version) func(i, j int) bool {
	return func(i, j int) bool {
		is, err := semver.NewVersion(versions[i].Value.(string))
		if err != nil {
			return false
		}

		js, err := semver.NewVersion(versions[j].Value.(string))
		if err != nil {
			return false
		}

		return is.LessThan(js)
	}
}
