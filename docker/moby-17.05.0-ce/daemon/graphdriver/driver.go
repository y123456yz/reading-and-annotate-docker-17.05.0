package graphdriver

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/Sirupsen/logrus"
	"github.com/vbatts/tar-split/tar/storage"

	"github.com/docker/docker/pkg/archive"
	"github.com/docker/docker/pkg/idtools"
	"github.com/docker/docker/pkg/plugingetter"
)

// FsMagic unsigned id of the filesystem in use.
type FsMagic uint32

const (
	// FsMagicUnsupported is a predefined constant value other than a valid filesystem id.
	FsMagicUnsupported = FsMagic(0x00000000)
)

var (
	// All registered drivers
	//可以搜索 graphdriver.Register，例如devicemapper driver对应的driver注册见 graphdriver\devmapper\driver.go
	//overlay 对应的为 graphdriver\overlay\driver.go
	drivers map[string]InitFunc    //下面的 Register 中注册map信息    InitFunc执行见 GetDriver () 见graphdriver\driver.go中的init

	// ErrNotSupported returned when driver is not supported.
	ErrNotSupported = errors.New("driver not supported")
	// ErrPrerequisites returned when driver does not meet prerequisites.
	ErrPrerequisites = errors.New("prerequisites for driver not satisfied (wrong filesystem?)")
	// ErrIncompatibleFS returned when file system is not supported.
	ErrIncompatibleFS = fmt.Errorf("backing file system is unsupported for this graph driver")
)

//CreateOpts contains optional arguments for Create() and CreateReadWrite()
// methods.
type CreateOpts struct {
	MountLabel string
	StorageOpt map[string]string
}

// InitFunc initializes the storage driver.
type InitFunc func(root string, options []string, uidMaps, gidMaps []idtools.IDMap) (Driver, error)

// ProtoDriver defines the basic capabilities of a driver.
// This interface exists solely to be a minimum set of methods
// for client code which choose not to implement the entire Driver
// interface and use the NaiveDiffDriver wrapper constructor.
//
// Use of ProtoDriver directly by client code is not recommended.
//例如devicemapper驱动的相应接口在 graphdriver\driver.go 中的Driver结构中包含有一下这些接口实现
type ProtoDriver interface {  //包含在 NaiveDiffDriver 结构中       type Driver interface {}也包含该接口
	// String returns a string representation of this driver.
	String() string   //代表这个驱动的字符串
	// CreateReadWrite creates a new, empty filesystem layer that is ready
	// to be used as the storage for a container. Additional options can
	// be passed in opts. parent may be "" and opts may be nil.

	//创建一个可读写层
	CreateReadWrite(id, parent string, opts *CreateOpts) error
	// Create creates a new, empty, filesystem layer with the
	// specified id and parent and options passed in opts. Parent
	// may be "" and opts may be nil.

	//创建一个新的镜像层，需要调用者传入一个唯一的ID和所需父镜像的ID
	Create(id, parent string, opts *CreateOpts) error

	//根据指定的ID删除一层
	// Remove attempts to remove the filesystem layer with this id.
	Remove(id string) error
	// Get returns the mountpoint for the layered filesystem referred
	// to by this id. You can optionally specify a mountLabel or "".
	// Returns the absolute path to the mounted layered filesystem.
	//返回指定的层的挂载点的绝对路径
	Get(id, mountLabel string) (dir string, err error)
	// Put releases the system resources for the specified id,
	// e.g, unmounting layered filesystem.
	Put(id string) error //释放一个层使用的资源，比如卸载一个已经挂载的层
	// Exists returns whether a filesystem layer with the specified
	// ID exists on this driver.
	Exists(id string) bool  //查找指定的ID对应的层是否存在
	// Status returns a set of key-value pairs which give low
	// level diagnostic status about this driver.
	Status() [][2]string  //返回这个驱动的状态，这个状态用一些键值对表示
	// Returns a set of key-value pairs which give low level information
	// about the image/container driver is managing.
	GetMetadata(id string) (map[string]string, error)
	// Cleanup performs necessary tasks to release resources
	// held by the driver, e.g., unmounting all layered filesystems
	// known to this driver.
	Cleanup() error   //释放由这个驱动管理的所有资源，比如卸载所有的层
}

// DiffDriver is the interface to use to implement graph diffs
type DiffDriver interface { //type Driver interface {}包含该接口
	// Diff produces an archive of the changes between the specified
	// layer and its parent layer which may be "".
	//将指定ID的层相对父镜像层改动的文件打包并返回
	Diff(id, parent string) (io.ReadCloser, error)
	// Changes produces a list of changes between the specified layer
	// and its parent layer. If parent is "", then all changes will be ADD changes.
	//返回指定镜像层与父镜像层之间的差异列表
	Changes(id, parent string) ([]archive.Change, error)
	// ApplyDiff extracts the changeset from the given diff into the
	// layer with the specified id and parent, returning the size of the
	// new layer in bytes.
	// The archive.Reader must be an uncompressed stream.

	//从差异文件包中提取差异列表，并应用到指定ID的层与父镜像层，返回新镜像层的大小
	ApplyDiff(id, parent string, diff io.Reader) (size int64, err error)
	// DiffSize calculates the changes between the specified id
	// and its parent and returns the size in bytes of the changes
	// relative to its base filesystem directory.

	//计算指定ID层与其父镜像层的差异，并返回差异相对于基础文件系统的大小
	DiffSize(id, parent string) (size int64, err error)
}

// Driver is the interface for layered/snapshot file system drivers.
type Driver interface { //layerStore 包含该接口成员
	ProtoDriver
	DiffDriver  //有些driver没有DiffDriver接口，例如devicemapper
}

// Capabilities defines a list of capabilities a driver may implement.
// These capabilities are not required; however, they do determine how a
// graphdriver can be used.
type Capabilities struct {
	// Flags that this driver is capable of reproducing exactly equivalent
	// diffs for read-only layers. If set, clients can rely on the driver
	// for consistent tar streams, and avoid extra processing to account
	// for potential differences (eg: the layer store's use of tar-split).
	ReproducesExactDiffs bool
}

// CapabilityDriver is the interface for layered file system drivers that
// can report on their Capabilities.
type CapabilityDriver interface {
	Capabilities() Capabilities
}

// DiffGetterDriver is the interface for layered file system drivers that
// provide a specialized function for getting file contents for tar-split.
type DiffGetterDriver interface {
	Driver
	// DiffGetter returns an interface to efficiently retrieve the contents
	// of files in a layer.
	DiffGetter(id string) (FileGetCloser, error)
}

// FileGetCloser extends the storage.FileGetter interface with a Close method
// for cleaning up.
type FileGetCloser interface {
	storage.FileGetter
	// Close cleans up any resources associated with the FileGetCloser.
	Close() error
}

// Checker makes checks on specified filesystems.
type Checker interface {
	// IsMounted returns true if the provided path is mounted for the specific checker
	IsMounted(path string) bool
}

func init() {
	drivers = make(map[string]InitFunc)
}

// Register registers an InitFunc for the driver.
//可以搜索 graphdriver.Register，例如devicemapper driver对应的driver注册见 graphdriver\devmapper\driver.go
//overlay 对应的为 graphdriver\overlay\driver.go
func Register(name string, initFunc InitFunc) error { //NewDaemon->NewStoreFromOptions->graphdriver.New->GetDriver->init  存储驱动初始化调用流程
	if _, exists := drivers[name]; exists {
		return fmt.Errorf("Name already registered %s", name)
	}
	drivers[name] = initFunc

	return nil
}

////NewDaemon->NewStoreFromOptions->graphdriver.New->GetDriver->init
// GetDriver initializes and returns the registered driver     name 为 devicemapper  overlay  vfs等
func GetDriver(name string, pg plugingetter.PluginGetter, config Options) (Driver, error) {
	if initFunc, exists := drivers[name]; exists {
		//  /var/lib/docker/devicemapper    devicemapper 这里的initfunc对应的就是 graphdriver.Register("devicemapper", Init)中
		// 的init，见graphdriver\devmapper\driver.go中的init
		//执行对应driver的init函数
		return initFunc(filepath.Join(config.Root, name), config.DriverOptions, config.UIDMaps, config.GIDMaps)
	}

	pluginDriver, err := lookupPlugin(name, pg, config)
	if err == nil {
		return pluginDriver, nil
	}
	logrus.WithError(err).WithField("driver", name).WithField("home-dir", config.Root).Error("Failed to GetDriver graph")
	return nil, ErrNotSupported
}

// getBuiltinDriver initializes and returns the registered driver, but does not try to load from plugins
func getBuiltinDriver(name, home string, options []string, uidMaps, gidMaps []idtools.IDMap) (Driver, error) {
	if initFunc, exists := drivers[name]; exists {
		return initFunc(filepath.Join(home, name), options, uidMaps, gidMaps)
	}
	logrus.Errorf("Failed to built-in GetDriver graph %s %s", name, home)
	return nil, ErrNotSupported
}

// Options is used to initialize a graphdriver    --storage-opt dm.basesize=20G命令行参数配置
//赋值见NewStoreFromOptions
type Options struct {  //命令行中写带的graphdriver storage相关参数在 NewDeviceSet 中生效
	Root                string
	DriverOptions       []string   //options选项可以为 dm.basesize dm.loopdatasize等，见NewDeviceSet
	UIDMaps             []idtools.IDMap
	GIDMaps             []idtools.IDMap
	ExperimentalEnabled bool
}

// New creates the driver and initializes it at the specified root.
//NewDaemon->NewStoreFromOptions->graphdriver.New    name 为 devicemapper  overlay  vfs等
func New(name string, pg plugingetter.PluginGetter, config Options) (Driver, error) {  //存储驱动的初始化

	if name != "" { //NewDaemon->NewStoreFromOptions->graphdriver.New->GetDriver
		logrus.Debugf("[graphdriver] trying provided driver: %s", name) // so the logs show specified driver
		return GetDriver(name, pg, config)
	}

	// Guess for prior driver
	driversMap := scanPriorDrivers(config.Root)
	for _, name := range priority {
		if name == "vfs" {
			// don't use vfs even if there is state present.
			continue
		}
		if _, prior := driversMap[name]; prior {
			// of the state found from prior drivers, check in order of our priority
			// which we would prefer
			driver, err := getBuiltinDriver(name, config.Root, config.DriverOptions, config.UIDMaps, config.GIDMaps)
			if err != nil {
				// unlike below, we will return error here, because there is prior
				// state, and now it is no longer supported/prereq/compatible, so
				// something changed and needs attention. Otherwise the daemon's
				// images would just "disappear".
				logrus.Errorf("[graphdriver] prior storage driver %s failed: %s", name, err)
				return nil, err
			}

			// abort starting when there are other prior configured drivers
			// to ensure the user explicitly selects the driver to load
			if len(driversMap)-1 > 0 {
				var driversSlice []string
				for name := range driversMap {
					driversSlice = append(driversSlice, name)
				}

				return nil, fmt.Errorf("%s contains several valid graphdrivers: %s; Please cleanup or explicitly choose storage driver (-s <DRIVER>)", config.Root, strings.Join(driversSlice, ", "))
			}

			logrus.Infof("[graphdriver] using prior storage driver: %s", name)
			return driver, nil
		}
	}

	// Check for priority drivers first
	for _, name := range priority {
		driver, err := getBuiltinDriver(name, config.Root, config.DriverOptions, config.UIDMaps, config.GIDMaps)
		if err != nil {
			if isDriverNotSupported(err) {
				continue
			}
			return nil, err
		}
		return driver, nil
	}

	// Check all registered drivers if no priority driver is found
	for name, initFunc := range drivers {
		driver, err := initFunc(filepath.Join(config.Root, name), config.DriverOptions, config.UIDMaps, config.GIDMaps)
		if err != nil {
			if isDriverNotSupported(err) {
				continue
			}
			return nil, err
		}
		return driver, nil
	}
	return nil, fmt.Errorf("No supported storage backend found")
}

// isDriverNotSupported returns true if the error initializing
// the graph driver is a non-supported error.
func isDriverNotSupported(err error) bool {
	return err == ErrNotSupported || err == ErrPrerequisites || err == ErrIncompatibleFS
}

// scanPriorDrivers returns an un-ordered scan of directories of prior storage drivers
func scanPriorDrivers(root string) map[string]bool {
	driversMap := make(map[string]bool)

	for driver := range drivers {
		p := filepath.Join(root, driver)
		if _, err := os.Stat(p); err == nil && driver != "vfs" {
			driversMap[driver] = true
		}
	}
	return driversMap
}
