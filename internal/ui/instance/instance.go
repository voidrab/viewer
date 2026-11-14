// Package instance makes a program run once per user: the first instance
// listens on a unix socket, and later launches hand their request to it
// instead of starting another.
package instance

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"
)

// Request asks the running instance to open files. Token is the activation
// token the launcher gave the new process, which lets the running instance
// bring its window forward.
type Request struct {
	Paths []string `json:"paths"`
	Token string   `json:"token,omitempty"`
}

// DefaultPath returns where the socket of the program called name lives: in
// the user's runtime directory, or the temporary directory without one.
func DefaultPath(name string) string {
	if dir := os.Getenv("XDG_RUNTIME_DIR"); dir != "" {
		return filepath.Join(dir, name+".sock")
	}
	return filepath.Join(os.TempDir(), fmt.Sprintf("%s-%d.sock", name, os.Getuid()))
}

// ioTimeout bounds how long either end waits on the other.
const ioTimeout = 5 * time.Second

// Server is the running instance's end: it receives the requests of later
// launches.
type Server struct {
	ln   net.Listener
	reqs chan Request
	wg   sync.WaitGroup
}

// Claim makes this process the instance listening at path, or forwards req to
// the instance that already does. Relative paths in req are made absolute
// first, since the instance may have another working directory. It returns the
// server, or forwarded true and no server once the other instance has the
// request.
func Claim(path string, req Request) (srv *Server, forwarded bool, err error) {
	abs := make([]string, len(req.Paths))
	for i, p := range req.Paths {
		if abs[i], err = filepath.Abs(p); err != nil {
			return nil, false, err
		}
	}
	req.Paths = abs
	ln, err := net.Listen("unix", path)
	if errors.Is(err, syscall.EADDRINUSE) {
		// A live instance holds the socket, or an earlier one died without
		// removing it.
		if send(path, req) == nil {
			return nil, true, nil
		}
		if err = os.Remove(path); err != nil {
			return nil, false, err
		}
		ln, err = net.Listen("unix", path)
	}
	if err != nil {
		return nil, false, err
	}
	s := &Server{ln: ln, reqs: make(chan Request, 16)}
	s.wg.Add(1)
	go s.accept()
	return s, false, nil
}

// Requests delivers the requests of later launches.
func (s *Server) Requests() <-chan Request { return s.reqs }

// Close stops listening and removes the socket.
func (s *Server) Close() error {
	err := s.ln.Close()
	s.wg.Wait()
	return err
}

func (s *Server) accept() {
	defer s.wg.Done()
	for {
		conn, err := s.ln.Accept()
		if err != nil {
			return
		}
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			s.serve(conn)
		}()
	}
}

// serve reads one request from conn, queues it and acknowledges it.
func (s *Server) serve(conn net.Conn) {
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(ioTimeout))
	var req Request
	if err := json.NewDecoder(bufio.NewReader(conn)).Decode(&req); err != nil {
		return
	}
	select {
	case s.reqs <- req:
		conn.Write([]byte("ok\n"))
	case <-time.After(ioTimeout):
	}
}

// send gives req to the instance at path and waits for its acknowledgement.
func send(path string, req Request) error {
	conn, err := net.DialTimeout("unix", path, ioTimeout)
	if err != nil {
		return err
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(ioTimeout))
	if err := json.NewEncoder(conn).Encode(req); err != nil {
		return err
	}
	ack, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil {
		return err
	}
	if ack != "ok\n" {
		return fmt.Errorf("instance: unexpected reply %q", ack)
	}
	return nil
}
