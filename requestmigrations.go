package requestmigrations

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"reflect"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

type VersionFormat string

const (
	SemverFormat VersionFormat = "semver"
	DateFormat   VersionFormat = "date"
)

var (
	ErrServerError                 = errors.New("Server error")
	ErrInvalidVersion              = errors.New("Invalid version number")
	ErrInvalidVersionFormat        = errors.New("Invalid version format")
	ErrCurrentVersionCannotBeEmpty = errors.New("Current Version field cannot be empty")
)

// Migration is the core interface each transformation in every version
// needs to implement. It includes two predicate functions and two
// transformation functions.
type Migration interface {
	Migrate(data []byte, header http.Header) ([]byte, http.Header, error)
}

// Migrations is an array of migrations declared by each handler.
type Migrations []Migration

// migrations := Migrations{
//   "2023-02-28": []Migration{
//     Migration{},
//	   Migration{},
//	 },
// }
type MigrationStore map[string]Migrations

type GetUserVersionFunc func(req *http.Request) (string, error)

// RequestMigrationOptions is used to configure the RequestMigration type.
type RequestMigrationOptions struct {
	// VersionHeader refers to the header value used to retrieve the request's
	// version. If VersionHeader is empty, we call the GetUserVersionFunc to
	// retrive the user's version.
	VersionHeader string

	// CurrentVersion refers to the API's most recent version. This value should
	// map to the most recent version in the Migrations slice.
	CurrentVersion string

	// GetUserHeaderFunc is a function to retrieve the user's version. This is useful
	// where the user has a persistent version that necessarily being available in the
	// request.
	GetUserVersionFunc GetUserVersionFunc

	// VersionFormat is used to specify the versioning format. The two supported types
	// are DateFormat and SemverFormat.
	VersionFormat VersionFormat
}

type rollbackFn func(w http.ResponseWriter)

// RequestMigration is the exported type responsible for handling request migrations.
type RequestMigration struct {
	opts     *RequestMigrationOptions
	versions []*Version
	metric   *prometheus.HistogramVec
	iv       string

	mu         sync.Mutex
	migrations MigrationStore
}

func NewRequestMigration(opts *RequestMigrationOptions) (*RequestMigration, error) {
	if opts == nil {
		return nil, errors.New("options cannot be nil")
	}

	if isStringEmpty(opts.CurrentVersion) {
		return nil, ErrCurrentVersionCannotBeEmpty
	}

	me := prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name: "requestmigrations_seconds",
		Help: "The latency of request migrations from one version to another.",
	}, []string{"from", "to"})

	var iv string
	if opts.VersionFormat == DateFormat {
		iv = new(time.Time).Format(time.DateOnly)
	} else if opts.VersionFormat == SemverFormat {
		iv = "v0"
	}

	migrations := MigrationStore{
		iv: []Migration{},
	}

	var versions []*Version
	versions = append(versions, &Version{Format: opts.VersionFormat, Value: iv})

	return &RequestMigration{
		opts:       opts,
		metric:     me,
		iv:         iv,
		versions:   versions,
		migrations: migrations,
	}, nil
}

func (rm *RequestMigration) RegisterMigrations(migrations MigrationStore) error {
	rm.mu.Lock()
	defer rm.mu.Unlock()

	for k, v := range migrations {
		rm.migrations[k] = v
		rm.versions = append(rm.versions, &Version{Format: rm.opts.VersionFormat, Value: k})
	}

	switch rm.opts.VersionFormat {
	case SemverFormat:
		sort.Slice(rm.versions, semVerSorter(rm.versions))
	case DateFormat:
		sort.Slice(rm.versions, dateVersionSorter(rm.versions))
	default:
		return ErrInvalidVersionFormat
	}

	return nil
}

// Migrate is the core API for apply transformations to your handlers. It should be
// called at the start of your handler to transform the body attached to your request
// before further processing. To transform the response as well, you need to use
// the rollback and res function to roll changes back and set the handler response
// respectively.
func (rm *RequestMigration) Migrate(r *http.Request, handler string) (error, *response, rollbackFn) {
	err := rm.migrateRequest(r, handler)
	if err != nil {
		return err, nil, nil
	}

	res := &response{}
	rollback := func(w http.ResponseWriter) {
		res.body, err = rm.migrateResponse(r, res.body, handler)
		if err != nil {
			// write an error to the client.
			return
		}

		err = rm.writeResponseToClient(w, res)
		if err != nil {
			// write an error to the client.
			return
		}
	}

	return nil, res, rollback
}

func (rm *RequestMigration) migrateRequest(r *http.Request, handler string) error {
	from, err := rm.getUserVersion(r)
	if err != nil {
		return err
	}

	to := rm.getCurrentVersion()
	m, err := Newmigrator(from, to, rm.versions, rm.migrations)
	if err != nil {
		return err
	}

	if from.Equal(to) {
		return nil
	}

	startTime := time.Now()
	defer rm.observeRequestLatency(from, to, startTime)

	err = m.applyRequestMigrations(r, handler)
	if err != nil {
		return err
	}

	return nil
}

func (rm *RequestMigration) migrateResponse(r *http.Request, body []byte, handler string) ([]byte, error) {
	from, err := rm.getUserVersion(r)
	if err != nil {
		return nil, err
	}

	to := rm.getCurrentVersion()
	m, err := Newmigrator(from, to, rm.versions, rm.migrations)
	if err != nil {
		return nil, err
	}

	if from.Equal(to) {
		return body, nil
	}

	return m.applyResponseMigrations(r, r.Header, body, handler)
}

func (rm *RequestMigration) getUserVersion(req *http.Request) (*Version, error) {
	var vh string
	vh = req.Header.Get(rm.opts.VersionHeader)

	if !isStringEmpty(vh) {
		return &Version{
			Format: rm.opts.VersionFormat,
			Value:  vh,
		}, nil
	}

	if rm.opts.GetUserVersionFunc != nil {
		vh, err := rm.opts.GetUserVersionFunc(req)
		if err != nil {
			return nil, err
		}

		return &Version{
			Format: rm.opts.VersionFormat,
			Value:  vh,
		}, nil
	}

	return &Version{
		Format: rm.opts.VersionFormat,
		Value:  rm.iv,
	}, nil
}

func (rm *RequestMigration) getCurrentVersion() *Version {
	return &Version{
		Format: rm.opts.VersionFormat,
		Value:  rm.opts.CurrentVersion,
	}
}

func (rm *RequestMigration) observeRequestLatency(from, to *Version, sT time.Time) {
	finishTime := time.Now()
	latency := finishTime.Sub(sT)

	h, err := rm.metric.GetMetricWith(
		prometheus.Labels{
			"from": from.String(),
			"to":   to.String()})
	if err != nil {
		// do nothing.
		return
	}

	h.Observe(latency.Seconds())
}

func (rm *RequestMigration) RegisterMetrics(reg *prometheus.Registry) {
	reg.MustRegister(rm.metric)
}

func (rm *RequestMigration) writeResponseToClient(w http.ResponseWriter, res *response) error {
	if res.statusCode != 0 {
		w.WriteHeader(res.statusCode)
	}

	_, err := w.Write(res.body)
	if err != nil {
		return err
	}

	// TODO(subomi): log bytesWritten
	return nil
}

type migrator struct {
	to         *Version
	from       *Version
	versions   []*Version
	migrations MigrationStore
}

func Newmigrator(from, to *Version, avs []*Version, migrations MigrationStore) (*migrator, error) {
	if !from.IsValid() || !to.IsValid() {
		return nil, ErrInvalidVersion
	}

	var versions []*Version
	for i, v := range avs {
		if v.Equal(from) {
			versions = avs[i:]
			break
		}
	}

	return &migrator{
		to:         to,
		from:       from,
		versions:   versions,
		migrations: migrations,
	}, nil
}

func (m *migrator) applyRequestMigrations(req *http.Request, handler string) error {
	if m.versions == nil {
		return nil
	}

	data, err := io.ReadAll(req.Body)
	if err != nil {
		return err
	}

	header := req.Header.Clone()

	for _, version := range m.versions {
		migrations, ok := m.migrations[version.String()]
		if !ok {
			return ErrInvalidVersion
		}

		// skip initial version.
		if m.from.Equal(version) {
			continue
		}

		migration := m.retrieveHandlerRequestMigration(migrations, handler)
		if migration != nil {
			data, header, err = migration.Migrate(data, header)
			if err != nil {
				return err
			}
		}
	}

	req.Header = header

	// set the body back for the rest of the middleware.
	req.Body = io.NopCloser(bytes.NewReader(data))

	return nil
}

func (m *migrator) applyResponseMigrations(r *http.Request, header http.Header, data []byte, handler string) ([]byte, error) {
	var err error

	for i := len(m.versions); i > 0; i-- {
		version := m.versions[i-1]
		migrations, ok := m.migrations[version.String()]
		if !ok {
			return nil, ErrServerError
		}

		// skip initial version.
		if m.from.Equal(version) {
			return data, nil
		}

		migration := m.retrieveHandlerResponseMigration(migrations, handler)
		if migration != nil {
			data, _, err = migration.Migrate(data, header)
			if err != nil {
				return nil, ErrServerError
			}
		}

	}

	return data, nil
}

func (m *migrator) retrieveHandlerResponseMigration(migrations Migrations, handler string) Migration {
	return m.retrieveHandlerMigration(migrations, strings.Join([]string{handler, "response"}, ""))
}

func (m *migrator) retrieveHandlerRequestMigration(migrations Migrations, handler string) Migration {
	return m.retrieveHandlerMigration(migrations, strings.Join([]string{handler, "request"}, ""))
}

func (m *migrator) retrieveHandlerMigration(migrations Migrations, handler string) Migration {
	for _, migration := range migrations {
		var mv reflect.Value

		mv = reflect.ValueOf(migration)

		if mv.Kind() == reflect.Ptr {
			mv = mv.Elem()
		}

		fName := strings.ToLower(mv.Type().Name())
		if strings.HasPrefix(fName, strings.ToLower(handler)) {
			return migration
		}
	}

	return nil
}
