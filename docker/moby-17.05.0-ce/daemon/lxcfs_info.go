package daemon

import (
    "errors"
	"encoding/json"
	
    "github.com/Sirupsen/logrus"
    "github.com/docker/docker/api/types"
)

const  lxcfsGetStatisticString   = "docker get lxcfs statistics"
/*
 "hostname": "16be9a0ea5b6",
"namespaces": [
      {
        "type": "mount"
      },
      {
        "type": "network"
      },
      {
        "type": "uts"
      },
      {
        "type": "pid"
      },
      {
        "type": "ipc"
      }
    ],
    "devices": [
      {
        "path": "/dev/autofs",
        "type": "c",
        "major": 10,
        "minor": 235,
        "fileMode": 8576,
        "uid": 0,
        "gid": 0
      },
      {
        "path": "/dev/bsg/0:0:0:0",
        "type": "c",
        "major": 251,
        "minor": 0,
        "fileMode": 8576,
        "uid": 0,
        "gid": 0
      },   
*/

// SystemInfo returns information about the host server the daemon is running on.
func (daemon *Daemon) LxcfsInfo() (*types.LxcfsInfo, error){
	remote := daemon.lxcfsRemote
	if remote == nil {
		err1 := errors.New("daemon.lxcfsRemote = nil")
		logrus.Errorf("Error LxcfsInfo because of %v", err1.Error())
		return nil, err1
	}

	buf, err := remote.WriteRead([]byte(lxcfsGetStatisticString)) 
	if err != nil {
		logrus.Errorf("Error to write read message because of %v", err.Error())
		return nil, err
	}

	info := &types.LxcfsInfo{}
	err = json.Unmarshal([]byte(buf), &info)
	if err != nil {
		logrus.Errorf("Error LxcfsInfo because of %v", err.Error())
		return nil, err
	}
	return info, nil
}

