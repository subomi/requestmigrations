package requestmigrations

import (
	"context"
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

type userVersionKey struct{}

// UserVersionFromContext retrieves the user's API version from a migration context.
func UserVersionFromContext(ctx context.Context) *Version {
	if v, ok := ctx.Value(userVersionKey{}).(*Version); ok {
		return v
	}
	return nil
}

func withUserVersion(ctx context.Context, version *Version) context.Context {
	return context.WithValue(ctx, userVersionKey{}, version)
}

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

	graphBuilder *TypeGraphBuilder
}

// Migrator is a request-scoped handle for performing migrations.
type Migrator struct {
	rm          *RequestMigration
	ctx         context.Context
	userVersion *Version
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

	rm := &RequestMigration{
		opts:       opts,
		metric:     me,
		iv:         iv,
		versions:   versions,
		mu:         new(sync.Mutex),
		migrations: make(map[reflect.Type]map[string]TypeMigration),
	}

	rm.graphBuilder = NewTypeGraphBuilder(rm)

	return rm, nil
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

// For creates a request-scoped Migrator for performing migrations.
func (rm *RequestMigration) For(r *http.Request) (*Migrator, error) {
	if r == nil {
		return nil, errors.New("request cannot be nil")
	}

	userVersion, err := rm.getUserVersion(r)
	if err != nil {
		return nil, err
	}

	// Use request's context directly, only add user version
	ctx := withUserVersion(r.Context(), userVersion)

	return &Migrator{
		rm:          rm,
		ctx:         ctx,
		userVersion: userVersion,
	}, nil
}

// Bind is an alias for For.
func (rm *RequestMigration) Bind(r *http.Request) (*Migrator, error) {
	return rm.For(r)
}

func (m *Migrator) Marshal(v interface{}) ([]byte, error) {
	startTime := time.Now()

	graph, err := m.rm.graphBuilder.BuildFromValue(reflect.ValueOf(v), m.userVersion)
	if err != nil {
		return nil, err
	}

	if !graph.HasMigrations() {
		return json.Marshal(v)
	}

	currentVersion := m.rm.getCurrentVersion()

	data, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}

	var intermediate any
	if err := json.Unmarshal(data, &intermediate); err != nil {
		return nil, err
	}

	if err := graph.MigrateBackward(m.ctx, &intermediate); err != nil {
		return nil, err
	}

	result, err := json.Marshal(intermediate)
	if err != nil {
		return nil, err
	}

	// Observe migration latency: from current version -> to user version (backward migration)
	m.rm.observeRequestLatency(currentVersion, m.userVersion, startTime)

	return result, nil
}

func (m *Migrator) Unmarshal(data []byte, v interface{}) error {
	startTime := time.Now()

	t := reflect.TypeOf(v)
	if t.Kind() != reflect.Ptr {
		return errors.New("v must be a pointer")
	}

	graph, err := m.rm.graphBuilder.Build(t, m.userVersion)
	if err != nil {
		return err
	}

	if !graph.HasMigrations() {
		return json.Unmarshal(data, v)
	}

	currentVersion := m.rm.getCurrentVersion()

	var intermediate any
	if err := json.Unmarshal(data, &intermediate); err != nil {
		return err
	}

	if err := graph.MigrateForward(m.ctx, &intermediate); err != nil {
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
	m.rm.observeRequestLatency(m.userVersion, currentVersion, startTime)

	return nil
}

// TypeMigration defines how to migrate a specific type
type TypeMigration interface {
	// MigrateForward transforms data from old version to new
	MigrateForward(ctx context.Context, data any) (any, error)

	// MigrateBackward transforms data from new version to old
	MigrateBackward(ctx context.Context, data any) (any, error)
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

// MigrationFinder abstracts the lookup of migrations for a given type and version
type MigrationFinder interface {
	FindMigrationsForType(t reflect.Type, version *Version) []TypeMigration
}

// TypeGraphBuilder handles the construction of TypeGraph instances
type TypeGraphBuilder struct {
	finder  MigrationFinder
	cacheMu *sync.RWMutex
	cache   map[graphCacheKey]*TypeGraph
}

// NewTypeGraphBuilder creates a new TypeGraphBuilder with the given migration finder
func NewTypeGraphBuilder(finder MigrationFinder) *TypeGraphBuilder {
	return &TypeGraphBuilder{
		finder:  finder,
		cacheMu: new(sync.RWMutex),
		cache:   make(map[graphCacheKey]*TypeGraph),
	}
}

func (b *TypeGraphBuilder) Build(t reflect.Type, userVersion *Version) (*TypeGraph, error) {
	t = dereferenceToLastPtr(t)

	key := graphCacheKey{
		t:       t,
		version: userVersion.String(),
	}

	// 1. Check cache (O(1))
	b.cacheMu.RLock()
	if g, ok := b.cache[key]; ok {
		b.cacheMu.RUnlock()
		return g, nil
	}
	b.cacheMu.RUnlock()

	// 2. Build graph (DFS with internal cycle detection)
	b.cacheMu.Lock()
	defer b.cacheMu.Unlock()

	// Re-check cache after acquiring write lock
	if g, ok := b.cache[key]; ok {
		return g, nil
	}

	g, err := b.buildRecursive(t, userVersion, make(map[reflect.Type]*TypeGraph))
	if err != nil {
		return nil, err
	}

	// 3. Cache the result
	b.cache[key] = g
	return g, nil
}

func (b *TypeGraphBuilder) buildRecursive(t reflect.Type, userVersion *Version, visited map[reflect.Type]*TypeGraph) (*TypeGraph, error) {
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

	graph.Migrations = b.finder.FindMigrationsForType(t, userVersion)

	if t.Kind() == reflect.Slice || t.Kind() == reflect.Array {
		elemGraph, err := b.buildRecursive(t.Elem(), userVersion, visited)
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
			fieldGraph, err := b.buildRecursive(field.Type, userVersion, visited)
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

// BuildFromValue builds a type graph from a value, enabling runtime type detection for interface{} fields.
func (b *TypeGraphBuilder) BuildFromValue(v reflect.Value, userVersion *Version) (*TypeGraph, error) {
	return b.buildFromValueRecursive(v, userVersion, make(map[uintptr]bool))
}

func (b *TypeGraphBuilder) buildFromValueRecursive(v reflect.Value, userVersion *Version, visited map[uintptr]bool) (*TypeGraph, error) {
	for v.Kind() == reflect.Ptr {
		if v.IsNil() {
			return &TypeGraph{Fields: make(map[string]*TypeGraph)}, nil
		}
		v = v.Elem()
	}

	if v.Kind() == reflect.Struct && v.CanAddr() {
		addr := v.UnsafeAddr()
		if visited[addr] {
			return &TypeGraph{Fields: make(map[string]*TypeGraph)}, nil
		}
		visited[addr] = true
	}

	t := v.Type()

	graph := &TypeGraph{
		Type:   t,
		Fields: make(map[string]*TypeGraph),
	}

	graph.Migrations = b.finder.FindMigrationsForType(t, userVersion)

	if v.Kind() == reflect.Slice || v.Kind() == reflect.Array {
		if v.Len() > 0 {
			elemValue := v.Index(0)
			if elemValue.Kind() == reflect.Interface && !elemValue.IsNil() {
				elemValue = elemValue.Elem()
			}

			elemGraph, err := b.buildFromValueRecursive(elemValue, userVersion, visited)
			if err != nil {
				return nil, err
			}
			if elemGraph.HasMigrations() {
				graph.Fields["__elem"] = elemGraph
			}
		}
	}

	if v.Kind() == reflect.Struct {
		for i := 0; i < t.NumField(); i++ {
			field := t.Field(i)
			fieldValue := v.Field(i)

			if !fieldValue.CanInterface() {
				continue
			}

			var fieldGraph *TypeGraph
			var err error

			if field.Type.Kind() == reflect.Interface {
				if fieldValue.IsNil() {
					continue
				}
				actualValue := fieldValue.Elem()
				fieldGraph, err = b.buildFromValueRecursive(actualValue, userVersion, visited)
			} else {
				fieldGraph, err = b.buildFromValueRecursive(fieldValue, userVersion, visited)
			}

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

// MigrateForward applies forward migrations (old version -> new version) to the data
func (g *TypeGraph) MigrateForward(ctx context.Context, data *any) error {
	val := *data
	if val == nil {
		return nil
	}

	switch v := val.(type) {
	case map[string]interface{}:
		for fieldName, fieldGraph := range g.Fields {
			if fieldName == "__elem" {
				continue
			}
			fieldData, ok := v[fieldName]
			if !ok || fieldData == nil {
				continue
			}
			if err := fieldGraph.MigrateForward(ctx, &fieldData); err != nil {
				return err
			}
			v[fieldName] = fieldData
		}
	case []interface{}:
		elemGraph := g.Fields["__elem"]
		if elemGraph != nil {
			for i := range v {
				if err := elemGraph.MigrateForward(ctx, &v[i]); err != nil {
					return err
				}
			}
		}
	}

	for _, m := range g.Migrations {
		migratedData, err := m.MigrateForward(ctx, *data)
		if err != nil {
			return err
		}
		*data = migratedData
	}

	return nil
}

// MigrateBackward applies backward migrations (new version -> old version) to the data
func (g *TypeGraph) MigrateBackward(ctx context.Context, data *any) error {
	if *data == nil {
		return nil
	}

	for i := len(g.Migrations) - 1; i >= 0; i-- {
		m := g.Migrations[i]
		migratedData, err := m.MigrateBackward(ctx, *data)
		if err != nil {
			return err
		}
		*data = migratedData
	}

	val := *data

	switch v := val.(type) {
	case map[string]interface{}:
		for fieldName, fieldGraph := range g.Fields {
			if fieldName == "__elem" {
				continue
			}
			fieldData, ok := v[fieldName]
			if !ok || fieldData == nil {
				continue
			}
			if err := fieldGraph.MigrateBackward(ctx, &fieldData); err != nil {
				return err
			}
			v[fieldName] = fieldData
		}
	case []interface{}:
		elemGraph := g.Fields["__elem"]
		if elemGraph != nil {
			for i := range v {
				if err := elemGraph.MigrateBackward(ctx, &v[i]); err != nil {
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

func (rm *RequestMigration) FindMigrationsForType(t reflect.Type, userVersion *Version) []TypeMigration {
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
