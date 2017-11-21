package schema2

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/docker/distribution"
	"github.com/docker/distribution/manifest"
	"github.com/opencontainers/go-digest"
)

const (
	// MediaTypeManifest specifies the mediaType for the current version.
	MediaTypeManifest = "application/vnd.docker.distribution.manifest.v2+json"

	// MediaTypeImageConfig specifies the mediaType for the image configuration.
	MediaTypeImageConfig = "application/vnd.docker.container.image.v1+json"

	// MediaTypePluginConfig specifies the mediaType for plugin configuration.
	MediaTypePluginConfig = "application/vnd.docker.plugin.v1+json"

	// MediaTypeLayer is the mediaType used for layers referenced by the
	// manifest.
	MediaTypeLayer = "application/vnd.docker.image.rootfs.diff.tar.gzip"

	// MediaTypeForeignLayer is the mediaType used for layers that must be
	// downloaded from foreign URLs.
	MediaTypeForeignLayer = "application/vnd.docker.image.rootfs.foreign.diff.tar.gzip"

	// MediaTypeUncompressedLayer is the mediaType used for layers which
	// are not compressed.
	MediaTypeUncompressedLayer = "application/vnd.docker.image.rootfs.diff.tar"
)

var (
	// SchemaVersion provides a pre-initialized version structure for this
	// packages version of the manifest.
	SchemaVersion = manifest.Versioned{
		SchemaVersion: 2,
		MediaType:     MediaTypeManifest,
	}
)

//schema2\manifest.go 中的 init()->schema2Func
func init() {
	////如果头部字段Content-Type内容为"application/vnd.docker.distribution.manifest.v2+json"对应V2,则执行schema2Func
	// 如果头部字段Content-Type内容为"application/json"对应V1，则执行 schema1Func
	//把b中的内容反序列化存储到m中返回，该函数执行在  UnmarshalManifest 中执行
	schema2Func := func(b []byte) (distribution.Manifest, distribution.Descriptor, error) {
		m := new(DeserializedManifest)
		//反序列化b内容填充到 DeserializedManifest 结构
		err := m.UnmarshalJSON(b)
		if err != nil {
			return nil, distribution.Descriptor{}, err
		}

		//根据b内容算出其 digest
		dgst := digest.FromBytes(b)
		return m, distribution.Descriptor{Digest: dgst, Size: int64(len(b)), MediaType: MediaTypeManifest}, err
	}
	err := distribution.RegisterManifestSchema(MediaTypeManifest, schema2Func)
	if err != nil {
		panic(fmt.Sprintf("Unable to register manifest: %s", err))
	}
}

/* http报文体中携带的manifest内容信息   docker pull nginx (ms *manifests) Get 函数获取manifest文件，并打印内容
{
   "schemaVersion": 2,
   "mediaType": "application/vnd.docker.distribution.manifest.v2+json",
   "config": {
      "mediaType": "application/vnd.docker.container.image.v1+json",
      "size": 5836,
      "digest": "sha256:40960efd7b8f44ed5cafee61c189a8f4db39838848d41861898f56c29565266e"
   },
   "layers": [
      {
     "mediaType": "application/vnd.docker.image.rootfs.diff.tar.gzip",
     "size": 22492350,
     "digest": "sha256:bc95e04b23c06ba1b9bf092d07d1493177b218e0340bd2ed49dac351c1e34313"
      },
      {
     "mediaType": "application/vnd.docker.image.rootfs.diff.tar.gzip",
     "size": 21913353,
     "digest": "sha256:a21d9ee25fc3dcef76028536e7191e44554a8088250d4c3ec884af23cef4f02a"
      },
      {
     "mediaType": "application/vnd.docker.image.rootfs.diff.tar.gzip",
     "size": 202,
     "digest": "sha256:9bda7d5afd399f51550422c49172f8c9169fc3ffdef2748b13cfbf6467661ac5"
      }
   ]
}
*/

/*
如果HTTP ctHeader 头部中的resp.Header.Get("Content-Type")为"application/json",则执行 schema1Func，返回 SignedManifest，Descriptor
如果头部字段Content-Type内容为"application/vnd.docker.distribution.manifest.v2+json"对应V2，则执行 schema2Func，返回 DeserializedManifest，Descriptor
如果头部字段Content-Type内容为"application/vnd.docker.distribution.manifest.list.v2+json"则对应 manifestlist，则执行 manifestListFunc，返回 DeserializedManifestList，Descriptor
*/
//schema2Func := func(b []byte) (distribution.Manifest, distribution.Descriptor, error) 或者
//schema1Func := func(b []byte) (distribution.Manifest, distribution.Descriptor, error)
//中把从仓库中下周的manifest内容反序列化存储到该结构中   distribution\manifest\schema2\manifest.go 中的 type Manifest struct 结构
// Manifest defines a schema2 manifest.
//DeserializedManifest 包含该类
type Manifest struct {
	manifest.Versioned

	// Config references the image configuration as a blob.
	Config distribution.Descriptor `json:"config"`

	// Layers lists descriptors for the layers referenced by the
	// configuration.
	Layers []distribution.Descriptor `json:"layers"`
}

// References returnes the descriptors of this manifests references.
func (m Manifest) References() []distribution.Descriptor {
	references := make([]distribution.Descriptor, 0, 1+len(m.Layers))
	references = append(references, m.Config)
	references = append(references, m.Layers...)
	return references
}

// Target returns the target of this signed manifest.
// schema2\manifest.go 中的 (m Manifest) Target()
func (m Manifest) Target() distribution.Descriptor {
	return m.Config
}

// DeserializedManifest wraps Manifest with a copy of the original JSON.
// It satisfies the distribution.Manifest interface.

/* http报文体中携带的manifest内容信息   docker pull nginx
{
   "schemaVersion": 2,
   "mediaType": "application/vnd.docker.distribution.manifest.v2+json",
   "config": {
      "mediaType": "application/vnd.docker.container.image.v1+json",
      "size": 5836,
      "digest": "sha256:40960efd7b8f44ed5cafee61c189a8f4db39838848d41861898f56c29565266e"
   },
   "layers": [
      {
     "mediaType": "application/vnd.docker.image.rootfs.diff.tar.gzip",
     "size": 22492350,
     "digest": "sha256:bc95e04b23c06ba1b9bf092d07d1493177b218e0340bd2ed49dac351c1e34313"
      },
      {
     "mediaType": "application/vnd.docker.image.rootfs.diff.tar.gzip",
     "size": 21913353,
     "digest": "sha256:a21d9ee25fc3dcef76028536e7191e44554a8088250d4c3ec884af23cef4f02a"
      },
      {
     "mediaType": "application/vnd.docker.image.rootfs.diff.tar.gzip",
     "size": 202,
     "digest": "sha256:9bda7d5afd399f51550422c49172f8c9169fc3ffdef2748b13cfbf6467661ac5"
      }
   ]
}
*/

/*
如果HTTP ctHeader 头部中的resp.Header.Get("Content-Type")为"application/json",则执行 schema1Func，返回 SignedManifest，Descriptor
如果头部字段Content-Type内容为"application/vnd.docker.distribution.manifest.v2+json"对应V2，则执行 schema2Func，返回 DeserializedManifest，Descriptor
如果头部字段Content-Type内容为"application/vnd.docker.distribution.manifest.list.v2+json"则对应 manifestlist，则执行 manifestListFunc，返回 DeserializedManifestList，Descriptor
*/
//init->schema2Func 中构造使用
type DeserializedManifest struct {
	//V2 的HTTP包体内容反序列化后的值存入这里，见 (m *DeserializedManifest) UnmarshalJSON
	Manifest

	// canonical is the canonical byte representation of the Manifest.
	//V2 的HTTP包体内容，见 (m *DeserializedManifest) UnmarshalJSON
	canonical []byte //见上面的打印内容为docker pull nginx时候的http包体内容
}

// FromStruct takes a Manifest structure, marshals it to JSON, and returns a
// DeserializedManifest which contains the manifest and its JSON representation.
func FromStruct(m Manifest) (*DeserializedManifest, error) {
	var deserialized DeserializedManifest
	deserialized.Manifest = m

	var err error
	deserialized.canonical, err = json.MarshalIndent(&m, "", "   ")
	return &deserialized, err
}

// UnmarshalJSON populates a new Manifest struct from JSON data.
//反序列化b内容填充到 DeserializedManifest 结构
func (m *DeserializedManifest) UnmarshalJSON(b []byte) error {
	m.canonical = make([]byte, len(b), len(b))
	// store manifest in canonical
	copy(m.canonical, b)

	// Unmarshal canonical JSON into Manifest object
	var manifest Manifest
	if err := json.Unmarshal(m.canonical, &manifest); err != nil {
		return err
	}

	m.Manifest = manifest

	return nil
}

// MarshalJSON returns the contents of canonical. If canonical is empty,
// marshals the inner contents.
func (m *DeserializedManifest) MarshalJSON() ([]byte, error) {
	if len(m.canonical) > 0 {
		return m.canonical, nil
	}

	return nil, errors.New("JSON representation not initialized in DeserializedManifest")
}

// Payload returns the raw content of the manifest. The contents can be used to
// calculate the content identifier.
//返回http请求的 manifest 包体内容中的mediatype和整个manifest内容
func (m DeserializedManifest) Payload() (string, []byte, error) {

	return m.MediaType, m.canonical, nil
}
