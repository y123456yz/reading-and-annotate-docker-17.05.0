package container

import (
	"time"

	"github.com/docker/docker/api/types/strslice"
	"github.com/docker/go-connections/nat"
)

// HealthConfig holds configuration settings for the HEALTHCHECK feature.
type HealthConfig struct {
	// Test is the test to perform to check that the container is healthy.
	// An empty slice means to inherit the default.
	// The options are:
	// {} : inherit healthcheck
	// {"NONE"} : disable healthcheck
	// {"CMD", args...} : exec arguments directly
	// {"CMD-SHELL", command} : run command with system's default shell
	Test []string `json:",omitempty"`

	// Zero means to inherit. Durations are expressed as integer nanoseconds.
	Interval    time.Duration `json:",omitempty"` // Interval is the time to wait between checks.
	Timeout     time.Duration `json:",omitempty"` // Timeout is the time to wait before considering the check to have hung.
	StartPeriod time.Duration `json:",omitempty"` // The start period for the container to initialize before the retries starts to count down.

	// Retries is the number of consecutive failures needed to consider a container as unhealthy.
	// Zero means inherit.
	Retries int `json:",omitempty"`
}

// Config contains the configuration data about a container.
// It should hold only portable information about the container.
// Here, "portable" means "independent from the host we are running on".
// Non-portable information *should* appear in HostConfig.
// All fields added to this struct must be marked `omitempty` to keep getting
// predictable hashes from the old `v1Compatibility` configuration.

//在docker create的时候，解析出 *ContainerCreateConfig.config与*ContainerCreateConfig.hostConfig 后，然后在 postContainersCreate->containerCreate 中实例化 Container 结构

//*ContainerCreateConfig.config与*ContainerCreateConfig.hostConfig都是配置的结构，区别是config是只与container相关的配置，hostConfig属于与宿主机相关的配置选项；见ContainerCreateConfig
// 他们都包含在ContainerCreateConfig结构中,见postContainersCreate
type Config struct { //ContainerCreateConfig 中的 Config成员为该结构
	Hostname        string              // Hostname  newContainer->generateHostname中赋值，默认为imageid的前12字节
	Domainname      string              // Domainname
	User            string              // User that will run the command(s) inside the container, also support user:group
	AttachStdin     bool                // Attach the standard input, makes possible user interaction
	AttachStdout    bool                // Attach the standard output
	AttachStderr    bool                // Attach the standard error
	ExposedPorts    nat.PortSet         `json:",omitempty"` // List of exposed ports
	Tty             bool                // Attach standard streams to a tty, including stdin if it is not closed.
	OpenStdin       bool                // Open stdin
	StdinOnce       bool                // If true, close stdin after the 1 attached client disconnects.
	Env             []string            // List of environment variable to set in the container
	//命令行参数在存储在这里
	Cmd             strslice.StrSlice   // Command to run when starting the container
	Healthcheck     *HealthConfig       `json:",omitempty"` // Healthcheck describes how to check the container is healthy
	ArgsEscaped     bool                `json:",omitempty"` // True if command is already escaped (Windows specific)
	Image           string              // Name of the image as it was passed by the operator (e.g. could be symbolic)
	Volumes         map[string]struct{} // List of volumes (mounts) used for the container
	//SetupWorkingDirectory 中赋值
	WorkingDir      string              // Current directory (PWD) in the command will be launched
	Entrypoint      strslice.StrSlice   // Entrypoint to run when starting the container
	NetworkDisabled bool                `json:",omitempty"` // Is network disabled
	MacAddress      string              `json:",omitempty"` // Mac Address of the container
	OnBuild         []string            // ONBUILD metadata that were defined on the image Dockerfile
	Labels          map[string]string   // List of labels set to this container
	StopSignal      string              `json:",omitempty"` // Signal to stop a container
	StopTimeout     *int                `json:",omitempty"` // Timeout (in seconds) to stop a container
	Shell           strslice.StrSlice   `json:",omitempty"` // Shell for shell-form of RUN, CMD, ENTRYPOINT
}
