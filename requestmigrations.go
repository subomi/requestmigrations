package requestmigrations

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
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
	ErrServerError          = errors.New("Server error")
	ErrInvalidVersion       = errors.New("Invalid version number")
	ErrInvalidVersionFormat = errors.New("Invalid version format")
)

// migrations := Migrations{
//   "2023-02-28": []Migration{
//     Migration{},
//	   Migration{},
//	 },
// }
type Migrations map[string][]Migration

// Migration is the core interface each transformation in every version
// needs to implement.
type Migration interface {
	Migrate(data []byte, header http.Header) ([]byte, http.Header, error)
	ShouldMigrateConstraint(url *url.URL, method string, data []byte, isReq bool) bool
}

type GetUserHeaderFunc func(req *http.Request) (string, error)

type RequestMigrationOptions struct {
	VersionHeader     string
	CurrentVersion    string
	GetUserHeaderFunc GetUserHeaderFunc
	VersionFormat     VersionFormat
}

type RequestMigration struct {
	opts     *RequestMigrationOptions
	versions []*Version
	Metric   *prometheus.HistogramVec
	iv       string

	mu         sync.Mutex
	migrations Migrations
}

func NewRequestMigration(opts *RequestMigrationOptions) (*RequestMigration, error) {
	if opts == nil {
		return nil, errors.New("options cannot be nil")
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
		Metric:     me,
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

func (rm *RequestMigration) VersionAPI(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		from, err := rm.getUserVersion(req)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		to := rm.getCurrentVersion()
		m, err := NewMigrator(from, to, rm.versions, rm.migrations)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		fmt.Printf("from: %v, to: %v\n", from, to)
		if from.Equal(to) {
			next.ServeHTTP(w, req)
			return
		}

		startTime := time.Now()
		defer func() {
			finishTime := time.Now()
			latency := finishTime.Sub(startTime)

			h, err := rm.Metric.GetMetricWith(
				prometheus.Labels{
					"from": from.String(),
					"to":   to.String()})
			if err != nil {
				// do nothing.
				return
			}

			h.Observe(latency.Seconds())
		}()

		err = m.applyRequestMigrations(req)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			fmt.Println(err)
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

	if isStringEmpty(vh) {
		if rm.opts.GetUserHeaderFunc != nil {
			var err error
			vh, err = rm.opts.GetUserHeaderFunc(req)
			if err != nil {
				return nil, err
			}
		}
	}

	if isStringEmpty(vh) {
		vh = rm.iv
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
		return nil, ErrInvalidVersion
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

func (m *Migrator) applyRequestMigrations(req *http.Request) error {
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

		for _, migration := range migrations {
			if !migration.ShouldMigrateConstraint(req.URL, req.Method, data, true) {
				continue
			}

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

func (m *Migrator) applyResponseMigrations(
	req *http.Request,
	rr *httptest.ResponseRecorder, w http.ResponseWriter) error {
	res := rr.Result()

	data, err := io.ReadAll(res.Body)
	if err != nil {
		return err
	}

	header := res.Header.Clone()

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
			if !migration.ShouldMigrateConstraint(req.URL, req.Method, data, false) {
				continue
			}

			data, header, err = migration.Migrate(data, header)
			if err != nil {
				return ErrServerError
			}
		}
	}

	err = m.finalResponder(w, data, header)
	if err != nil {
		// log error.
		return ErrServerError
	}

	return nil
}

func (m *Migrator) finalResponder(w http.ResponseWriter, body []byte, h http.Header) error {
	for k, v := range h {
		w.Header()[k] = v
	}

	_, err := w.Write(body)
	if err != nil {
		return err
	}

	return nil
}
