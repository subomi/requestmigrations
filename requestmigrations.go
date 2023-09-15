package requestmigrations

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
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

// migrations := Migrations{
//   "2023-02-28": []Migration{
//     Migration{},
//	   Migration{},
//	 },
// }
type Migrations map[string][]Migration

// Migration is the core interface each transformation in every version
// needs to implement. It includes two predicate functions and two
// transformation functions.
type Migration interface {
	ShouldMigrateRequest(req *http.Request) bool
	MigrateRequest(req *http.Request) error

	ShouldMigrateResponse(req *http.Request, res *http.Response) bool
	MigrateResponse(res *http.Response) error
}

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

// RequestMigration is the exported type responsible for handling request migrations.
type RequestMigration struct {
	opts     *RequestMigrationOptions
	versions []*Version
	metric   *prometheus.HistogramVec
	iv       string

	mu         sync.Mutex
	migrations Migrations
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

	migrations := Migrations{
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

func (rm *RequestMigration) RegisterMigrations(migrations Migrations) error {
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

// VersionAPI is the core exported method responsible for applying request and
// response transformations. It can be applied as a middleware or to specific
// handlers alone.
func (rm *RequestMigration) VersionAPI(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		from, err := rm.getUserVersion(req)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		to := rm.getCurrentVersion()
		m, err := Newmigrator(from, to, rm.versions, rm.migrations)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		if from.Equal(to) {
			next.ServeHTTP(w, req)
			return
		}

		startTime := time.Now()
		defer rm.observeRequestLatency(from, to, startTime)

		err = m.applyRequestMigrations(req)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		// set up reverse migrations
		ww := httptest.NewRecorder()
		defer func() {
			err := m.applyResponseMigrations(req, ww, w)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
			}
		}()

		next.ServeHTTP(ww, req)
	})
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

type migrator struct {
	to         *Version
	from       *Version
	versions   []*Version
	migrations Migrations
}

func Newmigrator(from, to *Version, avs []*Version, migrations Migrations) (*migrator, error) {
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

func (m *migrator) applyRequestMigrations(req *http.Request) error {
	if m.versions == nil {
		return nil
	}

	for _, version := range m.versions {
		migrations, ok := m.migrations[version.String()]
		if !ok {
			return ErrInvalidVersion
		}

		// skip initial version.
		if m.from.Equal(version) {
			continue
		}

		for _, migration := range migrations {
			if !migration.ShouldMigrateRequest(req) {
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

func (m *migrator) applyResponseMigrations(
	req *http.Request,
	rr *httptest.ResponseRecorder, w http.ResponseWriter) error {
	res := rr.Result()

	for i := len(m.versions); i > 0; i-- {
		version := m.versions[i-1]
		migrations, ok := m.migrations[version.String()]
		if !ok {
			return ErrServerError
		}

		// skip initial version.
		if m.from.Equal(version) {
			continue
		}

		for _, migration := range migrations {
			if !migration.ShouldMigrateResponse(req, res) {
				continue
			}

			err := migration.MigrateResponse(res)
			if err != nil {
				return ErrServerError
			}
		}
	}

	err := m.finalResponder(w, res)
	if err != nil {
		// log error.
		return ErrServerError
	}

	return nil
}

func (m *migrator) finalResponder(w http.ResponseWriter, res *http.Response) error {
	body, err := io.ReadAll(res.Body)
	if err != nil {
		return err
	}

	for k, v := range res.Header {
		w.Header()[k] = v
	}

	_, err = w.Write(body)
	if err != nil {
		return err
	}

	return nil
}
