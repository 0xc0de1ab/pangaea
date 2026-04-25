package config

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/0xc0de1ab/pangaea/internal/common"
)

// ProfilesFile is the on-disk shape of profiles.yaml.
type ProfilesFile struct {
	Profiles []Profile `yaml:"profiles"`
}

// Profile mirrors specs §6.2.
type Profile struct {
	Name           string        `yaml:"name"`
	Format         string        `yaml:"format"`
	Dir            string        `yaml:"dir"`
	WatchFiles     []string      `yaml:"watch_files"`
	AllowedClients []string      `yaml:"allowed_clients"`
	Validate       ValidateSpec  `yaml:"validate"`
	Propagate      PropagateSpec `yaml:"propagate"`
}

// ValidateSpec configures which strategy the format uses to compare snapshots
// and whether the live_check HTTP probe should run.
type ValidateSpec struct {
	Strategy         string        `yaml:"strategy"`
	LiveCheck        bool          `yaml:"live_check"`
	LiveCheckTimeout time.Duration `yaml:"live_check_timeout"`
}

// PropagateSpec controls server-to-node truth.push behaviour.
type PropagateSpec struct {
	Mode     string        `yaml:"mode"`
	Cooldown time.Duration `yaml:"cooldown"`
}

// Allowed propagate.mode values.
const (
	PropagateModeStaleOnly = "to_stale_only"
	PropagateModeAll       = "to_all"

	defaultPropagateCooldown = 2 * time.Second
)

// rawValidateSpec / rawPropagateSpec / rawProfile let us accept duration
// strings ("2s") in YAML and convert them once. yaml.v3 has no built-in
// time.Duration support.
type rawValidateSpec struct {
	Strategy         string `yaml:"strategy"`
	LiveCheck        bool   `yaml:"live_check"`
	LiveCheckTimeout string `yaml:"live_check_timeout"`
}

type rawPropagateSpec struct {
	Mode     string `yaml:"mode"`
	Cooldown string `yaml:"cooldown"`
}

type rawProfile struct {
	Name           string           `yaml:"name"`
	Format         string           `yaml:"format"`
	Dir            string           `yaml:"dir"`
	WatchFiles     []string         `yaml:"watch_files"`
	AllowedClients []string         `yaml:"allowed_clients"`
	Validate       rawValidateSpec  `yaml:"validate"`
	Propagate      rawPropagateSpec `yaml:"propagate"`
}

type rawProfilesFile struct {
	Profiles []rawProfile `yaml:"profiles"`
}

// LoadProfiles reads profiles.yaml from disk and validates it. Returned
// errors wrap common.ErrConfigInvalid so callers can match with errors.Is.
func LoadProfiles(path string) (*ProfilesFile, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, common.Wrap(err, common.ErrConfigInvalid, common.MsgConfigMissing, path)
		}
		return nil, common.Wrap(err, common.ErrConfigInvalid, "read %s", path)
	}
	return parseProfiles(raw)
}

func parseProfiles(raw []byte) (*ProfilesFile, error) {
	var rf rawProfilesFile
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true)
	if err := dec.Decode(&rf); err != nil {
		return nil, common.Wrap(err, common.ErrConfigInvalid, "parse profiles yaml")
	}
	if len(rf.Profiles) == 0 {
		return nil, common.Wrap(nil, common.ErrConfigInvalid, "profiles list is empty")
	}

	out := &ProfilesFile{Profiles: make([]Profile, 0, len(rf.Profiles))}
	seen := make(map[string]struct{}, len(rf.Profiles))
	for i, rp := range rf.Profiles {
		p, err := convertProfile(rp, i)
		if err != nil {
			return nil, err
		}
		if _, dup := seen[p.Name]; dup {
			return nil, common.Wrap(nil, common.ErrConfigInvalid, "duplicate profile name %q", p.Name)
		}
		seen[p.Name] = struct{}{}
		out.Profiles = append(out.Profiles, p)
	}
	return out, nil
}

func convertProfile(rp rawProfile, idx int) (Profile, error) {
	if rp.Name == "" {
		return Profile{}, common.Wrap(nil, common.ErrConfigInvalid, "profile #%d has empty name", idx)
	}
	if rp.Format == "" {
		return Profile{}, common.Wrap(nil, common.ErrConfigInvalid, "profile %q: format is required", rp.Name)
	}
	if rp.Dir == "" {
		return Profile{}, common.Wrap(nil, common.ErrConfigInvalid, common.MsgProfileDirEmpty, rp.Name)
	}
	dir, err := ExpandPath(rp.Dir)
	if err != nil {
		return Profile{}, err
	}
	watchFiles := make([]string, 0, len(rp.WatchFiles))
	for _, f := range rp.WatchFiles {
		if f == "" {
			return Profile{}, common.Wrap(nil, common.ErrConfigInvalid, "profile %q: watch_files must not contain empty entries", rp.Name)
		}
		v, err := ExpandPathFromDir(dir, f)
		if err != nil {
			return Profile{}, err
		}
		watchFiles = append(watchFiles, filepath.Clean(v))
	}
	if len(rp.AllowedClients) == 0 {
		return Profile{}, common.Wrap(nil, common.ErrConfigInvalid, "profile %q: allowed_clients must not be empty", rp.Name)
	}
	for _, c := range rp.AllowedClients {
		if c == "" {
			return Profile{}, common.Wrap(nil, common.ErrConfigInvalid, common.MsgAllowedClientEmpty)
		}
	}

	val, err := convertValidate(rp.Validate, rp.Name)
	if err != nil {
		return Profile{}, err
	}
	prop, err := convertPropagate(rp.Propagate, rp.Name)
	if err != nil {
		return Profile{}, err
	}

	return Profile{
		Name:           rp.Name,
		Format:         rp.Format,
		Dir:            dir,
		WatchFiles:     watchFiles,
		AllowedClients: append([]string(nil), rp.AllowedClients...),
		Validate:       val,
		Propagate:      prop,
	}, nil
}

func convertValidate(rv rawValidateSpec, profileName string) (ValidateSpec, error) {
	out := ValidateSpec{Strategy: rv.Strategy, LiveCheck: rv.LiveCheck}
	if rv.LiveCheckTimeout != "" {
		d, err := time.ParseDuration(rv.LiveCheckTimeout)
		if err != nil {
			return ValidateSpec{}, common.Wrap(err, common.ErrConfigInvalid, "profile %q: validate.live_check_timeout %q", profileName, rv.LiveCheckTimeout)
		}
		if d <= 0 {
			return ValidateSpec{}, common.Wrap(nil, common.ErrConfigInvalid, "profile %q: validate.live_check_timeout must be positive", profileName)
		}
		out.LiveCheckTimeout = d
	}
	return out, nil
}

func convertPropagate(rp rawPropagateSpec, profileName string) (PropagateSpec, error) {
	mode := rp.Mode
	if mode == "" {
		mode = PropagateModeStaleOnly
	}
	if mode != PropagateModeStaleOnly && mode != PropagateModeAll {
		return PropagateSpec{}, common.Wrap(nil, common.ErrConfigInvalid, "profile %q: propagate.mode %q must be %q or %q", profileName, rp.Mode, PropagateModeStaleOnly, PropagateModeAll)
	}
	cooldown := defaultPropagateCooldown
	if rp.Cooldown != "" {
		d, err := time.ParseDuration(rp.Cooldown)
		if err != nil {
			return PropagateSpec{}, common.Wrap(err, common.ErrConfigInvalid, "profile %q: propagate.cooldown %q", profileName, rp.Cooldown)
		}
		if d < 0 {
			return PropagateSpec{}, common.Wrap(nil, common.ErrConfigInvalid, "profile %q: propagate.cooldown must be >= 0", profileName)
		}
		cooldown = d
	}
	return PropagateSpec{Mode: mode, Cooldown: cooldown}, nil
}
