package runtime

import (
	"errors"
	"fmt"
	"path"
	"strings"
	"unicode"
	"unicode/utf8"
)

var (
	ErrInvalidContainerSpec = errors.New("invalid container spec")
	ErrInvalidImageRef      = errors.New("invalid image ref")
	ErrInvalidCopySpec      = errors.New("invalid copy spec")
)

func (spec ContainerSpec) Validate() error {
	if err := validateRequiredToken("provider_type", spec.ProviderType); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidContainerSpec, err)
	}
	if err := spec.Image.Validate(); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidContainerSpec, err)
	}
	for name, value := range map[string]string{
		"host_name":            spec.HostName,
		"network_mode":         spec.NetworkMode,
		"node_id":              spec.NodeID,
		"provider_instance_id": spec.ProviderInstanceID,
		"restart_policy":       spec.RestartPolicy,
	} {
		if err := validateOptionalToken(name, value); err != nil {
			return fmt.Errorf("%w: %w", ErrInvalidContainerSpec, err)
		}
	}
	if spec.AuthCopy != nil {
		if err := spec.AuthCopy.Validate(); err != nil {
			return fmt.Errorf("%w: %w", ErrInvalidContainerSpec, err)
		}
	}
	for _, mount := range spec.Mounts {
		if err := mount.Validate(); err != nil {
			return fmt.Errorf("%w: %w", ErrInvalidContainerSpec, err)
		}
	}
	if err := spec.Resources.Validate(); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidContainerSpec, err)
	}
	return nil
}

func (ref ImageRef) Validate() error {
	if err := validateRequiredToken("image", string(ref)); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidImageRef, err)
	}
	return nil
}

func (spec CopySpec) Validate() error {
	if err := validateAbsolutePath("host_path", spec.HostPath); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidCopySpec, err)
	}
	if err := validateAbsolutePath("container_path", spec.ContainerPath); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidCopySpec, err)
	}
	if spec.OwnerUID < 0 {
		return fmt.Errorf("%w: owner_uid must be non-negative", ErrInvalidCopySpec)
	}
	if spec.OwnerGID < 0 {
		return fmt.Errorf("%w: owner_gid must be non-negative", ErrInvalidCopySpec)
	}
	return nil
}

func (spec MountSpec) Validate() error {
	mountType := strings.ToLower(strings.TrimSpace(spec.Type))
	switch mountType {
	case "bind":
	default:
		return fmt.Errorf("mount type %q is unsupported", spec.Type)
	}
	if err := validateAbsolutePath("mount.target", spec.Target); err != nil {
		return err
	}
	if mountType == "bind" {
		if err := validateAbsolutePath("mount.source", spec.Source); err != nil {
			return err
		}
	}
	if spec.OwnerUID < 0 {
		return fmt.Errorf("mount.owner_uid must be non-negative")
	}
	if spec.OwnerGID < 0 {
		return fmt.Errorf("mount.owner_gid must be non-negative")
	}
	return nil
}

func (limits ResourceLimits) Validate() error {
	if err := validateOptionalRuntimeValue("resources.cpus", limits.CPUs); err != nil {
		return err
	}
	if err := validateOptionalRuntimeValue("resources.memory", limits.Memory); err != nil {
		return err
	}
	if limits.PIDsLimit < 0 {
		return fmt.Errorf("resources.pids_limit must be non-negative")
	}
	return nil
}

func validateOptionalRuntimeValue(name, value string) error {
	if value == "" {
		return nil
	}
	if strings.TrimSpace(value) != value {
		return fmt.Errorf("%s must not contain leading or trailing whitespace", name)
	}
	if strings.ContainsAny(value, "\x00\r\n\t") {
		return fmt.Errorf("%s must not contain control characters", name)
	}
	return nil
}

func validateRequiredToken(name, value string) error {
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("%s is required", name)
	}
	return validateToken(name, value)
}

func validateOptionalToken(name, value string) error {
	if value == "" {
		return nil
	}
	return validateToken(name, value)
}

func validateToken(name, value string) error {
	if strings.TrimSpace(value) != value {
		return fmt.Errorf("%s must not contain leading or trailing whitespace", name)
	}
	for _, r := range value {
		if unicode.IsSpace(r) || unicode.IsControl(r) {
			return fmt.Errorf("%s must not contain whitespace or control characters", name)
		}
	}
	return nil
}

func validateAbsolutePath(name, value string) error {
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("%s is required", name)
	}
	if strings.TrimSpace(value) != value {
		return fmt.Errorf("%s must not contain leading or trailing whitespace", name)
	}
	if strings.ContainsRune(value, '\x00') || !utf8.ValidString(value) {
		return fmt.Errorf("%s contains an invalid path character", name)
	}
	if !path.IsAbs(value) {
		return fmt.Errorf("%s must be absolute", name)
	}
	return nil
}
