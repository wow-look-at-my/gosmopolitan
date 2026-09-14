// Package probe is a step-0 experiment: a non-main package in the cmd module
// that reaches an internal cmd package and two vendored dependencies.
package probe

import (
	"cmd/internal/objabi"

	"github.com/wow-look-at-my/go-s3-server/cacheclient"
	"golang.org/x/mod/semver"
)

// Report names what this package could reach.
func Report() string {
	_ = cacheclient.ConfigFromEnv()
	return objabi.HeaderString() + " semver-valid(v1.2.3)=" + boolString(semver.IsValid("v1.2.3"))
}

func boolString(val bool) string {
	if val {
		return "true"
	}
	return "false"
}
