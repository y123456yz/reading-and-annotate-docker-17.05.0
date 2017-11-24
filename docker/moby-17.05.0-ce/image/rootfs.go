package image

import (
	"runtime"

	"github.com/Sirupsen/logrus"
	"github.com/docker/docker/layer"
)

// TypeLayers is used for RootFS.Type for filesystems organized into layers.
const TypeLayers = "layers"

// typeLayersWithBase is an older format used by Windows up to v1.12. We
// explicitly handle this as an error case to ensure that a daemon which still
// has an older image like this on disk can still start, even though the
// image itself is not usable. See https://github.com/docker/docker/pull/25806.
const typeLayersWithBase = "layers+base"

// RootFS describes images root filesystem
// This is currently a placeholder that only supports layers. In the future
// this can be made into an interface that supports different implementations.
/*
  "rootfs": {
    "type": "layers",
    "diff_ids": [
      "sha256:51a45fddc531d0138a18ad6f073310daab3a3fe4862997b51b6c8571f3776b62",
      "sha256:5792d8202a821076989a52ced68d1382fc0596f937e7808abbd5ffc1db93fffb",
      "sha256:b7bbef1946d74cdfd84b0db815b4fe9fc9405451190aa65b9eab6ae198c560b4",
      "sha256:15f9e79c2b67f8578e31eda9eb1696b86a10b343ac0e4c50787c5bf01ba55772",
      "sha256:4164b79e9b3832c5a07c9a94c85369bb8f122c9514db51c3e8eb65bc0f2116f9",
      "sha256:368ef39ae0d0020b57d1f2a86ffab5c7454e7bcc8410c0458b0b0934e11c7bdc",
      "sha256:fc91cde1bce45e852ad94bdaa5ef2f8e2e3ebd30f3a253c2dccc0e52b8bd19b4",
      "sha256:34442167f2bc45188d98271ca2ef54030709b2f83dffd4c5af448dd03085f02c"
    ]
  }
*/
//镜像下载生效的地方见 (p *v2Puller) pullSchema2
//type Image struct {}结构中包含该成员，该成员用来存储/var/lib/docker/image/devicemapper/imagedb/content/sha256/$id 中的rootfs {}这一段json信息
type RootFS struct {
	Type    string         `json:"type"`
	DiffIDs []layer.DiffID `json:"diff_ids,omitempty"`    //ChainID()  中会根据 DiffIDs 算出一个chainID
}

// NewRootFS returns empty RootFS struct
func NewRootFS() *RootFS {
	return &RootFS{Type: TypeLayers}
}

// Append appends a new diffID to rootfs
func (r *RootFS) Append(id layer.DiffID) {
	r.DiffIDs = append(r.DiffIDs, id)
}

// ChainID returns the ChainID for the top layer in RootFS.
//根据/var/lib/docker/image/devicemapper/imagedb/content/sha256/$id文件内容中的rootfs中的diff_ids {}json内容算出一个ChainID
func (r *RootFS) ChainID() layer.ChainID {
	if runtime.GOOS == "windows" && r.Type == typeLayersWithBase {
		logrus.Warnf("Layer type is unsupported on this platform. DiffIDs: '%v'", r.DiffIDs)
		return ""
	}

	//根据DiffIDs创建
	return layer.CreateChainID(r.DiffIDs)
}
