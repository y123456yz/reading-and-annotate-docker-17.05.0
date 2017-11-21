package manifest

// Versioned provides a struct with the manifest schemaVersion and mediaType.
// Incoming content with unknown schema version can be decoded against this
// struct to check the version.
//例如 distribution\manifest\schema2\manifest.go 中的 Manifest 就包含该结构
/*
如果HTTP ctHeader 头部中的resp.Header.Get("Content-Type")为"application/json",则执行 schema1Func，返回 SignedManifest，Descriptor
如果头部字段Content-Type内容为"application/vnd.docker.distribution.manifest.v2+json"对应V2，则执行 schema2Func，返回 DeserializedManifest，Descriptor
如果头部字段Content-Type内容为"application/vnd.docker.distribution.manifest.list.v2+json"则对应 manifestlist，则执行 manifestListFunc，返回 DeserializedManifestList，Descriptor
*/
//SignedManifest.Manifest   DeserializedManifest.Manifest   DeserializedManifestList.Manifest 都包含该接口
type Versioned struct {
	// SchemaVersion is the image manifest schema that this image follows
	SchemaVersion int `json:"schemaVersion"`

	// MediaType is the media type of this schema.
	MediaType string `json:"mediaType,omitempty"`
}
