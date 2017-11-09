package main

import (
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"syscall"

	"github.com/docker/containerd/osutils"
	"github.com/docker/docker/pkg/term"
)

func writeMessage(f *os.File, level string, err error) {
	fmt.Fprintf(f, `{"level": "%s","msg": "%s"}`, level, err)
}

type controlMessage struct {
	Type   int
	Width  int
	Height int
}

// containerd-shim is a small shim that sits in front of a runtime implementation
// that allows it to be repartented to init and handle reattach from the caller.
//
// the cwd of the shim should be the path to the state directory where the shim
// can locate fifos and other information.
// Arg0: id of the container
// Arg1: bundle path
// Arg2: runtime binary
func main() {
	flag.Parse()
	//getcwd()获取工作目录的绝对路径
	cwd, err := os.Getwd()
	if err != nil {
		panic(err)
	}

	//创建shim-log.json文件,格式类似{"level": "%s","msg": "%s"}
	f, err := os.OpenFile(filepath.Join(cwd, "shim-log.json"), os.O_CREATE|os.O_WRONLY|os.O_APPEND|os.O_SYNC, 0666)
	if err != nil {
		panic(err)
	}

	if err := start(f); err != nil {
		// this means that the runtime failed starting the container and will have the
		// proper error messages in the runtime log so we should to treat this as a
		// shim failure because the sim executed properly
		if err == errRuntime {
			f.Close()
			return
		}
		// log the error instead of writing to stderr because the shim will have
		// /dev/null as it's stdio because it is supposed to be reparented to system
		// init and will not have anyone to read from it
		writeMessage(f, "error", err)
		f.Close()
		os.Exit(1)
	}
}

func start(log *os.File) error {
	// start handling signals as soon as possible so that things are properly reaped
	// or if runtime exits before we hit the handler
	signals := make(chan os.Signal, 2048)
	signal.Notify(signals)
	// set the shim as the subreaper for all orphaned processes created by the container
	//保证容器中的所有孤儿进程被docker-containerd-shim接管，孤儿进程的父进程变为docker-containerd-shim
	//参考https://yq.aliyun.com/articles/61894
	//ps xf -o pid,ppid,stat,args 查看进程树
	if err := osutils.SetSubreaper(1); err != nil {
		return err
	}

	//打开exit pipe和control pipe     docker-containerd 进程中会创建者两个pipe
	// open the exit pipe
	f, err := os.OpenFile("exit", syscall.O_WRONLY, 0)
	if err != nil {
		return err
	}
	defer f.Close()
	control, err := os.OpenFile("control", syscall.O_RDWR, 0)
	if err != nil {
		return err
	}
	defer control.Close()

	// //创建Process，参数：arg0，arg1，arg2
	//docker-containerd-shim bd20340c7d49585b7aa697b2ffb2546d1b76c6695fc33573510cc5ed13737b0d /var/run/docker/libcontainerd/bd20340c7d49585b7aa697b2ffb2546d1b76c6695fc33573510cc5ed13737b0d docker-runc
	p, err := newProcess(flag.Arg(0), flag.Arg(1), flag.Arg(2))
	if err != nil {
		return err
	}
	defer func() {
		if err := p.Close(); err != nil {
			writeMessage(log, "warn", err)
		}
	}()
	if err := p.create(); err != nil {
		p.delete()
		return err
	}

	//创建一个goroutine，从control pipe中不断读取controlMessage
	msgC := make(chan controlMessage, 32)
	go func() {
		for {
			var m controlMessage
			//从control pipe中不断读取controlMessage
			if _, err := fmt.Fscanf(control, "%d %d %d\n", &m.Type, &m.Width, &m.Height); err != nil {
				continue
			}
			msgC <- m
		}
	}()
	if runtime.GOOS == "solaris" {
		return nil
	}
	var exitShim bool
	//无限for循环，对来自signal的信号和controlMessage进行处理
	for {
		select {
		case s := <-signals:
			switch s {
			//当从signal中获得的信号为SIGCHLD时，当退出的进程为runtime时，退出shim
			case syscall.SIGCHLD:
				exits, _ := osutils.Reap(false)
				for _, e := range exits {
					// check to see if runtime is one of the processes that has exited
					if e.Pid == p.pid() {
						exitShim = true
						writeInt("exitStatus", e.Status)
					}
				}
			}
			// runtime has exited so the shim can also exit
			if exitShim {
				// kill all processes in the container incase it was not running in
				// its own PID namespace
				p.killAll()
				// wait for all the processes and IO to finish
				p.Wait()
				// delete the container from the runtime
				p.delete()
				// the close of the exit fifo will happen when the shim exits
				return nil
			}

		//对来此control pipe的controlMessage进行处理，当msg的Type为0时，关闭stdin，当Type为1时，且p.console不为nil，则调整tty的窗口大小
		case msg := <-msgC:
			switch msg.Type {
			case 0:
				// close stdin
				if p.stdinCloser != nil {
					p.stdinCloser.Close()
				}
			case 1:
				if p.console == nil {
					continue
				}
				ws := term.Winsize{
					Width:  uint16(msg.Width),
					Height: uint16(msg.Height),
				}
				term.SetWinsize(p.console.Fd(), &ws)
			}
		}
	}
	return nil
}

func writeInt(path string, i int) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = fmt.Fprintf(f, "%d", i)
	return err
}
