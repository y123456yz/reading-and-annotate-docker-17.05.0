package registry

import (
	"github.com/docker/distribution/reference"
	registrytypes "github.com/docker/docker/api/types/registry"
)

// RepositoryData tracks the image list, list of endpoints for a repository
type RepositoryData struct {
	// ImgList is a list of images in the repository
	ImgList map[string]*ImgData
	// Endpoints is a list of endpoints returned in X-Docker-Endpoints
	Endpoints []string
}

// ImgData is used to transfer image checksums to and from the registry
type ImgData struct {
	// ID is an opaque string that identifies the image
	ID              string `json:"id"`
	Checksum        string `json:"checksum,omitempty"`
	ChecksumPayload string `json:"-"`
	Tag             string `json:",omitempty"`
}

// PingResult contains the information returned when pinging a registry. It
// indicates the registry's version and whether the registry claims to be a
// standalone registry.
type PingResult struct {
	// Version is the registry version supplied by the registry in an HTTP
	// header
	Version string `json:"version"`
	// Standalone is set to true if the registry indicates it is a
	// standalone registry in the X-Docker-Registry-Standalone
	// header
	Standalone bool `json:"standalone"`
}

// APIVersion is an integral representation of an API version (presently
// either 1 or 2)
type APIVersion int

func (av APIVersion) String() string {
	return apiVersions[av]
}

// API Version identifiers.
const (
	_                      = iota
	APIVersion1 APIVersion = iota
	APIVersion2
)

var apiVersions = map[APIVersion]string{
	APIVersion1: "v1",
	APIVersion2: "v2",
}

/*
// RepositoryInfo Examples:
// {
//   "Index" : {
//     "Name" : "docker.io",
//     "Mirrors" : ["https://registry-2.docker.io/v1/", "https://registry-3.docker.io/v1/"],
//     "Secure" : true,
//     "Official" : true,
//   },
//   "RemoteName" : "library/debian",
//   "LocalName" : "debian",
//   "CanonicalName" : "docker.io/debian"
//   "Official" : true,
// }
//
// {
//   "Index" : {
//     "Name" : "127.0.0.1:5000",
//     "Mirrors" : [],
//     "Secure" : false,
//     "Official" : false,
//   },
//   "RemoteName" : "user/repo",
//   "LocalName" : "127.0.0.1:5000/user/repo",
//   "CanonicalName" : "127.0.0.1:5000/user/repo",
//   "Official" : false,
// }
*/
//可以参考http://www.cnblogs.com/yuhan-TB/p/5053370.html   http://lib.csdn.net/article/docker/58307
// RepositoryInfo describes a repository  RepositoryInfo其实是就是包含了所有可用仓库地址(仓库镜像地址也算)的结构.

//newRepositoryInfo 中构造该类，该结构成员值可以参考上面的注释
type RepositoryInfo struct { //RegistryService实际上是DefaultService.看下imagePullConfig.RegistryService.ResolveRepository(ref),实现在docker\registry\service.go:
	Name reference.Named
		// Index points to registry information
	//Index来源于config.IndexConfigs.那config.IndexConfigs是什么呢?容易发现config.IndexConfigs来源于DefaultService的config。
	//DefaultService的config则来源于NewService时的ServiceOptions。先看下ServiceOptions，实现在docker\registry\config.go：
	Index *registrytypes.IndexInfo  //  IndexInfo 类型，赋值见newRepositoryInfo
	// Official indicates whether the repository is considered official.
	// If the registry is official, and the normalized name does not
	// contain a '/' (e.g. "foo"), then it is considered an official repo.

	//表示是否官方的地址,实际上只要拉取镜像时只传入镜像的信息
	//而没有仓库的信息,就会使用官方默认的仓库地址,这时Official成员就是true
	Official bool
	// Class represents the class of the repository, such as "plugin"
	// or "image".
	Class string
}
