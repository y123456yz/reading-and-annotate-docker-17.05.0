// +build linux,cgo       http://www.cnblogs.com/sevenyuan/p/4544294.html    GO和C语言的互操作   http://blog.csdn.net/grace_yi/article/details/51582073
//https://github.com/steven676/lvm2  libdm源码实现见这里
// Linux中LVM2原理及制作LVM2见: http://blog.csdn.net/u013008795/article/details/51151624
//http://blog.csdn.net/bobpen/article/details/68924208  Docker学习（7）------配置Docker使用Devicemapper
//https://docs.docker.com/engine/userguide/storagedriver/device-mapper-driver/#configure-direct-lvm-mode-for-production
//http://blog.csdn.net/nerdx/article/details/38109931  graph driver-device mapper-01driver初始化
//docker高级应用之动态扩展容器空间大小  http://www.linuxeye.com/Linux/2114.html
//devicemapper 最佳实践  https://my.oschina.net/hippora/blog/681496
//dmsetup   linux内核dm thin pool分析   http://www.cnblogs.com/boring-codeer/p/6187879.html
//http://blog.csdn.net/felix_yujing/article/details/54344251  docker的devicemapper存储驱动    devicemapper驱动官方翻译
//Docker loop volume VS direct LVM 选型和分析    http://blog.csdn.net/gushenbusi/article/details/49494629   devicemapper loop  direct模式和主机的读写性能测试
//devicemapper thinpool配置 http://blog.csdn.net/a85880819/article/details/52457702
//理解docker镜像，容器和存储驱动  http://blog.csdn.net/a85880819/article/details/52448654
//linux下磁盘进行分区、文件系统创建、挂载和卸载    http://www.cnblogs.com/ljy2013/p/4620691.html      文件存储先要把磁盘/dev/sdx设备格式化、分区，然后通过mke2fs制作文件系统，文件存储就是通过目录结构存储文件
//Thin Provisioning Snapshot 演示   http://blog.csdn.net/leo_is_ant/article/details/52935304?locationNum=6&fps=1

package devicemapper

/*
#cgo LDFLAGS: -L. -ldevmapper
#include <libdevmapper.h>
#include <linux/fs.h>   // FIXME: present only for BLKGETSIZE64, maybe we can remove it?

// FIXME: Can't we find a way to do the logging in pure Go?
extern void DevmapperLogCallback(int level, char *file, int line, int dm_errno_or_class, char *str);

static void	log_cb(int level, const char *file, int line, int dm_errno_or_class, const char *f, ...)
{
  char buffer[256];
  va_list ap;

  va_start(ap, f);
  vsnprintf(buffer, 256, f, ap);
  va_end(ap);

  DevmapperLogCallback(level, (char *)file, line, dm_errno_or_class, buffer);
}

static void	log_with_errno_init()
{
  dm_log_with_errno_init(log_cb);
}
*/
import "C"

import (
	"reflect"
	"unsafe"
)

type (
	cdmTask C.struct_dm_task
)

// IOCTL consts
const (
	BlkGetSize64 = C.BLKGETSIZE64
	BlkDiscard   = C.BLKDISCARD
)

// Devicemapper cookie flags.
const (
	DmUdevDisableSubsystemRulesFlag = C.DM_UDEV_DISABLE_SUBSYSTEM_RULES_FLAG
	DmUdevDisableDiskRulesFlag      = C.DM_UDEV_DISABLE_DISK_RULES_FLAG
	DmUdevDisableOtherRulesFlag     = C.DM_UDEV_DISABLE_OTHER_RULES_FLAG
	DmUdevDisableLibraryFallback    = C.DM_UDEV_DISABLE_LIBRARY_FALLBACK
)

// DeviceMapper mapped functions.
var (  //结合lvm2中的 dmsetup.c 阅读
	DmGetLibraryVersion       = dmGetLibraryVersionFct
	DmGetNextTarget           = dmGetNextTargetFct
	DmLogInitVerbose          = dmLogInitVerboseFct
	DmSetDevDir               = dmSetDevDirFct
	DmTaskAddTarget           = dmTaskAddTargetFct
	DmTaskCreate              = dmTaskCreateFct
	DmTaskDestroy             = dmTaskDestroyFct
	DmTaskGetDeps             = dmTaskGetDepsFct
	DmTaskGetInfo             = dmTaskGetInfoFct
	DmTaskGetDriverVersion    = dmTaskGetDriverVersionFct
	DmTaskRun                 = dmTaskRunFct
	DmTaskSetAddNode          = dmTaskSetAddNodeFct
	DmTaskSetCookie           = dmTaskSetCookieFct
	DmTaskSetMessage          = dmTaskSetMessageFct
	DmTaskSetName             = dmTaskSetNameFct
	DmTaskSetRo               = dmTaskSetRoFct
	DmTaskSetSector           = dmTaskSetSectorFct
	DmUdevWait                = dmUdevWaitFct
	DmUdevSetSyncSupport      = dmUdevSetSyncSupportFct
	DmUdevGetSyncSupport      = dmUdevGetSyncSupportFct
	DmCookieSupported         = dmCookieSupportedFct
	LogWithErrnoInit          = logWithErrnoInitFct
	DmTaskDeferredRemove      = dmTaskDeferredRemoveFct
	DmTaskGetInfoWithDeferred = dmTaskGetInfoWithDeferredFct
)

func free(p *C.char) {
	C.free(unsafe.Pointer(p))
}

func dmTaskDestroyFct(task *cdmTask) {
	C.dm_task_destroy((*C.struct_dm_task)(task))
}

func dmTaskCreateFct(taskType int) *cdmTask {
	return (*cdmTask)(C.dm_task_create(C.int(taskType)))
}

func dmTaskRunFct(task *cdmTask) int {
	ret, _ := C.dm_task_run((*C.struct_dm_task)(task))
	return int(ret)
}

func dmTaskSetNameFct(task *cdmTask, name string) int {
	Cname := C.CString(name)
	defer free(Cname)

	return int(C.dm_task_set_name((*C.struct_dm_task)(task), Cname))
}

func dmTaskSetMessageFct(task *cdmTask, message string) int {
	Cmessage := C.CString(message)
	defer free(Cmessage)

	return int(C.dm_task_set_message((*C.struct_dm_task)(task), Cmessage))
}

func dmTaskSetSectorFct(task *cdmTask, sector uint64) int {
	return int(C.dm_task_set_sector((*C.struct_dm_task)(task), C.uint64_t(sector)))
}

func dmTaskSetCookieFct(task *cdmTask, cookie *uint, flags uint16) int {
	cCookie := C.uint32_t(*cookie)
	defer func() {
		*cookie = uint(cCookie)
	}()
	return int(C.dm_task_set_cookie((*C.struct_dm_task)(task), &cCookie, C.uint16_t(flags)))
}

func dmTaskSetAddNodeFct(task *cdmTask, addNode AddNodeType) int {
	return int(C.dm_task_set_add_node((*C.struct_dm_task)(task), C.dm_add_node_t(addNode)))
}

func dmTaskSetRoFct(task *cdmTask) int {
	return int(C.dm_task_set_ro((*C.struct_dm_task)(task)))
}

func dmTaskAddTargetFct(task *cdmTask,
	start, size uint64, ttype, params string) int {

	Cttype := C.CString(ttype)
	defer free(Cttype)

	Cparams := C.CString(params)
	defer free(Cparams)

	return int(C.dm_task_add_target((*C.struct_dm_task)(task), C.uint64_t(start), C.uint64_t(size), Cttype, Cparams))
}

func dmTaskGetDepsFct(task *cdmTask) *Deps {
	Cdeps := C.dm_task_get_deps((*C.struct_dm_task)(task))
	if Cdeps == nil {
		return nil
	}

	// golang issue: https://github.com/golang/go/issues/11925
	hdr := reflect.SliceHeader{
		Data: uintptr(unsafe.Pointer(uintptr(unsafe.Pointer(Cdeps)) + unsafe.Sizeof(*Cdeps))),
		Len:  int(Cdeps.count),
		Cap:  int(Cdeps.count),
	}
	devices := *(*[]C.uint64_t)(unsafe.Pointer(&hdr))

	deps := &Deps{
		Count:  uint32(Cdeps.count),
		Filler: uint32(Cdeps.filler),
	}
	for _, device := range devices {
		deps.Device = append(deps.Device, uint64(device))
	}
	return deps
}

func dmTaskGetInfoFct(task *cdmTask, info *Info) int {
	Cinfo := C.struct_dm_info{}
	defer func() {
		info.Exists = int(Cinfo.exists)
		info.Suspended = int(Cinfo.suspended)
		info.LiveTable = int(Cinfo.live_table)
		info.InactiveTable = int(Cinfo.inactive_table)
		info.OpenCount = int32(Cinfo.open_count)
		info.EventNr = uint32(Cinfo.event_nr)
		info.Major = uint32(Cinfo.major)
		info.Minor = uint32(Cinfo.minor)
		info.ReadOnly = int(Cinfo.read_only)
		info.TargetCount = int32(Cinfo.target_count)
	}()
	return int(C.dm_task_get_info((*C.struct_dm_task)(task), &Cinfo))
}

func dmTaskGetDriverVersionFct(task *cdmTask) string {
	buffer := C.malloc(128)
	defer C.free(buffer)
	res := C.dm_task_get_driver_version((*C.struct_dm_task)(task), (*C.char)(buffer), 128)
	if res == 0 {
		return ""
	}
	return C.GoString((*C.char)(buffer))
}

func dmGetNextTargetFct(task *cdmTask, next unsafe.Pointer, start, length *uint64, target, params *string) unsafe.Pointer {
	var (
		Cstart, Clength      C.uint64_t
		CtargetType, Cparams *C.char
	)
	defer func() {
		*start = uint64(Cstart)
		*length = uint64(Clength)
		*target = C.GoString(CtargetType)
		*params = C.GoString(Cparams)
	}()

	nextp := C.dm_get_next_target((*C.struct_dm_task)(task), next, &Cstart, &Clength, &CtargetType, &Cparams)
	return nextp
}

func dmUdevSetSyncSupportFct(syncWithUdev int) {
	(C.dm_udev_set_sync_support(C.int(syncWithUdev)))
}

func dmUdevGetSyncSupportFct() int {
	return int(C.dm_udev_get_sync_support())
}

func dmUdevWaitFct(cookie uint) int {
	return int(C.dm_udev_wait(C.uint32_t(cookie)))
}

func dmCookieSupportedFct() int {
	return int(C.dm_cookie_supported())
}

func dmLogInitVerboseFct(level int) {
	C.dm_log_init_verbose(C.int(level))
}

func logWithErrnoInitFct() {
	C.log_with_errno_init()
}

//设置   libdm库的 _dm_dir 为/dev/mapper，参考libdm代码 https://github.com/steven676/lvm2
func dmSetDevDirFct(dir string) int {
	Cdir := C.CString(dir)
	defer free(Cdir)

	return int(C.dm_set_dev_dir(Cdir))
}

func dmGetLibraryVersionFct(version *string) int {
	buffer := C.CString(string(make([]byte, 128)))
	defer free(buffer)
	defer func() {
		*version = C.GoString(buffer)
	}()
	return int(C.dm_get_library_version(buffer, 128))
}
