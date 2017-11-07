package libcontainerd

// Remote on Linux defines the accesspoint to the containerd grpc API.
// Remote on Windows is largely an unimplemented interface as there is
// no remote containerd.
type Remote interface { ////libcontainerd->remote_unix.go中的type remote struct 类型实现以下函数方法
	// Client returns a new Client instance connected with given Backend.
	Client(Backend) (Client, error) //  创建和daemon相关的容器客户端 libcontainerd
	// Cleanup stops containerd if it was started by libcontainerd.
	// Note this is not used on Windows as there is no remote containerd.
	Cleanup()
	// UpdateOptions allows various remote options to be updated at runtime.
	UpdateOptions(...RemoteOption) error
}

// RemoteOption allows to configure parameters of remotes.
// This is unused on Windows.
type RemoteOption interface {
	Apply(Remote) error
}
