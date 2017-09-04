package commands

import (
	"os"

	"github.com/docker/docker/cli/command"
	"github.com/docker/docker/cli/command/checkpoint"
	"github.com/docker/docker/cli/command/container"
	"github.com/docker/docker/cli/command/image"
	"github.com/docker/docker/cli/command/network"
	"github.com/docker/docker/cli/command/node"
	"github.com/docker/docker/cli/command/plugin"
	"github.com/docker/docker/cli/command/registry"
	"github.com/docker/docker/cli/command/secret"
	"github.com/docker/docker/cli/command/service"
	"github.com/docker/docker/cli/command/stack"
	"github.com/docker/docker/cli/command/swarm"
	"github.com/docker/docker/cli/command/system"
	"github.com/docker/docker/cli/command/volume"
	"github.com/spf13/cobra"
)

//添加docker的各种命令信息，如docker image   docker bolume等     应该做各种客户端的时候用
// AddCommands adds all the commands from cli/command to the root command

//客户端通过//newDockerCommand->AddCommands构建命令请求发往daemon，daemon收到后通过initRouter中初始化的handler来执行对应job
func AddCommands(cmd *cobra.Command, dockerCli *command.DockerCli) { //newDockerCommand->AddCommands
	cmd.AddCommand(
		// checkpoint
		checkpoint.NewCheckpointCommand(dockerCli),

		// container
		container.NewContainerCommand(dockerCli),
		container.NewRunCommand(dockerCli),

		// image
		image.NewImageCommand(dockerCli),
		image.NewBuildCommand(dockerCli),

		// node
		node.NewNodeCommand(dockerCli),

		// network
		network.NewNetworkCommand(dockerCli),

		// plugin
		plugin.NewPluginCommand(dockerCli),

		// registry
		registry.NewLoginCommand(dockerCli),
		registry.NewLogoutCommand(dockerCli),
		registry.NewSearchCommand(dockerCli),

		// secret
		secret.NewSecretCommand(dockerCli),

		// service
		service.NewServiceCommand(dockerCli),

		// system
		system.NewSystemCommand(dockerCli),
		system.NewVersionCommand(dockerCli),

		// stack
		stack.NewStackCommand(dockerCli),
		stack.NewTopLevelDeployCommand(dockerCli),

		// swarm
		swarm.NewSwarmCommand(dockerCli),

		// volume
		volume.NewVolumeCommand(dockerCli),

		// legacy commands may be hidden
		hide(system.NewEventsCommand(dockerCli)),
		hide(system.NewInfoCommand(dockerCli)),
		hide(system.NewInspectCommand(dockerCli)),
		hide(container.NewAttachCommand(dockerCli)),
		hide(container.NewCommitCommand(dockerCli)),
		hide(container.NewCopyCommand(dockerCli)),
		hide(container.NewCreateCommand(dockerCli)),
		hide(container.NewDiffCommand(dockerCli)),
		hide(container.NewExecCommand(dockerCli)),
		hide(container.NewExportCommand(dockerCli)),
		hide(container.NewKillCommand(dockerCli)),
		hide(container.NewLogsCommand(dockerCli)),
		hide(container.NewPauseCommand(dockerCli)),
		hide(container.NewPortCommand(dockerCli)),
		hide(container.NewPsCommand(dockerCli)),
		hide(container.NewRenameCommand(dockerCli)),
		hide(container.NewRestartCommand(dockerCli)),
		hide(container.NewRmCommand(dockerCli)),
		hide(container.NewStartCommand(dockerCli)),
		hide(container.NewStatsCommand(dockerCli)),
		hide(container.NewStopCommand(dockerCli)),
		hide(container.NewTopCommand(dockerCli)),
		hide(container.NewUnpauseCommand(dockerCli)),
		hide(container.NewUpdateCommand(dockerCli)),
		hide(container.NewWaitCommand(dockerCli)),
		hide(image.NewHistoryCommand(dockerCli)),
		hide(image.NewImagesCommand(dockerCli)),
		hide(image.NewImportCommand(dockerCli)),
		hide(image.NewLoadCommand(dockerCli)),
		hide(image.NewPullCommand(dockerCli)),
		hide(image.NewPushCommand(dockerCli)),
		hide(image.NewRemoveCommand(dockerCli)),
		hide(image.NewSaveCommand(dockerCli)),
		hide(image.NewTagCommand(dockerCli)),
	)

}

func hide(cmd *cobra.Command) *cobra.Command {
	// If the environment variable with name "DOCKER_HIDE_LEGACY_COMMANDS" is not empty,
	// these legacy commands (such as `docker ps`, `docker exec`, etc)
	// will not be shown in output console.
	if os.Getenv("DOCKER_HIDE_LEGACY_COMMANDS") == "" {
		return cmd
	}
	cmdCopy := *cmd
	cmdCopy.Hidden = true
	cmdCopy.Aliases = []string{}
	return &cmdCopy
}
