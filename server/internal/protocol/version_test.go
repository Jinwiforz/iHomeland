package protocol

import (
	"errors"
	"testing"
)

func TestCheckVersionAcceptsSupportedVersion(t *testing.T) {
	if err := CheckVersion(MaxSupportedVersion); err != nil {
		t.Fatalf("CheckVersion returned error: %v", err)
	}
}

func TestCheckVersionRejectsLowVersion(t *testing.T) {
	err := CheckVersion(MinSupportedVersion - 1)
	var versionErr VersionError
	if !errors.As(err, &versionErr) {
		t.Fatalf("CheckVersion error = %v, want VersionError", err)
	}
	if versionErr.Response().GetMinSupportedVersion() != MinSupportedVersion {
		t.Fatalf("min supported = %d, want %d", versionErr.Response().GetMinSupportedVersion(), MinSupportedVersion)
	}
	if versionErr.Response().GetMaxSupportedVersion() != MaxSupportedVersion {
		t.Fatalf("max supported = %d, want %d", versionErr.Response().GetMaxSupportedVersion(), MaxSupportedVersion)
	}
}

func TestCheckVersionRejectsHighVersion(t *testing.T) {
	err := CheckVersion(MaxSupportedVersion + 1)
	var versionErr VersionError
	if !errors.As(err, &versionErr) {
		t.Fatalf("CheckVersion error = %v, want VersionError", err)
	}
	if versionErr.Response().GetClientVersion() != MaxSupportedVersion+1 {
		t.Fatalf("client version = %d, want %d", versionErr.Response().GetClientVersion(), MaxSupportedVersion+1)
	}
}
