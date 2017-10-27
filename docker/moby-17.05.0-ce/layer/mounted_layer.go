package layer

import (
	"io"

	"github.com/docker/docker/pkg/archive"
)

/*
docker中定义了 Layer 和 RWLayer 两种接口，分别用来定义只读层和可读写层的一些操作，又定义了roLayer和mountedLayer,分别实现这两种接口。
其中 roLayer 用于藐视不可改变的镜像层，mountedLayer 用于表视可读写的容器层
*/

/*  参考http://licyhust.com/%E5%AE%B9%E5%99%A8%E6%8A%80%E6%9C%AF/2016/09/27/docker-image-data-structure/
mounts本质上是一个map，类型为map[string]*mountedLayer。前面提到过mounts存放的其实是每个容器可写的layer的信息，他们的元数据存
放在/var/lib/docker/image/{driver}/layerdb/mounts目录下。而mountedLayer则是这些可写的layer在内存中的结构
*/

/*
参考 https://segmentfault.com/a/1190000009769352
*/

//roLayer 是只读的layer原信息，mounts是运行容器的时候可写layer
//roLayer 存储只读镜像层信息，见loadLayer  mountedLayer 存储只读层(容器层)信息，见loadMount
//初始化实例见CreateRWLayer  layerStore 包含该成员类型， loadMount 中初始化赋值该类，   //setRWLayer->CreateRWLayer创建容器的时候，也会构建新的mountedLayer
type mountedLayer struct { //参考loadMount 读取/var/lib/docker/image/devicemapper/layerdb/mounts/containerId目录下面的文件内容存入 mountedLayer
//mountedLayer 存储的内容主要是索引某个容器的可读写层(也叫容器层)的ID(也对应容器的ID)
//只读层元数据的持久化位于 /var/lib/docker/image/[graphdriver]/imagedb/metadata/sha256/[chainID]/文件夹下
// 可读写层(也叫容器层)存储在 /var/lib/docker/image/[graph_driver]/layerdb/mounts/[chain_id]/路径下
	name       string  // /var/lib/docker/image/devicemapper/layerdb/mounts/containerId 中的containerId
	//initID和mountID表示了这个layer数据存放的位置，和 roLayer.CacheId一样。
	mountID    string  //读写层ID  根据创建容器时指定的容器名，来生成一容器ID随机随机数，赋值见CreateRWLayer
	initID     string  //容器init层的ID
	parent     *roLayer //父镜像ID，也就是只读层  该容器层对应的镜像层ID，也就是创建容器的时候需要指定的镜像ID
	path       string
	layerStore *layerStore

	references map[RWLayer]*referencedRWLayer
}

func (ml *mountedLayer) cacheParent() string {
	if ml.initID != "" {
		return ml.initID
	}
	if ml.parent != nil {
		return ml.parent.cacheID
	}
	return ""
}

func (ml *mountedLayer) TarStream() (io.ReadCloser, error) {
	return ml.layerStore.driver.Diff(ml.mountID, ml.cacheParent())
}

func (ml *mountedLayer) Name() string {
	return ml.name
}

func (ml *mountedLayer) Parent() Layer {
	if ml.parent != nil {
		return ml.parent
	}

	// Return a nil interface instead of an interface wrapping a nil
	// pointer.
	return nil
}

func (ml *mountedLayer) Size() (int64, error) {
	return ml.layerStore.driver.DiffSize(ml.mountID, ml.cacheParent())
}

func (ml *mountedLayer) Changes() ([]archive.Change, error) {
	return ml.layerStore.driver.Changes(ml.mountID, ml.cacheParent())
}

func (ml *mountedLayer) Metadata() (map[string]string, error) {
	return ml.layerStore.driver.GetMetadata(ml.mountID)
}

func (ml *mountedLayer) getReference() RWLayer {
	ref := &referencedRWLayer{
		mountedLayer: ml,
	}
	ml.references[ref] = ref

	return ref
}

func (ml *mountedLayer) hasReferences() bool {
	return len(ml.references) > 0
}

func (ml *mountedLayer) deleteReference(ref RWLayer) error {
	if _, ok := ml.references[ref]; !ok {
		return ErrLayerNotRetained
	}
	delete(ml.references, ref)
	return nil
}

func (ml *mountedLayer) retakeReference(r RWLayer) {
	if ref, ok := r.(*referencedRWLayer); ok {
		ml.references[ref] = ref
	}
}

type referencedRWLayer struct {
	*mountedLayer
}

func (rl *referencedRWLayer) Mount(mountLabel string) (string, error) {
	return rl.layerStore.driver.Get(rl.mountedLayer.mountID, mountLabel)
}

// Unmount decrements the activity count and unmounts the underlying layer
// Callers should only call `Unmount` once per call to `Mount`, even on error.
func (rl *referencedRWLayer) Unmount() error {
	return rl.layerStore.driver.Put(rl.mountedLayer.mountID)
}
