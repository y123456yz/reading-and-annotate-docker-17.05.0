package lxcfs

import (
	"github.com/docker/docker/api/types"
)

// Backend is the methods that need to be implemented to provide
// system specific functionality.
type Backend interface {
	LxcfsInfo() (*types.LxcfsInfo, error)
}

