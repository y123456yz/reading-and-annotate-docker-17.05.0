package daemon

import (
	"io"
	"strings"

	dist "github.com/docker/distribution"
	"github.com/docker/distribution/reference"
	"github.com/docker/docker/api/types"
	"github.com/docker/docker/builder"
	"github.com/docker/docker/distribution"
	progressutils "github.com/docker/docker/distribution/utils"
	"github.com/docker/docker/pkg/progress"
	"github.com/docker/docker/registry"
	"github.com/opencontainers/go-digest"
	"golang.org/x/net/context"
)

// PullImage initiates a pull operation. image is the repository name to pull, and
// tag may be either empty, or indicate a specific tag to pull.

//postImagesCreate 中调用该接口
func (daemon *Daemon) PullImage(ctx context.Context, image, tag string, metaHeaders map[string][]string, authConfig *types.AuthConfig, outStream io.Writer) error {
	// Special case: "pull -a" may send an image name with a
	// trailing :. This is ugly, but let's not break API
	// compatibility.
	image = strings.TrimSuffix(image, ":")
	//name格式: xxx:yyy | @zzz  xxx 代表镜像名,如果没有加上仓库地址:docker.io，会使用默认的仓库地址， yyy ：代表版本TAG  zzz： 代表摘要digests
	ref, err := reference.ParseNormalizedNamed(image)
	if err != nil {
		return err
	}

	if tag != "" {//如果tag不为空，则要看tag标签还是摘要digests，或者什么也不是
		// The "tag" could actually be a digest.
		var dgst digest.Digest
		dgst, err = digest.Parse(tag)
		//docker pull mysql@sha256:89cc6ff6a7ac9916c3384e864fb04b8ee9415b572f872a2a4cf5b909dbbca81b 则为digests 89cc6ff6a7ac9916c3384e864fb04b8ee9415b572f872a2a4cf5b909dbbca81b
		//docker pull harbor.intra.XXX.com/XXX/centos:20150101 则为 TAG  20150101
		if err == nil { //digests
			ref, err = reference.WithDigest(reference.TrimNamed(ref), dgst)
		} else {
			ref, err = reference.WithTag(ref, tag)
		}
		if err != nil {
			return err
		}
	}

	//docker pull  mysql@sha256:8d9cc6ff6a7ac9916c3384e864fb04b8ee9415b572f872a2a4c5b909dbbca81b ref对应 docker.io/library/mysql@sha256:8d9cc6ff6a7ac9916c3384e864fb04b8ee9415b572f872a2a4c5b909dbbca81b
	//docker pull harbor.intra.XXX.com/XXX/centos:20150101 ref对应 harbor.intra.XXX.com/XXX/centos:20150101
	return daemon.pullImageWithReference(ctx, ref, metaHeaders, authConfig, outStream)
}

// PullOnBuild tells Docker to pull image referenced by `name`.
func (daemon *Daemon) PullOnBuild(ctx context.Context, name string, authConfigs map[string]types.AuthConfig, output io.Writer) (builder.Image, error) {
	ref, err := reference.ParseNormalizedNamed(name)
	if err != nil {
		return nil, err
	}
	ref = reference.TagNameOnly(ref)

	pullRegistryAuth := &types.AuthConfig{}
	if len(authConfigs) > 0 {
		// The request came with a full auth config file, we prefer to use that
		repoInfo, err := daemon.RegistryService.ResolveRepository(ref)
		if err != nil {
			return nil, err
		}

		resolvedConfig := registry.ResolveAuthConfig(
			authConfigs,
			repoInfo.Index,
		)
		pullRegistryAuth = &resolvedConfig
	}

	if err := daemon.pullImageWithReference(ctx, ref, nil, pullRegistryAuth, output); err != nil {
		return nil, err
	}
	return daemon.GetImage(name)
}
//docker pull  mysql@sha256:8d9cc6ff6a7ac9916c3384e864fb04b8ee9415b572f872a2a4c5b909dbbca81b ref对应 docker.io/library/mysql@sha256:8d9cc6ff6a7ac9916c3384e864fb04b8ee9415b572f872a2a4c5b909dbbca81b
//docker pull harbor.intra.XXX.com/XXX/centos:20150101 ref对应 harbor.intra.XXX.com/XXX/centos:20150101
func (daemon *Daemon) pullImageWithReference(ctx context.Context, ref reference.Named, metaHeaders map[string][]string, authConfig *types.AuthConfig, outStream io.Writer) error {
	// Include a buffer so that slow client connections don't affect
	// transfer performance.

	//progressChan 通道是用来显示pull操作的状态
	progressChan := make(chan progress.Progress, 100)
	//writesDone 用来等待协程处理结束
	writesDone := make(chan struct{}) //writesDone 用来等待协程处理结束

	ctx, cancelFunc := context.WithCancel(ctx)

	go func() {
		progressutils.WriteDistributionProgress(cancelFunc, outStream, progressChan)
		close(writesDone)
	}()

	//构造了一个imagePullConfig 对象，里面包含了很多拉取镜像时要用到的信息
	imagePullConfig := &distribution.ImagePullConfig {
		Config: distribution.Config{
			MetaHeaders:      metaHeaders,
			AuthConfig:       authConfig,
			ProgressOutput:   progress.ChanOutput(progressChan),
			RegistryService:  daemon.RegistryService,
			ImageEventLogger: daemon.LogImageEvent,
			MetadataStore:    daemon.distributionMetadataStore,
			ImageStore:       distribution.NewImageConfigStoreFromStore(daemon.imageStore),
			ReferenceStore:   daemon.referenceStore,
		},
		DownloadManager: daemon.downloadManager,
		Schema2Types:    distribution.ImageTypes,
	}

	//获取仓库端信息，遍历端点，根据api版本创建V1或V2版本的puller的Pull 函数
	err := distribution.Pull(ctx, ref, imagePullConfig)
	close(progressChan)
	<-writesDone
	return err
}

// GetRepository returns a repository from the registry.
func (daemon *Daemon) GetRepository(ctx context.Context, ref reference.NamedTagged, authConfig *types.AuthConfig) (dist.Repository, bool, error) {
	// get repository info
	repoInfo, err := daemon.RegistryService.ResolveRepository(ref)
	if err != nil {
		return nil, false, err
	}
	// makes sure name is not empty or `scratch`
	if err := distribution.ValidateRepoName(repoInfo.Name); err != nil {
		return nil, false, err
	}

	// get endpoints
	endpoints, err := daemon.RegistryService.LookupPullEndpoints(reference.Domain(repoInfo.Name))
	if err != nil {
		return nil, false, err
	}

	// retrieve repository
	var (
		confirmedV2 bool
		repository  dist.Repository
		lastError   error
	)

	for _, endpoint := range endpoints {
		if endpoint.Version == registry.APIVersion1 {
			continue
		}

		repository, confirmedV2, lastError = distribution.NewV2Repository(ctx, repoInfo, endpoint, nil, authConfig, "pull")
		if lastError == nil && confirmedV2 {
			break
		}
	}
	return repository, confirmedV2, lastError
}
