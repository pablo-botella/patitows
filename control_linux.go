package patitows

import (
	"errors"
	"fmt"
	"net"
	"os"
	"syscall"
)

// On Linux the control channel is an abstract Unix socket: a system-wide
// name with no file behind it, released by the kernel when the process
// dies, however it dies.
func controlAddr(id string) string { return "@patitows/" + id }

func listenControl(id string) (net.Listener, error) {
	ln, err := net.Listen("unix", controlAddr(id))
	if err != nil {
		if errors.Is(err, syscall.EADDRINUSE) {
			return nil, fmt.Errorf("instance %q is already running", id)
		}
		return nil, err
	}
	return ln, nil
}

func dialControl(id string) (net.Conn, error) {
	return net.Dial("unix", controlAddr(id))
}

// checkPeer accepts only the user running the instance, or root.
func checkPeer(conn net.Conn) error {
	uc, ok := conn.(*net.UnixConn)
	if !ok {
		return errors.New("unexpected connection type")
	}
	raw, err := uc.SyscallConn()
	if err != nil {
		return err
	}
	var cred *syscall.Ucred
	var credErr error
	if err := raw.Control(func(fd uintptr) {
		cred, credErr = syscall.GetsockoptUcred(int(fd), syscall.SOL_SOCKET, syscall.SO_PEERCRED)
	}); err != nil {
		return err
	}
	if credErr != nil {
		return credErr
	}
	if cred.Uid != 0 && int(cred.Uid) != os.Getuid() {
		return fmt.Errorf("permission denied for uid %d", cred.Uid)
	}
	return nil
}
