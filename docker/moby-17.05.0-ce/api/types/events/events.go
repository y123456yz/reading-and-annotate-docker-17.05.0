package events

/*
[root@newnamespace ~]# docker events
docker run
2017-11-24T19:46:18.706668287+08:00 container kill 7e6e1d3b91811348fffd2ad6046e6c76806bb8f7d5425e4c92bffe4867819f1a (image=69f833c62773, name=affectionate_heyrovsky, signal=15)
2017-11-24T19:46:18.728771592+08:00 container die 7e6e1d3b91811348fffd2ad6046e6c76806bb8f7d5425e4c92bffe4867819f1a (exitCode=0, image=69f833c62773, name=affectionate_heyrovsky)
2017-11-24T19:46:18.854277901+08:00 network disconnect bb56d33c0c90e44f6b242f852cfe514b71c89b67f5776fda062fc5884bc43cea (container=7e6e1d3b91811348fffd2ad6046e6c76806bb8f7d5425e4c92bffe4867819f1a, name=bridge, type=bridge)
2017-11-24T19:46:18.854791433+08:00 volume unmount 2507271bc4a0a4357a573d3014aa6e02d3e5f5b1093d6e743f7e40ee44899f20 (container=7e6e1d3b91811348fffd2ad6046e6c76806bb8f7d5425e4c92bffe4867819f1a, driver=local)
2017-11-24T19:46:18.950990360+08:00 container stop 7e6e1d3b91811348fffd2ad6046e6c76806bb8f7d5425e4c92bffe4867819f1a (image=69f833c62773, name=affectionate_heyrovsky)
2017-11-24T19:46:18.971417271+08:00 container destroy 7e6e1d3b91811348fffd2ad6046e6c76806bb8f7d5425e4c92bffe4867819f1a (image=69f833c62773, name=affectionate_heyrovsky)

kill dockerpid
2017-11-24T19:46:39.767880115+08:00 container create 5f87d5b5dee60b6f3cb776867b0bef02da9e80a5b79daa21e6f6b128c740c342 (image=69f833c62773, name=blissful_swartz)
2017-11-24T19:46:39.769776870+08:00 container attach 5f87d5b5dee60b6f3cb776867b0bef02da9e80a5b79daa21e6f6b128c740c342 (image=69f833c62773, name=blissful_swartz)
2017-11-24T19:46:39.895904755+08:00 network connect bb56d33c0c90e44f6b242f852cfe514b71c89b67f5776fda062fc5884bc43cea (container=5f87d5b5dee60b6f3cb776867b0bef02da9e80a5b79daa21e6f6b128c740c342, name=bridge, type=bridge)
2017-11-24T19:46:39.924114766+08:00 volume mount 98133697be4eba55665a21cb25cb092ee1d8ca93efd8385759eb5cf51ee6c801 (container=5f87d5b5dee60b6f3cb776867b0bef02da9e80a5b79daa21e6f6b128c740c342, destination=/var/lib/docker, driver=local, propagation=, read/write=true)
2017-11-24T19:46:40.068171536+08:00 container start 5f87d5b5dee60b6f3cb776867b0bef02da9e80a5b79daa21e6f6b128c740c342 (image=69f833c62773, name=blissful_swartz)
2017-11-24T19:46:40.070413280+08:00 container resize 5f87d5b5dee60b6f3cb776867b0bef02da9e80a5b79daa21e6f6b128c740c342 (height=67, image=69f833c62773, name=blissful_swartz, width=236)
*/
const ( //docker events 可以监控所有的这些event
	//注意 ContainerEventType (daemon.EventsService.Log通知给docker events客户端) 和 StateStart(通过 clnt.backend.StateChanged 设置状态，被记录到events.log文件)等的区别
	// ContainerEventType is the event type that containers generate
	ContainerEventType = "container" //LogImageEventWithAttributes 中记录
	// DaemonEventType is the event type that daemon generate 中记录
	DaemonEventType = "daemon" //LogDaemonEventWithAttributes 中记录
	// ImageEventType is the event type that images generate
	ImageEventType = "image"  //LogImageEventWithAttributes 中记录
	// NetworkEventType is the event type that networks generate
	NetworkEventType = "network" //LogNetworkEventWithAttributes 记录
	// PluginEventType is the event type that plugins generate
	PluginEventType = "plugin"  //LogPluginEventWithAttributes 中记录
	// VolumeEventType is the event type that volumes generate
	VolumeEventType = "volume"  //LogVolumeEvent记录
)

// Actor describes something that generates events,
// like a container, or a network, or a volume.
// It has a defined name and a set or attributes.
// The container attributes are its labels, other actors
// can generate these attributes from other properties.
type Actor struct {
	ID         string
	Attributes map[string]string
}

// Message represents the information an event contains
type Message struct {
	// Deprecated information from JSONMessage.
	// With data only in container events.
	Status string `json:"status,omitempty"`
	ID     string `json:"id,omitempty"`
	From   string `json:"from,omitempty"`

	Type   string
	Action string
	Actor  Actor

	Time     int64 `json:"time,omitempty"`
	TimeNano int64 `json:"timeNano,omitempty"`
}
