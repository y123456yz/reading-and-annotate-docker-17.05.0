package image

import (
	"github.com/spf13/cobra"

	"github.com/docker/docker/cli"
	"github.com/docker/docker/cli/command"
)

/*
拉取镜像的命令： Docker pull NAME[:TAG|@DIGEST] ，TAG为标签，DIGEST为数字摘要，也就是拉取镜像可以附带TAG或数字摘要等参数，或只使用镜像名（默认latest）。如果参数带TAG则使用NamedTagged描述 ，如果参数带DIGEST则使用Canonical 描述。
$ docker images --digests
$ docker mysql@sha256:89cc6ff6a7ac9916c3384e864fb04b8ee9415b572f872a2a4cf5b909dbbca81b
$ docker pull library/mysql
*/
// NewImageCommand returns a cobra command for `image` subcommands
func NewImageCommand(dockerCli *command.DockerCli) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "image",
		Short: "Manage images",
		Args:  cli.NoArgs,
		RunE:  dockerCli.ShowHelp,
	}
	cmd.AddCommand(
		NewBuildCommand(dockerCli),
		NewHistoryCommand(dockerCli),
		NewImportCommand(dockerCli),
		NewLoadCommand(dockerCli),
		NewPullCommand(dockerCli),
		NewPushCommand(dockerCli),
		NewSaveCommand(dockerCli),
		NewTagCommand(dockerCli),
		newListCommand(dockerCli),
		newRemoveCommand(dockerCli),
		newInspectCommand(dockerCli),
		NewPruneCommand(dockerCli),
	)
	return cmd
}
