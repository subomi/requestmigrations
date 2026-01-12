package requestmigrations

import (
	"encoding/json"
	"errors"
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
	ErrServerError                 = errors.New("server error")
	ErrInvalidVersion              = errors.New("invalid version number")
	ErrInvalidVersionFormat        = errors.New("invalid version format")
	ErrCurrentVersionCannotBeEmpty = errors.New("current version field cannot be empty")
)

type GetUserVersionFunc func(req *http.Request) (string, error)

// RequestMigrationOptions is used to configure the RequestMigration type.
type RequestMigrationOptions struct {
	// VersionHeader refers to the header value used to retrieve the request's
	// version. If VersionHeader is empty, we call the GetUserVersionFunc to
	// retrieve the user's version.
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

	mu         *sync.Mutex
	migrations map[reflect.Type]map[string]TypeMigration // type -> version -> migration

	cacheMu *sync.RWMutex
	cache   map[graphCacheKey]*TypeGraph

	request *http.Request // context for a specific request
}

type graphCacheKey struct {
	t       reflect.Type
	version string
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
	switch opts.VersionFormat {
	case DateFormat:
		iv = new(time.Time).Format(time.DateOnly)
	case SemverFormat:
		iv = "v0"
	}

	versions := make([]*Version, 0, 1)
	versions = append(versions, &Version{Format: opts.VersionFormat, Value: iv})

	return &RequestMigration{
		opts:       opts,
		metric:     me,
		iv:         iv,
		versions:   versions,
		mu:         new(sync.Mutex),
		migrations: make(map[reflect.Type]map[string]TypeMigration),
		cacheMu:    new(sync.RWMutex),
		cache:      make(map[graphCacheKey]*TypeGraph),
	}, nil
}

func (rm *RequestMigration) getUserVersion(req *http.Request) (*Version, error) {
	var vh = req.Header.Get(rm.opts.VersionHeader)

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

func (rm *RequestMigration) WriteVersionHeader() func(next http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			version, err := rm.getUserVersion(r)
			if err != nil {
				// fail silently
				next.ServeHTTP(w, r)
			}

			w.Header().Set(rm.opts.VersionHeader, version.String())
			next.ServeHTTP(w, r)
		})
	}
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

func (rm *RequestMigration) WithUserVersion(r *http.Request) *RequestMigration {
	newRM := *rm
	newRM.request = r
	return &newRM
}

func (rm *RequestMigration) Marshal(v interface{}) ([]byte, error) {
	startTime := time.Now()

	var (
		err         error
		userVersion *Version
	)

	if rm.request != nil {
		userVersion, err = rm.getUserVersion(rm.request)
		if err != nil {
			return nil, err
		}
	} else {
		userVersion = rm.getCurrentVersion()
	}

	graph, err := rm.buildTypeGraph(reflect.TypeOf(v), userVersion)
	if err != nil {
		return nil, err
	}

	if !graph.HasMigrations() {
		return json.Marshal(v)
	}

	currentVersion := rm.getCurrentVersion()

	data, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}

	var intermediate any
	if err := json.Unmarshal(data, &intermediate); err != nil {
		return nil, err
	}

	if err := rm.migrateBackward(graph, &intermediate); err != nil {
		return nil, err
	}

	result, err := json.Marshal(intermediate)
	if err != nil {
		return nil, err
	}

	// Observe migration latency: from current version -> to user version (backward migration)
	rm.observeRequestLatency(currentVersion, userVersion, startTime)

	return result, nil
}

func (rm *RequestMigration) Unmarshal(data []byte, v interface{}) error {
	startTime := time.Now()

	var (
		err         error
		userVersion *Version
	)

	if rm.request != nil {
		userVersion, err = rm.getUserVersion(rm.request)
		if err != nil {
			return err
		}
	} else {
		userVersion = rm.getCurrentVersion()
	}

	t := reflect.TypeOf(v)
	if t.Kind() != reflect.Ptr {
		return errors.New("v must be a pointer")
	}

	graph, err := rm.buildTypeGraph(t, userVersion)
	if err != nil {
		return err
	}

	if !graph.HasMigrations() {
		return json.Unmarshal(data, v)
	}

	currentVersion := rm.getCurrentVersion()

	var intermediate any
	if err := json.Unmarshal(data, &intermediate); err != nil {
		return err
	}

	if err := rm.migrateForward(graph, &intermediate); err != nil {
		return err
	}

	data, err = json.Marshal(intermediate)
	if err != nil {
		return err
	}

	if err := json.Unmarshal(data, v); err != nil {
		return err
	}

	// Observe migration latency: from user version -> to current version (forward migration)
	rm.observeRequestLatency(userVersion, currentVersion, startTime)

	return nil
}

// TypeMigration defines how to migrate a specific type
type TypeMigration interface {
	// MigrateForward transforms data from old version to new
	MigrateForward(data any) (any, error)

	// MigrateBackward transforms data from new version to old
	MigrateBackward(data any) (any, error)
}

// MigrationVersion represents all type migrations for a specific version
type MigrationVersion struct {
	Version    string
	Migrations map[reflect.Type]TypeMigration
}

// TypeGraph represents dependencies between types
type TypeGraph struct {
	Type       reflect.Type
	Fields     map[string]*TypeGraph // field name -> nested type graph
	Migrations []TypeMigration
}

func (rm *RequestMigration) buildTypeGraph(t reflect.Type, userVersion *Version) (*TypeGraph, error) {
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}

	key := graphCacheKey{
		t:       t,
		version: userVersion.String(),
	}

	// 1. Check cache (O(1))
	rm.cacheMu.RLock()
	if g, ok := rm.cache[key]; ok {
		rm.cacheMu.RUnlock()
		return g, nil
	}
	rm.cacheMu.RUnlock()

	// 2. Build graph (DFS with internal cycle detection)
	rm.cacheMu.Lock()
	defer rm.cacheMu.Unlock()

	// Re-check cache after acquiring write lock
	if g, ok := rm.cache[key]; ok {
		return g, nil
	}

	g, err := rm.buildTypeGraphRecursive(t, userVersion, make(map[reflect.Type]*TypeGraph))
	if err != nil {
		return nil, err
	}

	// 3. Cache the result
	rm.cache[key] = g
	return g, nil
}

func (rm *RequestMigration) buildTypeGraphRecursive(t reflect.Type, userVersion *Version, visited map[reflect.Type]*TypeGraph) (*TypeGraph, error) {
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}

	if g, ok := visited[t]; ok {
		return g, nil
	}

	graph := &TypeGraph{
		Type:   t,
		Fields: make(map[string]*TypeGraph),
	}
	visited[t] = graph

	graph.Migrations = rm.findMigrationsForType(t, userVersion)

	if t.Kind() == reflect.Slice || t.Kind() == reflect.Array {
		elemGraph, err := rm.buildTypeGraphRecursive(t.Elem(), userVersion, visited)
		if err != nil {
			return nil, err
		}

		if elemGraph.HasMigrations() {
			graph.Fields["__elem"] = elemGraph
		}
	}

	if t.Kind() == reflect.Struct {
		for i := 0; i < t.NumField(); i++ {
			field := t.Field(i)
			fieldGraph, err := rm.buildTypeGraphRecursive(field.Type, userVersion, visited)
			if err != nil {
				return nil, err
			}

			if fieldGraph.HasMigrations() {
				name := field.Name
				if tag := field.Tag.Get("json"); tag != "" {
					name = strings.Split(tag, ",")[0]
				}
				graph.Fields[name] = fieldGraph
			}
		}
	}

	return graph, nil
}

func (g *TypeGraph) HasMigrations() bool {
	if len(g.Migrations) > 0 {
		return true
	}

	for _, field := range g.Fields {
		if field.HasMigrations() {
			return true
		}
	}

	return false
}

func (rm *RequestMigration) migrateForward(graph *TypeGraph, data *any) error {
	val := *data
	if val == nil {
		return nil
	}

	switch v := val.(type) {
	case map[string]interface{}:
		for fieldName, fieldGraph := range graph.Fields {
			if fieldName == "__elem" {
				continue
			}
			fieldData, ok := v[fieldName]
			if !ok || fieldData == nil {
				continue
			}
			if err := rm.migrateForward(fieldGraph, &fieldData); err != nil {
				return err
			}
			v[fieldName] = fieldData
		}
	case []interface{}:
		elemGraph := graph.Fields["__elem"]
		if elemGraph != nil {
			for i := range v {
				if err := rm.migrateForward(elemGraph, &v[i]); err != nil {
					return err
				}
			}
		}
	}

	for _, m := range graph.Migrations {
		migratedData, err := m.MigrateForward(*data)
		if err != nil {
			return err
		}
		*data = migratedData
	}

	return nil
}

func (rm *RequestMigration) migrateBackward(graph *TypeGraph, data *any) error {
	if *data == nil {
		return nil
	}

	for i := len(graph.Migrations) - 1; i >= 0; i-- {
		m := graph.Migrations[i]
		migratedData, err := m.MigrateBackward(*data)
		if err != nil {
			return err
		}
		*data = migratedData
	}

	val := *data

	switch v := val.(type) {
	case map[string]interface{}:
		for fieldName, fieldGraph := range graph.Fields {
			if fieldName == "__elem" {
				continue
			}
			fieldData, ok := v[fieldName]
			if !ok || fieldData == nil {
				continue
			}
			if err := rm.migrateBackward(fieldGraph, &fieldData); err != nil {
				return err
			}
			v[fieldName] = fieldData
		}
	case []interface{}:
		elemGraph := graph.Fields["__elem"]
		if elemGraph != nil {
			for i := range v {
				if err := rm.migrateBackward(elemGraph, &v[i]); err != nil {
					return err
				}
			}
		}
	}

	return nil
}

func Register[T any](rm *RequestMigration, version string, m TypeMigration) error {
	t := reflect.TypeOf((*T)(nil)).Elem()
	return rm.registerTypeMigration(version, t, m)
}

func (rm *RequestMigration) registerTypeMigration(version string, t reflect.Type, m TypeMigration) error {
	rm.mu.Lock()
	defer rm.mu.Unlock()

	if rm.migrations == nil {
		rm.migrations = make(map[reflect.Type]map[string]TypeMigration)
	}

	// Check if this version is already known
	versionKnown := false
	for _, v := range rm.versions {
		if v.Value == version {
			versionKnown = true
			break
		}
	}

	if !versionKnown {
		rm.versions = append(rm.versions, &Version{Format: rm.opts.VersionFormat, Value: version})

		switch rm.opts.VersionFormat {
		case SemverFormat:
			sort.Slice(rm.versions, semVerSorter(rm.versions))
		case DateFormat:
			sort.Slice(rm.versions, dateVersionSorter(rm.versions))
		default:
			return ErrInvalidVersionFormat
		}
	}

	// Internal Type-Centric Pivot: map[Type]map[Version]Migration
	if _, ok := rm.migrations[t]; !ok {
		rm.migrations[t] = make(map[string]TypeMigration)
	}
	rm.migrations[t][version] = m

	return nil
}

func (rm *RequestMigration) findMigrationsForType(t reflect.Type, userVersion *Version) []TypeMigration {
	rm.mu.Lock()
	defer rm.mu.Unlock()

	var applicableMigrations []TypeMigration

	typeHistory, ok := rm.migrations[t]
	if !ok {
		return nil
	}

	// rm.versions is sorted oldest to newest.
	for _, v := range rm.versions {
		if v.Equal(userVersion) || v.isOlderThan(userVersion) {
			continue
		}

		if migration, ok := typeHistory[v.String()]; ok {
			applicableMigrations = append(applicableMigrations, migration)
		}
	}

	return applicableMigrations
}
