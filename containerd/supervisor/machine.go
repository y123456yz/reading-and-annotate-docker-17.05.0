// +build !solaris

package supervisor

import "github.com/cloudfoundry/gosigar"

// Machine holds the current machine cpu count and ram size
type Machine struct {
	Cpus   int
	Memory int64
}

// CollectMachineInformation returns information regarding the current
// machine (e.g. CPU count, RAM amount)
//检查机器信息，返回cpu数量，内存数量。
func CollectMachineInformation() (Machine, error) {
	m := Machine{}
	cpu := sigar.CpuList{}
	if err := cpu.Get(); err != nil {
		return m, err
	}
	m.Cpus = len(cpu.List)
	mem := sigar.Mem{}
	if err := mem.Get(); err != nil {
		return m, err
	}
	m.Memory = int64(mem.Total / 1024 / 1024)
	return m, nil
}
