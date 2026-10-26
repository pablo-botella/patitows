//go:build !linux

package patitows

import "net"

func listenControl(id string) (net.Listener, error) { return nil, ErrControlUnsupported }

func dialControl(id string) (net.Conn, error) { return nil, ErrControlUnsupported }

func checkPeer(conn net.Conn) error { return ErrControlUnsupported }
