// +build !linux

package bridge

import (
	"errors"
	"net"
)

// Add a subordinate to a bridge device.  This is more backward-compatible than
// netlink.NetworkSetMain and works on RHEL 6.
func ioctlAddToBridge(iface, main *net.Interface) error {
	return errors.New("not implemented")
}

func ioctlCreateBridge(name string, setMacAddr bool) error {
	return errors.New("not implemented")
}
