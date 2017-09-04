// +build linux

package main

import systemdDaemon "github.com/coreos/go-systemd/daemon"

// preNotifySystem sends a message to the host when the API is active, but before the daemon is
func preNotifySystem() {
}

// notifySystem sends a message to the host when the server is ready to be used
//通知init进程，docker已经开始正常工作了
func notifySystem() { //systemd-notify — Notify service manager about start-up completion and other daemon status changes
	// Tell the init daemon we are accepting requests
	go systemdDaemon.SdNotify("READY=1")
}
