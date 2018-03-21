package libcontainerd
import (
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"errors"

	"github.com/Sirupsen/logrus"
	containerd "github.com/docker/containerd/api/grpc/types"
	"github.com/docker/docker/pkg/mount"
	"github.com/docker/docker/pkg/system"
)

const (
	maxLxcfsConnectionRetryCount      = 3
	lxcfsHealthCheckTimeout     	  = 3 * time.Second
	lxcfsShutdownTimeout   		  = 3 * time.Second
	lxcfsBinary            		  = "docker-lxcfs"
	lxcfsPidFilename      		  = "docker-lxcfs.pid"
	lxcfsSockFilename    		  = "docker-lxcfs.sock"
	lxcfsLogDir                       = "/var/log/lxcfs/lxcfs.log"
	lxcfsStateDir            	  = "lxcfs"
	//eventLxcfsTimestampFilename       = "event-lxcfs.ts"

	lxcfsHealthString                 = "lxcfs docker health protocol"
	lxcfsHealthAckString               = "lxcfs docker health protocol ack"
	lxcfsMaxBufLen                    = 500
	lxcfsReadWriteTimeout             = 100 * time.Millisecond
)

type LxcfsRemote struct {
	sync.RWMutex
	apiClient           containerd.APIClient
	lxcfsPid            int
	stateDir            string
	connMutex 	    sync.Mutex
	startByDocker       bool

	debugLog             bool
	rpcAddr              string
	allowOther 	     bool
	offMultithread	     bool
	logPath 	     string
	mountPath            string

	closeManually        bool
	conn                 net.Conn
	clients              []*client
	daemonWaitCh         chan struct{}
	liveRestore          bool
	oomScore             int
}

// New creates a fresh instance of libcontainerd remote.
func NewLxcfs(stateDir string, options ...RemoteOption) (_ *LxcfsRemote, err error) {
	defer func() {
		if err != nil {
			err = fmt.Errorf("Failed to connect to lxcfs. Please make sure containerd is installed in your PATH or you have specified the correct address. Got error: %v", err)
		}
	}()
	r := &LxcfsRemote{
		stateDir:   stateDir,
		lxcfsPid:   -1,
		//eventTsPath: filepath.Join(stateDir, eventTimestampFilename),
	}

	//getPlatformLxcfsRemoteOptions
	for _, option := range options {
		if err := option.Apply(r); err != nil {
			return nil, fmt.Errorf("option.Apply: %v", err)
		}
	}

	if err := system.MkdirAll(stateDir, 0700); err != nil {
		return nil, fmt.Errorf("system.MkdirAll: %v", err)
	}

	if r.rpcAddr == "" {
		r.rpcAddr = filepath.Join(stateDir, lxcfsSockFilename)
	}

	if r.logPath == "" {
		r.logPath = lxcfsLogDir
	}

	fmt.Printf("yang test ... rpcaddr:%s, logpath:%s, debugLog:%d, allowOther:%d, offMultithread:%d\n", r.rpcAddr, r.logPath, r.debugLog, r.allowOther, r.offMultithread)
	if err := r.runLxcfsDaemon(); err != nil {
		return nil, fmt.Errorf("runLxcfsDaemon failed: %v", err)
	}


	time.Sleep(500 * time.Millisecond)
	err = r.LxcfsRemoteConnect(r.rpcAddr)
	if err != nil {
		return nil, fmt.Errorf("LxcfsRemoteConnect failed: %v", err)
	}

	go r.handleLxcfsConnectionChange()

	return r, nil
}

func (r *LxcfsRemote) LxcfsRemoteConnect(addr string) error {
	conn, err := net.Dial("unix", r.rpcAddr)
	if err != nil {
		return fmt.Errorf("error connecting to lxcfs: %v", err)
	}

	r.conn = conn
	return nil
}

func GetLxcfsDir() string {
	return lxcfsStateDir
}

func (r *LxcfsRemote) RemoveUmountLxcfs(target string) {
	err := mount.ForceUnmount(target)
	if err != nil {
		logrus.Errorf("RemoveUmountLxcfs failed %v", err.Error())
		return
	}

	logrus.Debugf("umount %s successful\n", r.mountPath)
}


func (r *LxcfsRemote) healthCheck() error {
	buf, err := r.WriteRead([]byte(lxcfsHealthString))
	if err != nil {
		logrus.Errorf("Error to write read message because of %v", err.Error())
		return nil
	}

	if strings.Compare(string(buf), lxcfsHealthAckString) == 0 {
		logrus.Warnf("Error to recv message because of %v", buf)
	}

	return nil
}

func (r *LxcfsRemote) WriteRead(writedata []byte)( []byte,  error) {
	conn := r.conn
	if conn == nil {
		err1 := errors.New("conn = nil")
		logrus.Errorf("Error to recv message because of %v", err1.Error())
		return nil, err1
	}

	conn.SetWriteDeadline(time.Now().Add(lxcfsReadWriteTimeout))
	_, err := conn.Write(writedata)
	if err != nil {
		logrus.Debugf("Error to send message because of %v", err.Error())
		return nil, err
	}

	buf := make([]byte, lxcfsMaxBufLen)
	conn.SetReadDeadline(time.Now().Add(lxcfsReadWriteTimeout))
	_, err = conn.Read(buf)
	if err != nil {
		logrus.Errorf("Error to recv message because of %v", err.Error())
		return nil, err
	}

	return buf, nil
}


//reloadLiveRestore
func (r *LxcfsRemote) UpdateOptions(options ...RemoteOption) error {
	for _, option := range options {
		if err := option.Apply(r); err != nil {
			return err
		}
	}
	return nil
}

func (r *LxcfsRemote) Client(b Backend) (Client, error) {
	return nil, nil
}

func (r *LxcfsRemote) handleLxcfsConnectionChange() {
	var transientFailureCount = 0

	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		<-ticker.C

		r.connMutex.Lock()
		err := r.healthCheck()
		r.connMutex.Unlock()
		if err == nil {
			continue
		}

		logrus.Debugf("lxcfs: containerd health check returned error: %v", err)

		if r.lxcfsPid != -1 {
			transientFailureCount++
			if transientFailureCount >= maxLxcfsConnectionRetryCount {
				fmt.Printf("yang test ...2222...\n");
				transientFailureCount = 0
				if system.IsProcessAlive(r.lxcfsPid) {
					 system.KillProcess(r.lxcfsPid)
				}

				if r.startByDocker == true {
				    <-r.daemonWaitCh
				}

				r.connMutex.Lock()
				if r.conn != nil {
					r.conn.Close()
					r.conn = nil
				}
				r.connMutex.Unlock()

				if err = r.runLxcfsDaemon(); err != nil {
					logrus.Errorf("lxcfs: error restarting containerd: %v", err)
					continue
				}

				time.Sleep(500 * time.Millisecond)

				r.connMutex.Lock()
				err = r.LxcfsRemoteConnect(r.rpcAddr)
				r.connMutex.Unlock()
				if err != nil {
					logrus.Errorf("lxcfs: LxcfsRemoteConnect error: %v", err)
				}
				continue
			}
		}
	}
}

func (r *LxcfsRemote) Cleanup() {
	logrus.Warnf("lxcfs: Cleanup %d\n", r.lxcfsPid)
	if r.lxcfsPid == -1 {
		return
	}

	r.RemoveUmountLxcfs(r.mountPath)
	r.closeManually = true

	r.connMutex.Lock()
	if r.conn != nil {
		r.conn.Close()
	}
	r.conn = nil
	r.connMutex.Unlock()
	// Ask the daemon to quit
	syscall.Kill(r.lxcfsPid, syscall.SIGTERM)

	// Wait up to 3secs for it to stop
	for i := time.Duration(0); i < lxcfsShutdownTimeout; i += time.Second {
		if !system.IsProcessAlive(r.lxcfsPid) {
			break
		}
		time.Sleep(time.Second)
	}

	if system.IsProcessAlive(r.lxcfsPid) {
		logrus.Warnf("lxcfs: lxcfs (%d) didn't stop within 15 secs, killing it\n", r.lxcfsPid)
		syscall.Kill(r.lxcfsPid, syscall.SIGKILL)
	}

	// cleanup some files
	os.Remove(filepath.Join(r.stateDir, lxcfsPidFilename))
	os.Remove(filepath.Join(r.stateDir, lxcfsSockFilename))
	//mount.Unmount(r.mountPath);

	logrus.Debug("lxcfs: rm %v %v\n", filepath.Join(r.stateDir, "/lxcfs/" + lxcfsPidFilename), filepath.Join(r.stateDir, "/lxcfs/" + lxcfsSockFilename))
}

func (r *LxcfsRemote) runLxcfsDaemon() error {
	pidFilename := filepath.Join(r.stateDir, lxcfsPidFilename)
	f, err := os.OpenFile(pidFilename, os.O_RDWR|os.O_CREATE, 0600)
	if err != nil {
		return err
	}
	defer f.Close()

	// File exist, check if the daemon is alive
	b := make([]byte, 8)
	n, err := f.Read(b)
	if err != nil && err != io.EOF {
		return err
	}

	if n > 0 {
		pid, err := strconv.ParseUint(string(b[:n]), 10, 64)
		if err != nil {
			return err
		}
		if system.IsProcessAlive(int(pid)) {
			logrus.Infof("lxcfs: previous instance of lxcfs still alive (%d)", pid)
			r.lxcfsPid = int(pid)
			r.startByDocker = false
			return nil
		}
	}

	r.RemoveUmountLxcfs(r.mountPath)
	// rewind the file
	_, err = f.Seek(0, os.SEEK_SET)
	if err != nil {
		return err
	}

	// Truncate it
	err = f.Truncate(0)
	if err != nil {
		return err
	}

	// /usr/local/bin/lxcfs -s -f -o allow_other /usr/local/var/lib/lxcfs/  2>&1  | tee /var/log/lxcfs/lxcfs.log
	// Start a new instance
	args := []string{
		"-f",
	}

	if r.offMultithread {
		args = append(args, "-s")
	}

	if r.allowOther {
		args = append(args, "-o")
		args = append(args, "allow_other")
	}

	if r.debugLog {
		args = append(args, "--debug")
	}

	args = append(args, "-p")
	args = append(args, filepath.Join(r.stateDir, lxcfsPidFilename))

	if r.logPath {
		args = append(args, "-l")
		args = append(args, r.logPath)
	}

	args = append(args, "-p")
	args = append(args, filepath.Join(r.stateDir, lxcfsPidFilename))

	args = append(args, "-L")
	args = append(args, filepath.Join(r.stateDir, lxcfsSockFilename))

	args = append(args, "/usr/local/var/lib/lxcfs/")

	logrus.Debugf("lxcfs: runLxcfsDaemon Args: %s", args)

	cmd := exec.Command(lxcfsBinary, args...)
	// redirect containerd logs to docker logs
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.SysProcAttr = setSysProcAttr(true)
	cmd.Env = nil

	if err := cmd.Start(); err != nil {
		logrus.Error("lxcfs: new lxcfs process, pid: %d", cmd.Process.Pid)
		return err
	}
	logrus.Infof("lxcfs: new lxcfs process, pid: %d", cmd.Process.Pid)
	if err := setOOMScore(cmd.Process.Pid, r.oomScore); err != nil {
		system.KillProcess(cmd.Process.Pid)
		logrus.Error("lxcfs: setOOMScore err")
		return err
	}
	if _, err := f.WriteString(fmt.Sprintf("%d", cmd.Process.Pid)); err != nil {
		system.KillProcess(cmd.Process.Pid)
		logrus.Error("lxcfs: setOOMScore err")
		return err
	}

	r.daemonWaitCh = make(chan struct{})
	go func() {
		cmd.Wait()
		logrus.Error("lxcfs: lxcfs exist")
		close(r.daemonWaitCh)
	}() // Reap our child when needed

	r.lxcfsPid = cmd.Process.Pid
	r.startByDocker = true
	return nil
}

//getPlatformLxcfsRemoteOptions
func LxcfsWithDebugLog(debug bool) RemoteOption {
	return lxcfsdebugLog(debug)
}
type lxcfsdebugLog bool
func (d lxcfsdebugLog) Apply(r Remote) error {
	if LxcfsRemote, ok := r.(*LxcfsRemote); ok {
		LxcfsRemote.debugLog = bool(d)
		return nil
	}
	return fmt.Errorf("LxcfsWithDebugLog option not supported for this remote")
}

// WithRemoteAddr sets the external lxcfs socket to connect to.
func LxcfsWithRemoteAddr(addr string) RemoteOption {
	return lxcfsRpcAddr(addr)
}
type lxcfsRpcAddr string
func (a lxcfsRpcAddr) Apply(r Remote) error {
	if LxcfsRemote, ok := r.(*LxcfsRemote); ok {
		LxcfsRemote.rpcAddr = string(a)
		return nil
	}
	return fmt.Errorf("LxcfsWithRemoteAddr option not supported for this remote")
}

func LxcfsWithLogPath(path string) RemoteOption {
	return lxcfsLogPath(path)
}
type lxcfsLogPath string
func (a lxcfsLogPath) Apply(r Remote) error {
	if LxcfsRemote, ok := r.(*LxcfsRemote); ok {
		LxcfsRemote.logPath = string(a)
		return nil
	}
	return fmt.Errorf("LxcfsWithLogPath option not supported for this remote")
}

func LxcfsWithOffMultithread(offmulti bool) RemoteOption {
	return lxcfsOffMultithread(offmulti)
}
type lxcfsOffMultithread bool
func (a lxcfsOffMultithread) Apply(r Remote) error {
	if LxcfsRemote, ok := r.(*LxcfsRemote); ok {
		LxcfsRemote.offMultithread = bool(a)
		return nil
	}
	return fmt.Errorf("LxcfsWithOffMultithread option not supported for this remote")
}

func LxcfsWithAllowOther(allowother bool) RemoteOption {
	return lxcfsAllowOther(allowother)
}
type lxcfsAllowOther bool
func (a lxcfsAllowOther) Apply(r Remote) error {
	if LxcfsRemote, ok := r.(*LxcfsRemote); ok {
		LxcfsRemote.allowOther = bool(a)
		return nil
	}
	return fmt.Errorf("LxcfsWithAllowOther option not supported for this remote")
}

func LxcfsWithOOMScore(oom int) RemoteOption {
	return LxcfsOOMScore(oom)
}
type LxcfsOOMScore int
func (a LxcfsOOMScore) Apply(r Remote) error {
	if LxcfsRemote, ok := r.(*LxcfsRemote); ok {
		LxcfsRemote.oomScore = int(a)
		return nil
	}
	return fmt.Errorf("LxcfsWithOOMScore option not supported for this remote")
}

func LxcfsWithMountPath(path string) RemoteOption {
	return lxcfsMountPath(path)
}
type lxcfsMountPath string
func (a lxcfsMountPath) Apply(r Remote) error {
	if LxcfsRemote, ok := r.(*LxcfsRemote); ok {
		LxcfsRemote.mountPath = string(a)
		return nil
	}
	return fmt.Errorf("LxcfsWithMountPath option not supported for this remote")
}





