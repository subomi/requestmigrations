package requestmigrations

import (
	"context"
	"encoding/json"
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
	ErrServerError                 = errors.New("server error")
	ErrInvalidVersion              = errors.New("invalid version number")
	ErrInvalidVersionFormat        = errors.New("invalid version format")
	ErrCurrentVersionCannotBeEmpty = errors.New("current version field cannot be empty")
	ErrNativeTypeMigration         = errors.New("cannot register migration for native Go type; use a custom type alias instead (e.g., 'type MyString string')")
	ErrAlreadyBuilt                = errors.New("cannot register migrations after Build() has been called")
	ErrNotBuilt                    = errors.New("must call Build() before using RequestMigration")
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

	migrations map[reflect.Type]map[string]TypeMigration // type -> version -> migration

	graphBuilder *typeGraphBuilder
	graphCache   sync.Map

	built bool
	err   error
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
		migrations: make(map[reflect.Type]map[string]TypeMigration),
	}

	rm.graphBuilder = newTypeGraphBuilder(rm, &rm.graphCache)

	return rm, nil
}

// For creates a request-scoped Migrator for performing migrations.
func (rm *RequestMigration) For(r *http.Request) (*Migrator, error) {
	if !rm.built {
		return nil, ErrNotBuilt
	}

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

// RegisterMetrics registers the migration latency metrics with a Prometheus registry.
func (rm *RequestMigration) RegisterMetrics(reg *prometheus.Registry) {
	reg.MustRegister(rm.metric)
}

// WriteVersionHeader returns middleware that writes the user's version to the response header.
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

// FindMigrationsForType returns all migrations applicable to a type from a given version forward.
func (rm *RequestMigration) FindMigrationsForType(t reflect.Type, userVersion *Version) []TypeMigration {
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

func (rm *RequestMigration) registerTypeMigration(version string, t reflect.Type, m TypeMigration) {
	if rm.migrations == nil {
		rm.migrations = make(map[reflect.Type]map[string]TypeMigration)
	}

	versionKnown := false
	for _, v := range rm.versions {
		if v.Value == version {
			versionKnown = true
			break
		}
	}

	if !versionKnown {
		rm.versions = append(rm.versions, &Version{Format: rm.opts.VersionFormat, Value: version})
	}

	if _, ok := rm.migrations[t]; !ok {
		rm.migrations[t] = make(map[string]TypeMigration)
	}
	rm.migrations[t][version] = m
}

// buildAndCacheGraphsForType builds and caches type graphs for all known versions.
// Called during Build to eagerly populate the cache.
// Types with interface fields are skipped - they require runtime value inspection
// and will be built lazily via buildFromValue.
func (rm *RequestMigration) buildAndCacheGraphsForType(t reflect.Type, versions []*Version) {
	// Skip caching for types with interface fields - they'll need BuildFromValue anyway
	// which inspects runtime values and can't use type-based cached graphs
	if typeHasInterfaceFields(t) {
		return
	}

	for _, v := range versions {
		key := graphCacheKey{t: t, version: v.String()}

		// Build graph (this is idempotent)
		// FindMigrationsForType will acquire RLock internally
		graph, err := rm.graphBuilder.buildFromType(t, v)
		if err != nil {
			// Log error but don't fail registration
			// Graph will be built lazily on first request if needed
			continue
		}

		// Store in cache — sync.Map handles concurrency
		rm.graphCache.Store(key, graph)
	}
}

// readBody converts v to a generic JSON representation (map/slice/primitive)
// by streaming the encoding directly into the decoder via an io.Pipe,
// avoiding a full intermediate []byte allocation.
func readBody(v any) (any, error) {
	pr, pw := io.Pipe()

	var result any
	errCh := make(chan error, 1)
	go func() {
		errCh <- json.NewDecoder(pr).Decode(&result)
	}()

	if err := json.NewEncoder(pw).Encode(v); err != nil {
		pw.CloseWithError(err)
		<-errCh
		return nil, err
	}
	pw.Close()

	if err := <-errCh; err != nil {
		return nil, err
	}

	return result, nil
}

// writeBody streams a generic JSON representation into the typed destination v,
// avoiding a full intermediate []byte allocation.
func writeBody(src any, dst any) error {
	pr, pw := io.Pipe()

	errCh := make(chan error, 1)
	go func() {
		errCh <- json.NewDecoder(pr).Decode(dst)
	}()

	if err := json.NewEncoder(pw).Encode(src); err != nil {
		pw.CloseWithError(err)
		<-errCh
		return err
	}
	pw.Close()

	return <-errCh
}

// Migrator is a request-scoped handle for performing migrations.
type Migrator struct {
	rm          *RequestMigration
	ctx         context.Context
	userVersion *Version
}

func (m *Migrator) Marshal(v interface{}) ([]byte, error) {
	startTime := time.Now()

	graph, err := m.rm.graphBuilder.buildFromValue(reflect.ValueOf(v), m.userVersion)
	if err != nil {
		return nil, err
	}

	if !graph.HasMigrations() {
		return json.Marshal(v)
	}

	currentVersion := m.rm.getCurrentVersion()

	intermediate, err := readBody(v)
	if err != nil {
		return nil, err
	}

	if err := graph.MigrateBackward(m.ctx, &intermediate); err != nil {
		return nil, err
	}

	result, err := json.Marshal(intermediate)
	if err != nil {
		return nil, err
	}

	m.rm.observeRequestLatency(currentVersion, m.userVersion, startTime)

	return result, nil
}

func (m *Migrator) Unmarshal(data []byte, v interface{}) error {
	startTime := time.Now()

	t := reflect.TypeOf(v)
	if t.Kind() != reflect.Ptr {
		return errors.New("v must be a pointer")
	}

	key := graphCacheKey{
		t:       dereferenceToLastPtr(t),
		version: m.userVersion.String(),
	}

	var graph *typeGraph
	if cached, ok := m.rm.graphCache.Load(key); ok {
		graph = cached.(*typeGraph)
	} else {
		var err error
		graph, err = m.rm.graphBuilder.buildFromType(t, m.userVersion)
		if err != nil {
			return err
		}
		m.rm.graphCache.Store(key, graph)
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

	if err := writeBody(intermediate, v); err != nil {
		return err
	}

	m.rm.observeRequestLatency(m.userVersion, currentVersion, startTime)

	return nil
}

type TypeMigration interface {
	MigrateForward(ctx context.Context, data any) (any, error)
	MigrateBackward(ctx context.Context, data any) (any, error)
}

// MigrationVersion represents all type migrations for a specific version.
type MigrationVersion struct {
	Version    string
	Migrations map[reflect.Type]TypeMigration
}

type graphCacheKey struct {
	t       reflect.Type
	version string
}

type typeGraph struct {
	Type       reflect.Type
	Fields     map[string]*typeGraph
	Migrations []TypeMigration
}

func (g *typeGraph) HasMigrations() bool {
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

func (g *typeGraph) MigrateForward(ctx context.Context, data *any) error {
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

func (g *typeGraph) MigrateBackward(ctx context.Context, data *any) error {
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

type migrationFinder interface {
	FindMigrationsForType(t reflect.Type, version *Version) []TypeMigration
}

type typeGraphBuilder struct {
	finder     migrationFinder
	graphCache *sync.Map
}

func newTypeGraphBuilder(finder migrationFinder, cache *sync.Map) *typeGraphBuilder {
	return &typeGraphBuilder{
		finder:     finder,
		graphCache: cache,
	}
}

func (b *typeGraphBuilder) buildFromType(t reflect.Type, userVersion *Version) (*typeGraph, error) {
	t = dereferenceToLastPtr(t)
	return b.walkType(t, userVersion, make(map[reflect.Type]*typeGraph))
}

func (b *typeGraphBuilder) walkType(t reflect.Type, userVersion *Version, visited map[reflect.Type]*typeGraph) (*typeGraph, error) {
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}

	if g, ok := visited[t]; ok {
		return g, nil
	}

	graph := &typeGraph{
		Type:   t,
		Fields: make(map[string]*typeGraph),
	}
	visited[t] = graph

	graph.Migrations = b.finder.FindMigrationsForType(t, userVersion)

	if t.Kind() == reflect.Slice || t.Kind() == reflect.Array {
		elemGraph, err := b.walkType(t.Elem(), userVersion, visited)
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
			fieldGraph, err := b.walkType(field.Type, userVersion, visited)
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

func (b *typeGraphBuilder) buildFromValue(v reflect.Value, userVersion *Version) (*typeGraph, error) {
	return b.walkValue(v, userVersion, make(map[reflect.Type]*typeGraph))
}

func (b *typeGraphBuilder) walkValue(v reflect.Value, userVersion *Version, visited map[reflect.Type]*typeGraph) (*typeGraph, error) {
	for v.Kind() == reflect.Ptr {
		if v.IsNil() {
			return &typeGraph{Fields: make(map[string]*typeGraph)}, nil
		}
		v = v.Elem()
	}

	t := v.Type()

	if g, ok := visited[t]; ok {
		return g, nil
	}

	if !typeHasInterfaceFields(t) {
		return b.buildFromType(t, userVersion)
	}

	graph := &typeGraph{
		Type:   t,
		Fields: make(map[string]*typeGraph),
	}
	visited[t] = graph

	graph.Migrations = b.finder.FindMigrationsForType(t, userVersion)

	if v.Kind() == reflect.Slice || v.Kind() == reflect.Array {
		if v.Len() > 0 {
			elemValue := v.Index(0)
			if elemValue.Kind() == reflect.Interface && !elemValue.IsNil() {
				elemValue = elemValue.Elem()
			}

			elemGraph, err := b.walkValue(elemValue, userVersion, visited)
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

			var fieldGraph *typeGraph
			var err error

			if field.Type.Kind() == reflect.Interface {
				if fieldValue.IsNil() {
					continue
				}
				actualValue := fieldValue.Elem()
				fieldGraph, err = b.walkValue(actualValue, userVersion, visited)
			} else {
				fieldGraph, err = b.buildFromType(field.Type, userVersion)
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

// VersionedTypeMigration pairs a type with a version and its migration logic.
// Construct using the Migration generic helper.
type VersionedTypeMigration struct {
	version   string
	t         reflect.Type
	migration TypeMigration
}

// Migration creates a VersionedTypeMigration entry for type T.
func Migration[T any](version string, m TypeMigration) VersionedTypeMigration {
	return VersionedTypeMigration{
		version:   version,
		t:         reflect.TypeOf((*T)(nil)).Elem(),
		migration: m,
	}
}

// Register adds one or more type migrations. Returns rm for chaining.
// Errors are accumulated and surfaced when Build is called.
func (rm *RequestMigration) Register(migrations ...VersionedTypeMigration) *RequestMigration {
	if rm.err != nil {
		return rm
	}

	if rm.built {
		rm.err = ErrAlreadyBuilt
		return rm
	}

	for _, entry := range migrations {
		if !isValidMigrationType(entry.t) {
			rm.err = ErrNativeTypeMigration
			return rm
		}
		rm.registerTypeMigration(entry.version, entry.t, entry.migration)
	}

	return rm
}

// Build sorts versions, eagerly builds type graphs, and marks the instance as
// ready for use. Must be called after all Register calls and before For/Bind.
func (rm *RequestMigration) Build() error {
	if rm.err != nil {
		return rm.err
	}

	if rm.built {
		return ErrAlreadyBuilt
	}

	switch rm.opts.VersionFormat {
	case SemverFormat:
		sort.Slice(rm.versions, semVerSorter(rm.versions))
	case DateFormat:
		sort.Slice(rm.versions, dateVersionSorter(rm.versions))
	default:
		return ErrInvalidVersionFormat
	}

	for t := range rm.migrations {
		rm.buildAndCacheGraphsForType(t, rm.versions)
	}

	rm.built = true
	return nil
}


// isValidMigrationType returns true ONLY if the type is a user-defined named type.
// It blocks built-in primitives (string, int) AND unnamed composites ([]string, map[int]int).
//
// Allowed: type MyString string, type User struct
// Blocked: string, int, []byte, map[string]string, interface{}, error
func isValidMigrationType(t reflect.Type) bool {
	// Dereference pointers to get the underlying type
	for t.Kind() == reflect.Ptr {
		t = t.Elem()
	}

	// If PkgPath is empty, it is a built-in type (string, int, error)
	// OR an unnamed composite literal ([]string, map[int]int).
	// We generally want to block ALL of these for migrations.
	if t.PkgPath() == "" {
		return false
	}

	return true
}
