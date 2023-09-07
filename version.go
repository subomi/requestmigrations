package requestmigrations

import (
	"time"

	"github.com/Masterminds/semver/v3"
)

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

func dateVersionSorter(versions []*Version) func(i, j int) bool {
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

func semVerSorter(versions []*Version) func(i, j int) bool {
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
