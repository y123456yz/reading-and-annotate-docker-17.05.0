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
cacheID,容器层(mountedLayer)中为mountID。 mountID是随机生成的并保存在mountedLayer的元数据mountID中

referencedCacheLayer 中包含 roLayer
*/

//loadLayer 中初始化构造该结构， layerStore 结构包含该成员类型
//注意 roLayer mountedLayer 和 layerStore 的关系  layerStore 包含 roLayer mountedLayer成员
//roLayer 存储只读镜像层信息，见loadLayer  mountedLayer 存储只读层(容器层)信息，见loadMount
type roLayer struct { //对应/var/lib/docker/image/overlay/layerdb/sha256/目录相关
/*  参考http://licyhust.com/%E5%AE%B9%E5%99%A8%E6%8A%80%E6%9C%AF/2016/09/27/docker-image-data-structure/
diff-id：通过docker pull下载镜像时，镜像的json文件中每一个layer都有一个唯一的diff-id
chain-id：chain-id是根据parent的chain-id和自身的diff-id生成的，假如没有parent，则chain-id等于diff-id，假如有parent，则chain-id等于sha256sum( “parent-chain-id diff-id”)
cache-id：随机生成的64个16进制数。cache-id标识了这个layer的数据具体存放位置  #cache-id是docker下载layer的时候在本地生成的一个随机uuid，指向真正存放layer文件的地方

//只读层元数据的持久化位于 /var/lib/docker/image/devicemapper/layerdb/sha256/[chainID]/文件夹下
// 可读写层(也叫容器层)存储在 /var/lib/docker/image/[graph_driver]/layerdb/mounts/[chain_id]/路径下

在layer的所有属性中，diffID采用SHA256算法，基于镜像层文件包的内容计算得到。而chainID是基于内容存储的索引，它是根据当前层与所有祖先镜像层
diffID计算出来的，具体算法如下:
1. 如果该镜像层是最底层(没有父镜像层)，该层的diffID便是chainID.
2. 该镜像层的chainID计算公式为chainID(n)=SHA256(chain(n-1) diffID(n))

镜像xxxx的属性信息存在下面这里：  参考https://segmentfault.com/a/1190000009730986
/var/lib/docker/image/devicemapper/imagedb/content/sha256/xxxx
chainID计算过程：假设某个镜像diff_ids如下  cat /var/lib/docker/image/devicemapper/imagedb/content/sha256/xxxx | jq .
  ....
  "rootfs": {
    "type": "layers",
    "diff_ids": [
      "sha256:51a45fddc531d0138a18ad6f073310daab3a3fe4862997b51b6c8571f3776b62",  #1
      "sha256:5792d8202a821076989a52ced68d1382fc0596f937e7808abbd5ffc1db93fffb",  #2
      "sha256:b7bbef1946d74cdfd84b0db815b4fe9fc9405451190aa65b9eab6ae198c560b4",  #3
    ]
  }

      镜像xxxx包含三层只读层，每层的diff_id如上。
      docker计算chainid时，用到了所有祖先layer的信息，从而能保证根据chainid得到的rootfs是唯一的。比如我在debian和ubuntu的image基
  础上都添加了一个同样的文件，那么commit之后新增加的这两个layer具有相同的内容，相同的diffid，但由于他们的父layer不一样，所以他们
  的chainid会不一样，从而根据chainid能找到唯一的rootfs。
      docker通过#1 #2 #3从仓库里面拉取各层内容的时候，存放在那呢？例如#1对应的只读层内容存到哪里？给每层计算一个chainid，然后在把该层
  相关内容记录到/var/lib/docker/image/devicemapper/layerdb/sha256/$chainID目录下的相关文件。
      #1的chainID就是#1的sha256,因为他没有parent父层ID，它就是最底层。
      root@fd-mesos-xxx.gz01:/var/lib/docker/image/devicemapper/layerdb/sha256$ ls 51a45fddc531d0138a18ad6f073310daab3a3fe4862997b51b6c8571f3776b62/
	cache-id  diff  size  tar-split.json.gz   //注意没有parent文件
      root@fd-mesos-xxx.gz01:/var/lib/docker/image/devicemapper/layerdb/sha256$cat 51a45fddc531d0138a18ad6f073310daab3a3fe4862997b51b6c8571f3776b62/diff
	sha256:51a45fddc531d0138a18ad6f073310daab3a3fe4862997b51b6c8571f3776b62 //diff和diff_ids第一层一样


      #2的chainID计算方法：(父层的chainID(第一层chainID)和第二层的diff_id计算sha256sum的结果)
      root@fd-mesos-xxx.gz01:/var/lib/docker/image/devicemapper/layerdb/sha256$ echo -n "sha256:51a45fddc531d0138a18ad6f073310daab3a3fe4862997b51b6c8571f3776b62 sha256:5792d8202a821076989a52ced68d1382fc0596f937e7808abbd5ffc1db93fffb" | sha256sum
      e299130128d155d60bac3991100c2cda6a35c5ad0b542a5ffab2679654dfd445  -
      root@fd-mesos-master04.gz01:/var/lib/docker/image/devicemapper/layerdb/sha256$ cat e299130128d155d60bac3991100c2cda6a35c5ad0b542a5ffab2679654dfd445/diff
      sha256:5792d8202a821076989a52ced68d1382fc0596f937e7808abbd5ffc1db93fffb  //diff内容就和diff_ids第二层一样
      root@fd-mesos-master04.gz01:/var/lib/docker/image/devicemapper/layerdb/sha256$
      root@fd-mesos-master04.gz01:/var/lib/docker/image/devicemapper/layerdb/sha256$ cat e299130128d155d60bac3991100c2cda6a35c5ad0b542a5ffab2679654dfd445/parent
      sha256:51a45fddc531d0138a18ad6f073310daab3a3fe4862997b51b6c8571f3776b62  //parent内容就是第一层chainID
      root@fd-mesos-master04.gz01:/var/lib/docker/image/devicemapper/layerdb/sha256$
      root@fd-mesos-master04.gz01:/var/lib/docker/image/devicemapper/layerdb/sha256$

      #3的chainID计算方法：(父层的chainID(第二层chainID)和第三层的diff_id计算sha256sum的结果)
        root@fd-mesos-xxx.gz01:/var/lib/docker/image/devicemapper/layerdb/sha256$ echo -n "sha256:e299130128d155d60bac3991100c2cda6a35c5ad0b542a5ffab2679654dfd445 sha256:b7bbef1946d74cdfd84b0db815b4fe9fc9405451190aa65b9eab6ae198c560b4" | sha256sum
	c6c38436b063046117fb9b4210a54c0d29aa8b5f350964d1723468e6a324e1a8  -
	root@fd-mesos-master04.gz01:/var/lib/docker/image/devicemapper/layerdb/sha256$ ls c6c38436b063046117fb9b4210a54c0d29aa8b5f350964d1723468e6a324e1a8/
	cache-id  diff  parent  size  tar-split.json.gz
	root@fd-mesos-master04.gz01:/var/lib/docker/image/devicemapper/layerdb/sha256$ cat c6c38436b063046117fb9b4210a54c0d29aa8b5f350964d1723468e6a324e1a8/parent
	sha256:e299130128d155d60bac3991100c2cda6a35c5ad0b542a5ffab2679654dfd445  //parent内容就是第二层chainID
	root@fd-mesos-master04.gz01:/var/lib/docker/image/devicemapper/layerdb/sha256$
	root@fd-mesos-master04.gz01:/var/lib/docker/image/devicemapper/layerdb/sha256$ cat c6c38436b063046117fb9b4210a54c0d29aa8b5f350964d1723468e6a324e1a8/diff
	sha256:b7bbef1946d74cdfd84b0db815b4fe9fc9405451190aa65b9eab6ae198c560b4 //diff内容就和diff_ids第二层一样
	root@fd-mesos-master04.gz01:/var/lib/docker/image/devicemapper/layerdb/sha256$
*/
	chainID    ChainID //chainID和parent可以从所属image元数据计算出来
	// /var/lib/docker/image/devicemapper/layerdb/sha256/$chainID/diff内容也就是/var/lib/docker/image/devicemapper/imagedb/content/sha256/$image中的diff_ids对应的层
	diffID     DiffID  //diffID和size可以通过一个该镜像层包计算出来
	//赋值见loadLayer 也就是/var/lib/docker/image/devicemapper/imagedb/content/sha256/$image中的diff_ids本层的上一层diffID
	parent     *roLayer  //每一层都包括指向父层的指针。如果没有这个指针，说明处于最底层。
	//在docker宿主机上随机生成的uuid,在当前宿主机上与该镜像层一一对应，用于标识和索引graphdriver中的镜像层文件
	//cache-id是docker下载layer的时候在本地生成的一个随机uuid，指向真正存放layer文件的地方
	cacheID    string //知名layer数据存放位置，/var/lib/docker/devicemapper/metadata/cache-id

	size       int64 //diffID和size可以通过一个该镜像层包计算出来
	layerStore *layerStore
	descriptor distribution.Descriptor

	//referentces存放的是他的子layer的信息。当删除镜像时，只有roLayer的referentceCount为零时，才能够删除该layer。
	//可以被子镜像层引用，也可以被容器层引用，还可以被/var/lib/docker/image/devicemapper/imagedb/content/sha256中的diff_ids计算出的ChinaID引用，可以搜索 referenceCount++
	referenceCount int  //该镜像层被容器层、镜像层、和/var/lib/docker/image/devicemapper/imagedb/content/sha256中的diff_ids引用的次数，
	references     map[Layer]struct{} //赋值参考getReference
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

type referencedCacheLayer struct { //下面的getReference 中会使用到该类
	*roLayer
}

func (rl *roLayer) getReference() Layer { //referencedCacheLayer 中的roLayer实现 Layer 中的各种方法
	ref := &referencedCacheLayer{
		roLayer: rl,  //把rl存入referencedCacheLayer
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
