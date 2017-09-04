// +build windows

package sysinfo
// 获取系统信息，linux 不支持cgroup则返回
// New returns an empty SysInfo for windows for now.
func New(quiet bool) *SysInfo {
	sysInfo := &SysInfo{}
	return sysInfo
}
