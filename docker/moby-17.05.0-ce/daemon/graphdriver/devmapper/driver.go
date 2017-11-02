// +build linux

package devmapper

import (
	"fmt"
	"io/ioutil"
	"os"
	"path"
	"strconv"

	"github.com/Sirupsen/logrus"

	"github.com/docker/docker/daemon/graphdriver"
	"github.com/docker/docker/pkg/devicemapper"
	"github.com/docker/docker/pkg/idtools"
	"github.com/docker/docker/pkg/locker"
	"github.com/docker/docker/pkg/mount"
	units "github.com/docker/go-units"
)

func init() { //NewDaemon->NewStoreFromOptions->graphdriver.New->GetDriver->init  初始化调用流程
	graphdriver.Register("devicemapper", Init)   //Init在 GetDriver 中执行 见graphdriver\driver.go
}

// Driver contains the device set mounted and the home directory
type Driver struct {  //初始化赋值见下面的 Init 函数    该 driver除了包含这些结构成员外，还包括 ProtoDriver 接口实现的 Status GetMetadata 等函数功能
	*DeviceSet
	home    string  // /var/lib/docker/devicemapper   //home默认为/var/lib/docker/devicemapper
	uidMaps []idtools.IDMap
	gidMaps []idtools.IDMap
	ctr     *graphdriver.RefCounter
	locker  *locker.Locker
}

// Init creates a driver with the given home and the set of options.
//Init在 GetDriver 中执行 见graphdriver\driver.go
//这里的home默认为/var/lib/docker/devicemapper
func Init(home string, options []string, uidMaps, gidMaps []idtools.IDMap) (graphdriver.Driver, error) {
	deviceSet, err := NewDeviceSet(home, true, options, uidMaps, gidMaps)
	if err != nil {
		return nil, err
	}

	if err := mount.MakePrivate(home); err != nil {
		return nil, err
	}

	d := &Driver{
		DeviceSet: deviceSet,
		home:      home,
		uidMaps:   uidMaps,
		gidMaps:   gidMaps,
		ctr:       graphdriver.NewRefCounter(graphdriver.NewDefaultChecker()),
		locker:    locker.New(),
	}

	//d包含ProtoDriver接口的函数功能   //例如devicemapper，这里的driver对应 graphdriver\driver.go中的 Driver结构
	return graphdriver.NewNaiveDiffDriver(d, uidMaps, gidMaps), nil
}

func (d *Driver) String() string {
	return "devicemapper"
}

// Status returns the status about the driver in a printable format.
// Information returned contains Pool Name, Data File, Metadata file, disk usage by
// the data and metadata, etc.
func (d *Driver) Status() [][2]string {
	s := d.DeviceSet.Status()

	status := [][2]string{
		{"Pool Name", s.PoolName},
		{"Pool Blocksize", fmt.Sprintf("%s", units.HumanSize(float64(s.SectorSize)))},
		{"Base Device Size", fmt.Sprintf("%s", units.HumanSize(float64(s.BaseDeviceSize)))},
		{"Backing Filesystem", s.BaseDeviceFS},
		{"Data file", s.DataFile},
		{"Metadata file", s.MetadataFile},
		{"Data Space Used", fmt.Sprintf("%s", units.HumanSize(float64(s.Data.Used)))},
		{"Data Space Total", fmt.Sprintf("%s", units.HumanSize(float64(s.Data.Total)))},
		{"Data Space Available", fmt.Sprintf("%s", units.HumanSize(float64(s.Data.Available)))},
		{"Metadata Space Used", fmt.Sprintf("%s", units.HumanSize(float64(s.Metadata.Used)))},
		{"Metadata Space Total", fmt.Sprintf("%s", units.HumanSize(float64(s.Metadata.Total)))},
		{"Metadata Space Available", fmt.Sprintf("%s", units.HumanSize(float64(s.Metadata.Available)))},
		{"Thin Pool Minimum Free Space", fmt.Sprintf("%s", units.HumanSize(float64(s.MinFreeSpace)))},
		{"Udev Sync Supported", fmt.Sprintf("%v", s.UdevSyncSupported)},
		{"Deferred Removal Enabled", fmt.Sprintf("%v", s.DeferredRemoveEnabled)},
		{"Deferred Deletion Enabled", fmt.Sprintf("%v", s.DeferredDeleteEnabled)},
		{"Deferred Deleted Device Count", fmt.Sprintf("%v", s.DeferredDeletedDeviceCount)},
	}
	if len(s.DataLoopback) > 0 {
		status = append(status, [2]string{"Data loop file", s.DataLoopback})
	}
	if len(s.MetadataLoopback) > 0 {
		status = append(status, [2]string{"Metadata loop file", s.MetadataLoopback})
	}
	if vStr, err := devicemapper.GetLibraryVersion(); err == nil {
		status = append(status, [2]string{"Library Version", vStr})
	}
	return status
}

// GetMetadata returns a map of information about the device.
func (d *Driver) GetMetadata(id string) (map[string]string, error) {
	m, err := d.DeviceSet.exportDeviceMetadata(id)

	if err != nil {
		return nil, err
	}

	metadata := make(map[string]string)
	metadata["DeviceId"] = strconv.Itoa(m.deviceID)
	metadata["DeviceSize"] = strconv.FormatUint(m.deviceSize, 10)
	metadata["DeviceName"] = m.deviceName
	return metadata, nil
}

// Cleanup unmounts a device.  //  清除thin pool
func (d *Driver) Cleanup() error {
	err := d.DeviceSet.Shutdown(d.home)   // 停止thin pool

	if err2 := mount.Unmount(d.home); err == nil {
		err = err2
	}

	return err
}

// CreateReadWrite creates a layer that is writable for use as a container
// file system.   (ls *layerStore) initMount()中调用执行  id为容器的ID，parent为依赖的镜像相关
// 创建一个可读写层  (ls *layerStore) initMount 中执行

//parent为顶层镜像层对应的cacheID,cacheID中的metaID是该镜像层中真正存储数据的元信息地址，指向/var/lib/docker/devicemapper/metadata/$metaID
//id为///var/lib/docker/image/devicemapper/layerdb/mounts/$containerID/中的init-id或者mount-id文件中的内容
// parent为创建容器时候需要的最顶层镜像层chainID目录中的cache-id中的内容(也就是roLayer.cacheID)
func (d *Driver) CreateReadWrite(id, parent string, opts *graphdriver.CreateOpts) error {
	return d.Create(id, parent, opts) //为id层创建对应的存储device
}

// Create adds a device with a given id and the parent.
//为 id 层创建对应的device
//  当加载新镜像时，添加一个新thin device
//  id为containerid或imageid
func (d *Driver) Create(id, parent string, opts *graphdriver.CreateOpts) error {
	var storageOpt map[string]string
	if opts != nil {
		storageOpt = opts.StorageOpt
	}

	if err := d.DeviceSet.AddDevice(id, parent, storageOpt); err != nil {
		return err
	}

	return nil
}

// Remove removes a device with a given id, unmounts the filesystem.
func (d *Driver) Remove(id string) error { //  删除thin device
	d.locker.Lock(id)
	defer d.locker.Unlock(id)
	if !d.DeviceSet.HasDevice(id) {  //检查thin device是否存在
		// Consider removing a non-existing device a no-op
		// This is useful to be able to progress on container removal
		// if the underlying device has gone away due to earlier errors
		return nil
	}

	// This assumes the device has been properly Get/Put:ed and thus is unmounted
	if err := d.DeviceSet.DeleteDevice(id, false); err != nil {  //通过id从thin pool中删除设备
		return fmt.Errorf("failed to remove device %s: %v", id, err)
	}

	//mp为/var/lib/docker/devicemapper/mnt/$id
	mp := path.Join(d.home, "mnt", id)
	//删除目录下所有的文件
	if err := os.RemoveAll(mp); err != nil && !os.IsNotExist(err) {
		return err
	}

	return nil
}

//initMount 中执行   id为mountID(/var/lib/docker/image/devicemapper/layerdb/mounts/$chainID/$mountID文件的内容对应的/var/lib/docker/devicemapper/mnt/$mountID)
// Get mounts a device with given id into the root filesystem

//  创建/var/lib/docker/devicemapper/mnt/$mountID
//  挂载thin device到/var/lib/docker/devicemapper/mnt/$mountID 目录下
func (d *Driver) Get(id, mountLabel string) (string, error) {
	d.locker.Lock(id)
	defer d.locker.Unlock(id)
	mp := path.Join(d.home, "mnt", id)  // /var/lib/docker/devicemapper/mnt/$mountID
	rootFs := path.Join(mp, "rootfs")   // /var/lib/docker/devicemapper/mnt/$mountID/rootfs
	if count := d.ctr.Increment(mp); count > 1 {
		return rootFs, nil
	}

	uid, gid, err := idtools.GetRootUIDGID(d.uidMaps, d.gidMaps)
	if err != nil {
		d.ctr.Decrement(mp)
		return "", err
	}

	// Create the target directories if they don't exist
	/*
	mnt目录
	里面存放的是经过devicemapper文件系统mount之后的layer数据，即当前layer和所有的下层layer合并之后的数据，对于devicemapper文件系统来说，
	只有在运行容器的时候才会被docker所mount，所以容器没启动的时候，这里看不到任何文件。
	/var/lib/docker/devicemapper/mnt/xxxx
	*/
	if err := idtools.MkdirAllAs(path.Join(d.home, "mnt"), 0755, uid, gid); err != nil && !os.IsExist(err) {
		d.ctr.Decrement(mp)
		return "", err
	}
	if err := idtools.MkdirAs(mp, 0755, uid, gid); err != nil && !os.IsExist(err) {
		d.ctr.Decrement(mp)
		return "", err
	}

	// Mount the device
	//挂载thin device到/var/lib/docker/devicemapper/mnt/$id
	if err := d.DeviceSet.MountDevice(id, mp, mountLabel); err != nil {
		d.ctr.Decrement(mp)
		return "", err
	}

	if err := idtools.MkdirAllAs(rootFs, 0755, uid, gid); err != nil && !os.IsExist(err) {
		d.ctr.Decrement(mp)
		d.DeviceSet.UnmountDevice(id, mp)
		return "", err
	}

	//创建id
	idFile := path.Join(mp, "id")
	if _, err := os.Stat(idFile); err != nil && os.IsNotExist(err) {
		// Create an "id" file with the container/image id in it to help reconstruct this in case
		// of later problems
		if err := ioutil.WriteFile(idFile, []byte(id), 0600); err != nil {
			d.ctr.Decrement(mp)
			d.DeviceSet.UnmountDevice(id, mp)
			return "", err
		}
	}

	//返回/var/lib/docker/devicemapper/mnt/$id/rootfs目录
	return rootFs, nil
}

// Put unmounts a device and removes it.
//  从/var/lib/docker/devicemapper/mnt/$id下解挂设备
func (d *Driver) Put(id string) error {
	d.locker.Lock(id)
	defer d.locker.Unlock(id)
	mp := path.Join(d.home, "mnt", id)
	if count := d.ctr.Decrement(mp); count > 0 {
		return nil
	}
	err := d.DeviceSet.UnmountDevice(id, mp)
	if err != nil {
		logrus.Errorf("devmapper: Error unmounting device %s: %s", id, err)
	}
	return err
}

// Exists checks to see if the device exists.
//  判断$id所对应的设备是否存在
func (d *Driver) Exists(id string) bool {
	return d.DeviceSet.HasDevice(id)
}
