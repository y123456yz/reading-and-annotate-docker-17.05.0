package layer

import (
	"fmt"
	"io"

	"github.com/docker/distribution"
	"github.com/opencontainers/go-digest"
	//"database/sql/driver"
)
//参考http://licyhust.com/%E5%AE%B9%E5%99%A8%E6%8A%80%E6%9C%AF/2016/09/27/docker-image-data-structure/
//store本质上是磁盘上保存了各个layer的元数据信息，当docker初始化时，它会利用
//这些元数据文件在内存中构造各个layer，每个Layer都用一个roLayer结构体表示，即只读(ro)的layer
//注意roLayer 和 layerStore 的关系


/*
docker中定义了 Layer 和 RWLayer 两种接口，分别用来定义只读层和可读写层的一些操作，又定义了roLayer和mountedLayer,分别实现这两种接口。
其中 roLayer 用于表视不可改变的镜像层，mountedLayer 用于表视可读写的容器层

docker镜像管理部分和存储驱动在设计上完全分离了，镜像层或者容器层在存储驱动中拥有一个新的标示ID，在镜像层(roLayer)中称为
cacheID,容器层(mountedLayer)中为mountID。 mountID是随机生成的并保存在mountedLayer的元数据mountID中，持久化到
*/

//roLayer是只读的layer原信息，mounts是运行容器的时候可写layer
type roLayer struct { //对应/var/lib/docker/image/overlay/layerdb/sha256/目录相关
/*  参考http://licyhust.com/%E5%AE%B9%E5%99%A8%E6%8A%80%E6%9C%AF/2016/09/27/docker-image-data-structure/
diff-id：通过docker pull下载镜像时，镜像的json文件中每一个layer都有一个唯一的diff-id
chain-id：chain-id是根据parent的chain-id和自身的diff-id生成的，假如没有parent，则chain-id等于diff-id，假如有parent，则chain-id等于sha256sum( “parent-chain-id diff-id”)
cache-id：随机生成的64个16进制数。前面提到过，cache-id标识了这个layer的数据具体存放位置
*/
	chainID    ChainID //
	diffID     DiffID
	parent     *roLayer  //每一层都包括指向父层的指针。如果没有这个指针，说明处于最底层。
	cacheID    string //知名layer数据存放位置，/var/lib/docker/devicemapper/metadata/cache-id


	size       int64
	layerStore *layerStore
	descriptor distribution.Descriptor

	//referentces存放的是他的子layer的信息。当删除镜像时，只有roLayer的referentceCount为零时，才能够删除该layer。
	referenceCount int
	references     map[Layer]struct{}
}

// TarStream for roLayer guarantees that the data that is produced is the exact
// data that the layer was registered with.
func (rl *roLayer) TarStream() (io.ReadCloser, error) {
	rc, err := rl.layerStore.getTarStream(rl)
	if err != nil {
		return nil, err
	}

	vrc, err := newVerifiedReadCloser(rc, digest.Digest(rl.diffID))
	if err != nil {
		return nil, err
	}
	return vrc, nil
}

// TarStreamFrom does not make any guarantees to the correctness of the produced
// data. As such it should not be used when the layer content must be verified
// to be an exact match to the registered layer.
func (rl *roLayer) TarStreamFrom(parent ChainID) (io.ReadCloser, error) {
	var parentCacheID string
	for pl := rl.parent; pl != nil; pl = pl.parent {
		if pl.chainID == parent {
			parentCacheID = pl.cacheID
			break
		}
	}

	if parent != ChainID("") && parentCacheID == "" {
		return nil, fmt.Errorf("layer ID '%s' is not a parent of the specified layer: cannot provide diff to non-parent", parent)
	}
	return rl.layerStore.driver.Diff(rl.cacheID, parentCacheID)
}

func (rl *roLayer) ChainID() ChainID {
	return rl.chainID
}

func (rl *roLayer) DiffID() DiffID {
	return rl.diffID
}

func (rl *roLayer) Parent() Layer {
	if rl.parent == nil {
		return nil
	}
	return rl.parent
}

func (rl *roLayer) Size() (size int64, err error) {
	if rl.parent != nil {
		size, err = rl.parent.Size()
		if err != nil {
			return
		}
	}

	return size + rl.size, nil
}

func (rl *roLayer) DiffSize() (size int64, err error) {
	return rl.size, nil
}

func (rl *roLayer) Metadata() (map[string]string, error) {
	return rl.layerStore.driver.GetMetadata(rl.cacheID)
}

type referencedCacheLayer struct {
	*roLayer
}

func (rl *roLayer) getReference() Layer {
	ref := &referencedCacheLayer{
		roLayer: rl,
	}
	rl.references[ref] = struct{}{}

	return ref
}

func (rl *roLayer) hasReference(ref Layer) bool {
	_, ok := rl.references[ref]
	return ok
}

func (rl *roLayer) hasReferences() bool {
	return len(rl.references) > 0
}

func (rl *roLayer) deleteReference(ref Layer) {
	delete(rl.references, ref)
}

func (rl *roLayer) depth() int {
	if rl.parent == nil {
		return 1
	}
	return rl.parent.depth() + 1
}

func storeLayer(tx MetadataTransaction, layer *roLayer) error {
	if err := tx.SetDiffID(layer.diffID); err != nil {
		return err
	}
	if err := tx.SetSize(layer.size); err != nil {
		return err
	}
	if err := tx.SetCacheID(layer.cacheID); err != nil {
		return err
	}
	// Do not store empty descriptors
	if layer.descriptor.Digest != "" {
		if err := tx.SetDescriptor(layer.descriptor); err != nil {
			return err
		}
	}
	if layer.parent != nil {
		if err := tx.SetParent(layer.parent.chainID); err != nil {
			return err
		}
	}

	return nil
}

func newVerifiedReadCloser(rc io.ReadCloser, dgst digest.Digest) (io.ReadCloser, error) {
	return &verifiedReadCloser{
		rc:       rc,
		dgst:     dgst,
		verifier: dgst.Verifier(),
	}, nil
}

type verifiedReadCloser struct {
	rc       io.ReadCloser
	dgst     digest.Digest
	verifier digest.Verifier
}

func (vrc *verifiedReadCloser) Read(p []byte) (n int, err error) {
	n, err = vrc.rc.Read(p)
	if n > 0 {
		if n, err := vrc.verifier.Write(p[:n]); err != nil {
			return n, err
		}
	}
	if err == io.EOF {
		if !vrc.verifier.Verified() {
			err = fmt.Errorf("could not verify layer data for: %s. This may be because internal files in the layer store were modified. Re-pulling or rebuilding this image may resolve the issue", vrc.dgst)
		}
	}
	return
}
func (vrc *verifiedReadCloser) Close() error {
	return vrc.rc.Close()
}
