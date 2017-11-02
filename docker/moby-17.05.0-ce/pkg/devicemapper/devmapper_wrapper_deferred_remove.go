// +build linux,cgo,!libdm_no_deferred_remove

/*  是否支持deferred_remove，在 hack/make.sh 中的测试程序来判断
# test whether "libdevmapper.h" is new enough to support deferred remove
# functionality.
if \
	command -v gcc &> /dev/null \
	&& ! ( echo -e  '#include <libdevmapper.h>\nint main() { dm_task_deferred_remove(NULL); }'| gcc -xc - -o /dev/null -ldevmapper &> /dev/null ) \
; then
       DOCKER_BUILDTAGS+=' libdm_no_deferred_remove'
fi
*/
//需要下载lvm2最新版本，见 https://www.mirrorservice.org/sites/sourceware.org/pub/lvm2/releases/，老版本不支持defer lvm ，
// 安装新版本LVM2后需要手动替换lib库，注意是替换编译docker代码的容器中，不是主机上的  注意把/lib/x86_64-linux-gnu/

package devicemapper

/*
#cgo LDFLAGS: -L. -ldevmapper
#include <libdevmapper.h>
*/
import "C"

// LibraryDeferredRemovalSupport is supported when statically linked.
const LibraryDeferredRemovalSupport = true   //libdm是否支持Deferred removal

func dmTaskDeferredRemoveFct(task *cdmTask) int {
	return int(C.dm_task_deferred_remove((*C.struct_dm_task)(task)))
}

func dmTaskGetInfoWithDeferredFct(task *cdmTask, info *Info) int {
	logrus.Debugf("yang test: dmTaskGetInfoWithDeferredFct")
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
		info.DeferredRemove = int(Cinfo.deferred_remove)
	}()
	return int(C.dm_task_get_info((*C.struct_dm_task)(task), &Cinfo))
}
