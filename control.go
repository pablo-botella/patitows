package patitows

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"time"
)

// The control channel is a local socket named after the instance id. Being
// able to listen on it is what makes an instance id unique on the system;
// it carries one JSON request and one JSON response per connection.

// Control commands.
const (
	CmdInfo       = "info"
	CmdReloadBlob = "reloadblob"
	CmdStop       = "stop"
)

// Request is a control command sent to an instance.
type Request struct {
	Cmd    string `json:"cmd"`
	BlobID string `json:"blobid,omitempty"`
}

// Response is an instance's answer to a Request.
type Response struct {
	OK       bool   `json:"ok"`
	Error    string `json:"error,omitempty"`
	Instance string `json:"instance,omitempty"`
	Config   string `json:"config,omitempty"`
	BlobFile string `json:"blobfile,omitempty"`
}

// ErrControlUnsupported is returned where the platform has no control
// channel yet.
var ErrControlUnsupported = errors.New("control channel not supported on this platform")

// ListenControl claims the instance id and starts answering control
// requests for s. It fails if another instance already holds the id.
func ListenControl(id string, s *Server) (net.Listener, error) {
	ln, err := listenControl(id)
	if err != nil {
		return nil, err
	}
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go handleControl(conn, s)
		}
	}()
	return ln, nil
}

func handleControl(conn net.Conn, s *Server) {
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(ShutdownTimeout + 10*time.Second))
	reply := func(r Response) { json.NewEncoder(conn).Encode(r) }

	if err := checkPeer(conn); err != nil {
		reply(Response{Error: err.Error()})
		return
	}
	var req Request
	if err := json.NewDecoder(bufio.NewReader(conn)).Decode(&req); err != nil {
		reply(Response{Error: "bad request: " + err.Error()})
		return
	}
	switch req.Cmd {
	case CmdInfo:
		reply(Response{OK: true, Instance: s.cfg.ID, Config: s.cfg.Path})
	case CmdReloadBlob:
		path, err := s.ReloadBlob(req.BlobID)
		if err != nil {
			log.Printf("control: reload blob %q: %v", req.BlobID, err)
			reply(Response{Error: err.Error()})
			return
		}
		reply(Response{OK: true, BlobFile: path})
	case CmdStop:
		log.Printf("control: stop requested")
		err := s.Stop()
		if err != nil {
			reply(Response{Error: "stopped, but not every request finished: " + err.Error()})
			return
		}
		reply(Response{OK: true})
	default:
		reply(Response{Error: fmt.Sprintf("unknown command %q", req.Cmd)})
	}
}

// Send connects to the instance id, sends req and returns its response.
func Send(id string, req Request) (*Response, error) {
	conn, err := dialControl(id)
	if err != nil {
		return nil, fmt.Errorf("instance %q not reachable: %w", id, err)
	}
	defer conn.Close()
	if err := json.NewEncoder(conn).Encode(req); err != nil {
		return nil, err
	}
	var resp Response
	if err := json.NewDecoder(conn).Decode(&resp); err != nil {
		return nil, fmt.Errorf("instance %q: %w", id, err)
	}
	return &resp, nil
}
