// +build !linux,!solaris,!windows

package sysinfo

// New returns an empty SysInfo for non linux nor solaris for now.
func New(quiet bool) *SysInfo { //NewDaemon中执行
	sysInfo := &SysInfo{}
	return sysInfo
}
