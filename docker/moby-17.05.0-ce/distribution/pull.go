package distribution

import (
	"fmt"

	"github.com/Sirupsen/logrus"
	"github.com/docker/distribution/reference"
	"github.com/docker/docker/api"
	"github.com/docker/docker/distribution/metadata"
	"github.com/docker/docker/pkg/progress"
	refstore "github.com/docker/docker/reference"
	"github.com/docker/docker/registry"
	"github.com/opencontainers/go-digest"
	"golang.org/x/net/context"
)

// Puller is an interface that abstracts pulling for different API versions.
type Puller interface {
	// Pull tries to pull the image referenced by `tag`
	// Pull returns an error if any, as well as a boolean that determines whether to retry Pull on the next configured endpoint.
	//
	Pull(ctx context.Context, ref reference.Named) error
}

// newPuller returns a Puller interface that will pull from either a v1 or v2
// registry. The endpoint argument contains a Version field that determines
// whether a v1 or v2 puller will be created. The other parameters are passed
// through to the underlying puller implementation for use during the actual
// pull operation.
//distribution\pull.go中的 Pull 接口调用该函数
//NewPuller会根据endpoint的形式（endpoint应该遵循restful api的设计，url中含有版本号），决定采用version1还是version2版本，我主要分析v2的版本，在graph/pull_v2.go中
func newPuller(endpoint registry.APIEndpoint, repoInfo *registry.RepositoryInfo, imagePullConfig *ImagePullConfig) (Puller, error) {
	switch endpoint.Version {
	case registry.APIVersion2:
		return &v2Puller{
			V2MetadataService: metadata.NewV2MetadataService(imagePullConfig.MetadataStore),
			endpoint:          endpoint,
			config:            imagePullConfig,
			repoInfo:          repoInfo,
		}, nil
	case registry.APIVersion1:
		return &v1Puller{
			v1IDService: metadata.NewV1IDService(imagePullConfig.MetadataStore),
			endpoint:    endpoint,
			config:      imagePullConfig,
			repoInfo:    repoInfo,
		}, nil
	}
	return nil, fmt.Errorf("unknown version %d for registry %s", endpoint.Version, endpoint.URL)
}

// Pull initiates a pull operation. image is the repository name to pull, and
// tag may be either empty, or indicate a specific tag to pull.

//docker pull  mysql@sha256:8d9cc6ff6a7ac9916c3384e864fb04b8ee9415b572f872a2a4c5b909dbbca81b ref对应 docker.io/library/mysql@sha256:8d9cc6ff6a7ac9916c3384e864fb04b8ee9415b572f872a2a4c5b909dbbca81b
//docker pull harbor.intra.XXX.com/XXX/centos:20150101 ref对应 harbor.intra.XXX.com/XXX/centos:20150101

//(daemon *Daemon) pullImageWithReference 调用该函数执行
func Pull(ctx context.Context, ref reference.Named, imagePullConfig *ImagePullConfig) error {
	// Resolve the Repository name from fqn to RepositoryInfo
	//在/docker/registry/config.go的 newServiceConfig初始化仓库地址和仓库镜像地址，其中有官方的和通过选项insecure-registry
	// 自定义的私有仓库,实质是通过IndexName找到IndexInfo，有用的也只有IndexName
	//这里的imagePullConfig.RegistryService 为daemon.RegistryService，也即是docker\registry\service.go的DefaultService
	//初始化时,会将insecure-registry选项和registry-mirrors存入ServiceOptions,在NewService函数被调用时,作为参入传入

	//repoInfo为 RepositoryInfo 对象,其实是对reference.Named对象的封装,添加了镜像成员和官方标示
	//(s *DefaultService) ResolveRepository
	repoInfo, err := imagePullConfig.RegistryService.ResolveRepository(ref) //构造仓库地址信息
	if err != nil {
		return err
	}

	// makes sure name is not `scratch`
	//为了确保不为空
	if err := ValidateRepoName(repoInfo.Name); err != nil {
		return err
	}

	// /docker/cmd/dockerddaemon.go----大约125 和 248
	//如果没有镜像仓库服务器地址，默认使用V2仓库地址registry-1.docker.io
	//Hostname()函数来源于Named

	//实质上如果Hostname()返回的是官方仓库地址,则endpoint的URL将是registry-1.docker.io,如果有镜像则会添加镜像作为endpoint
	// 否则就是私有地址的两种类型:http和https

	//V2的接口具体代码在Zdocker\registry\service_v2.go的函数lookupV2Endpoints
	//(s *DefaultService) LookupPullEndpoints     reference.Domain(repoInfo.Name) docker pull abc.com/xx/abcdef:456456 中的 abc.com
	endpoints, err := imagePullConfig.RegistryService.LookupPullEndpoints(reference.Domain(repoInfo.Name)) //通过repositoryInfo找到下载镜像的endpoint
	if err != nil {
		return err
	}

	var (
		lastErr error

		// discardNoSupportErrors is used to track whether an endpoint encountered an error of type registry.ErrNoSupport
		// By default it is false, which means that if an ErrNoSupport error is encountered, it will be saved in lastErr.
		// As soon as another kind of error is encountered, discardNoSupportErrors is set to true, avoiding the saving of
		// any subsequent ErrNoSupport errors in lastErr.
		// It's needed for pull-by-digest on v1 endpoints: if there are only v1 endpoints configured, the error should be
		// returned and displayed, but if there was a v2 endpoint which supports pull-by-digest, then the last relevant
		// error is the ones from v2 endpoints not v1.
		discardNoSupportErrors bool

		// confirmedV2 is set to true if a pull attempt managed to
		// confirm that it was talking to a v2 registry. This will
		// prevent fallback to the v1 protocol.
		confirmedV2 bool

		// confirmedTLSRegistries is a map indicating which registries
		// are known to be using TLS. There should never be a plaintext
		// retry for any of these.
		confirmedTLSRegistries = make(map[string]struct{})
	)
	//如果设置了镜像服务器地址,且使用了官方默认的镜像仓库,则endpoints包含官方仓库地址和镜像服务器地址,否则就是私有仓库地址的http和https形式
	for _, endpoint := range endpoints { //找到endpoints, 然后puller.Pull

		if imagePullConfig.RequireSchema2 && endpoint.Version == registry.APIVersion1 {
			continue
		}

		if confirmedV2 && endpoint.Version == registry.APIVersion1 {
			logrus.Debugf("Skipping v1 endpoint %s because v2 registry was detected", endpoint.URL)
			continue
		}

		if endpoint.URL.Scheme != "https" {
			if _, confirmedTLS := confirmedTLSRegistries[endpoint.URL.Host]; confirmedTLS {
				logrus.Debugf("Skipping non-TLS endpoint %s for host/port that appears to use TLS", endpoint.URL)
				continue
			}
		}

		/*
		[root@newnamespace ~]# cat /etc/docker/daemon.json
		{"registry-mirrors": ["http://a9e61d46.m.daocloud.io"]}

		例如:docker pull abcdef:456456   如果没有指定url，则默认优先使用daemon.json中的配置registry-mirrors，如果该地址连不上或者无镜像，则从默认的registry-1.docker.io获取，并且都是V2
		Trying to pull abcdef from http://a9e61d46.m.daocloud.io/ v2
		Trying to pull abcdef from https://registry-1.docker.io v2

		例如： docker pull abc.com/xx/abcdef:456456  如果指定有url，则可以通过V2和V1从指定的url获取镜像
		Trying to pull abc.com/xx/abcdef from https://abc.com v2
		Trying to pull abc.com/xx/abcdef from https://abc.com v1
		*/

		logrus.Debugf("Trying to pull %s from %s %s", reference.FamiliarName(repoInfo.Name), endpoint.URL, endpoint.Version)
		//针对每一个endpoint，建立一个Puller,newPuller会根据endpoint的形式（endpoint应该遵循restful api的设计，url中含有版本号），
		// 决定采用version1还是version2版本
		//imagePullConfig是个很重要的对象,包含了很多镜像操作相关的对象  针对每一个endpoint，建立一个Puller
		puller, err := newPuller(endpoint, repoInfo, imagePullConfig)
		if err != nil {
			lastErr = err
			continue
		}

		// (p *v2Puller) Pull 或者  (p *v1Puller) Pull
		//pullRepository 或者 pullV2Repository
		if err := puller.Pull(ctx, ref); err != nil {
			// Was this pull cancelled? If so, don't try to fall
			// back.
			fallback := false
			select {
			case <-ctx.Done():
			default:
				if fallbackErr, ok := err.(fallbackError); ok {
					fallback = true
					confirmedV2 = confirmedV2 || fallbackErr.confirmedV2
					if fallbackErr.transportOK && endpoint.URL.Scheme == "https" {
						confirmedTLSRegistries[endpoint.URL.Host] = struct{}{}
					}
					err = fallbackErr.err
				}
			}
			if fallback {
				if _, ok := err.(ErrNoSupport); !ok {
					// Because we found an error that's not ErrNoSupport, discard all subsequent ErrNoSupport errors.
					discardNoSupportErrors = true
					// append subsequent errors
					lastErr = err
				} else if !discardNoSupportErrors {
					// Save the ErrNoSupport error, because it's either the first error or all encountered errors
					// were also ErrNoSupport errors.
					// append subsequent errors
					lastErr = err
				}
				logrus.Infof("Attempting next endpoint for pull after error: %v", err)
				continue
			}
			logrus.Errorf("Not continuing with pull after error: %v", err)
			return TranslatePullError(err, ref)
		}

		imagePullConfig.ImageEventLogger(reference.FamiliarString(ref), reference.FamiliarName(repoInfo.Name), "pull")
		return nil
	}

	if lastErr == nil {
		lastErr = fmt.Errorf("no endpoints found for %s", reference.FamiliarString(ref))
	}

	return TranslatePullError(lastErr, ref)
}

// writeStatus writes a status message to out. If layersDownloaded is true, the
// status message indicates that a newer image was downloaded. Otherwise, it
// indicates that the image is up to date. requestedTag is the tag the message
// will refer to.
func writeStatus(requestedTag string, out progress.Output, layersDownloaded bool) {
	if layersDownloaded {
		progress.Message(out, "", "Status: Downloaded newer image for "+requestedTag)
	} else {
		progress.Message(out, "", "Status: Image is up to date for "+requestedTag)
	}
}

// ValidateRepoName validates the name of a repository.
func ValidateRepoName(name reference.Named) error {
	if reference.FamiliarName(name) == api.NoBaseImageSpecifier {
		return fmt.Errorf("'%s' is a reserved name", api.NoBaseImageSpecifier)
	}
	return nil
}

func addDigestReference(store refstore.Store, ref reference.Named, dgst digest.Digest, id digest.Digest) error {
	dgstRef, err := reference.WithDigest(reference.TrimNamed(ref), dgst)
	if err != nil {
		return err
	}

	if oldTagID, err := store.Get(dgstRef); err == nil {
		if oldTagID != id {
			// Updating digests not supported by reference store
			logrus.Errorf("Image ID for digest %s changed from %s to %s, cannot update", dgst.String(), oldTagID, id)
		}
		return nil
	} else if err != refstore.ErrDoesNotExist {
		return err
	}

	return store.AddDigest(dgstRef, id, true)
}
