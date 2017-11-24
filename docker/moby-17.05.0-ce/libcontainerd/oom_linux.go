package libcontainerd

import (
	"fmt"
	"os"
	"strconv"

	"github.com/Sirupsen/logrus"
	"github.com/opencontainers/runc/libcontainer/system"
)

//设置进程的oom_score_adj
func setOOMScore(pid, score int) error {
	//设置进程的oom_score_adj
	oomScoreAdjPath := fmt.Sprintf("/proc/%d/oom_score_adj", pid)
	f, err := os.OpenFile(oomScoreAdjPath, os.O_WRONLY, 0)
	if err != nil {
		return err
	}
	stringScore := strconv.Itoa(score)
	_, err = f.WriteString(stringScore)
	f.Close()
	if os.IsPermission(err) {
		// Setting oom_score_adj does not work in an
		// unprivileged container. Ignore the error, but log
		// it if we appear not to be in that situation.
		if !system.RunningInUserNS() {
			logrus.Debugf("Permission denied writing %q to %s", stringScore, oomScoreAdjPath)
		}
		return nil
	}
	return err
}
