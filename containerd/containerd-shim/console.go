// +build !solaris

package main

import (
	"fmt"
	"os"
	"syscall"
	"unsafe"
)

// NewConsole returns an initialized console that can be used within a container by copying bytes
// from the main side to the subordinate that is attached as the tty for the container's init process.
func newConsole(uid, gid int) (*os.File, string, error) {
	main, err := os.OpenFile("/dev/ptmx", syscall.O_RDWR|syscall.O_NOCTTY|syscall.O_CLOEXEC, 0)
	if err != nil {
		return nil, "", err
	}
	console, err := ptsname(main)
	if err != nil {
		return nil, "", err
	}
	if err := unlockpt(main); err != nil {
		return nil, "", err
	}
	if err := os.Chmod(console, 0600); err != nil {
		return nil, "", err
	}
	if err := os.Chown(console, uid, gid); err != nil {
		return nil, "", err
	}
	return main, console, nil
}

func ioctl(fd uintptr, flag, data uintptr) error {
	if _, _, err := syscall.Syscall(syscall.SYS_IOCTL, fd, flag, data); err != 0 {
		return err
	}
	return nil
}

// unlockpt unlocks the subordinate pseudoterminal device corresponding to the main pseudoterminal referred to by f.
// unlockpt should be called before opening the subordinate side of a pty.
func unlockpt(f *os.File) error {
	var u int32
	return ioctl(f.Fd(), syscall.TIOCSPTLCK, uintptr(unsafe.Pointer(&u)))
}

// ptsname retrieves the name of the first available pts for the given main.
func ptsname(f *os.File) (string, error) {
	var n int32
	if err := ioctl(f.Fd(), syscall.TIOCGPTN, uintptr(unsafe.Pointer(&n))); err != nil {
		return "", err
	}
	return fmt.Sprintf("/dev/pts/%d", n), nil
}
