package cni_shim

import (
	"context"
	"fmt"
	"log"
	"net"
	"os"
	"sync"
	"syscall"
	"time"

	zdsapi "github.com/isovalent/ztunnel-shim/cni-shim/ztunnel/proto"
	"google.golang.org/protobuf/proto"
)

// The Shim is a replacement for the CNIPod istio component.
// ZTunnel in shared mode must connect to a unix socket for workload information.
//
// Events on this Unix socket then instruct it to create a proxy in the network
// namespace of the corresponding event.
//
// The Shim implements the unix socket and protocol the ZTunnel expects.
type Shim struct {
	l          net.Listener
	netnsPaths []string
}

func (s *Shim) readAck(buf []byte) error {
	var ack zdsapi.Ack
	if err := proto.Unmarshal(buf, &ack); err != nil {
		return fmt.Errorf("failed to unmarshal ack message: %v", err)
	}
	log.Printf("received ack:")
	return nil
}

func (s *Shim) readZdsHello(buf []byte) error {
	var hello zdsapi.ZdsHello
	if err := proto.Unmarshal(buf, &hello); err != nil {
		return fmt.Errorf("failed to unmarshal hello message: %v", err)
	}
	if hello.Version != 1 {
		return fmt.Errorf("unsupported version: %d", hello.Version)
	}
	log.Printf("received hello: %v", hello.Version)
	return nil
}

func (s *Shim) writeSnapshot(conn net.Conn) error {
	workloadReq := zdsapi.WorkloadRequest{
		Payload: &zdsapi.WorkloadRequest_SnapshotSent{
			SnapshotSent: &zdsapi.SnapshotSent{},
		},
	}

	buf, err := proto.Marshal(&workloadReq)
	if err != nil {
		return fmt.Errorf("failed to marshal snapshot: %v", err)
	}

	var n int
	if n, err = conn.Write(buf); err != nil {
		return fmt.Errorf("failed to write snapshot: %v", err)
	}
	log.Printf("wrote %d bytes to connection", n)

	log.Printf("wrote snapshot")
	return nil
}

func (s *Shim) writeAddWorkload(conn net.Conn, netnsPath string) error {
	// get nanosecond timestamp
	timestamp := time.Now().UnixNano()
	id := fmt.Sprintf("workload-%d", timestamp)

	workloadReq := zdsapi.WorkloadRequest{
		Payload: &zdsapi.WorkloadRequest_Add{
			Add: &zdsapi.AddWorkload{
				Uid: id,
				WorkloadInfo: &zdsapi.WorkloadInfo{
					Name:           id,
					Namespace:      id,
					ServiceAccount: id,
				},
			},
		},
	}

	buf, err := proto.Marshal(&workloadReq)
	if err != nil {
		return fmt.Errorf("failed to marshal add workload: %v", err)
	}

	unixConn, ok := conn.(*net.UnixConn)
	if !ok {
		return fmt.Errorf("failed to cast conn to UnixConn")
	}

	file, err := os.Open(netnsPath)
	if err != nil {
		return fmt.Errorf("failed to open netns file: %v", err)
	}
	defer file.Close()

	fd := file.Fd()

	rights := syscall.UnixRights(int(fd))

	n, _, err := unixConn.WriteMsgUnix(buf, rights, nil)
	if err != nil {
		return fmt.Errorf("failed to write add workload: %v", err)
	}
	log.Printf("wrote %d bytes to connection", n)

	log.Printf("wrote add workload")

	return nil
}

// listen will begin listening for incoming connections on the unix socket and
// implements the zds protocol ztunnel expects.
//
// its designed to be ran as a go routine.
//
// it will add a delta to the provided wait group and also monitor the passed
// in context.
//
// when the context is canceled it will decrement the wait group once its
// cancelation is complete. therefore, callers can wait on the wait group
// to determine if its safe to continue its own cancelation.
func (s *Shim) listen(ctx context.Context, wg *sync.WaitGroup) {
	defer wg.Done()

	// close listener on ctx cancelation
	go func() {
		<-ctx.Done()
		log.Printf("cni_shim.Shim's listener closed by ctx cancelation")
		s.l.Close()
	}()

	buf := make([]byte, 1024)
	for {
		conn, err := s.l.Accept()
		if opErr, ok := err.(*net.OpError); ok {
			if opErr.Temporary() {
				// Log and retry for temporary errors
				log.Printf("encountered temporary accept error: %v\n", err)
				continue
			}
			log.Printf("failed to accept connection: %v.", err)
			return
		}
		log.Printf("accepted connection from %s", conn.RemoteAddr())

		n, err := conn.Read(buf)
		if err != nil {
			log.Printf("failed to read from connection: %v. Closing connection", err)
			goto cleanup
		}
		log.Printf("read %d bytes from connection", n)

		// first message should always be a ZdsHello
		if err = s.readZdsHello(buf[:n]); err != nil {
			log.Printf("failed to handle hello message: %v. Closing connection", err)
			goto cleanup
		}

		// send a snapshot for now, we don't care about reconciliation of stale
		// workloads for the PoC.
		if err = s.writeSnapshot(conn); err != nil {
			log.Printf("failed to handle snapshot message: %v. Closing connection", err)
			goto cleanup
		}

		// wait for ack
		n, err = conn.Read(buf)
		if err = s.readAck(buf[:n]); err != nil {
			log.Printf("failed to handle ack message: %v. Closing connection", err)
			goto cleanup
		}

		for _, p := range s.netnsPaths {
			log.Printf("path: %v", p)

			// send a workload add for each netns path
			if err = s.writeAddWorkload(conn, p); err != nil {
				log.Printf("failed to handle add workload message: %v. Closing connection", err)
				goto cleanup
			}

			// wait for ack
			n, err = conn.Read(buf)
			if err = s.readAck(buf[:n]); err != nil {
				log.Printf("failed to handle ack message: %v. Closing connection", err)
				goto cleanup
			}
		}

		continue
	cleanup:
		conn.Close()
	}
}

func NewShim(ctx context.Context, wg *sync.WaitGroup, netnsPaths []string) (*Shim, error) {

	shim := &Shim{
		netnsPaths: netnsPaths,
	}

	_, err := os.Stat("/var/run/ztunnel/ztunnel.sock")
	if os.IsNotExist(err) {
		// create the directory
		err = os.MkdirAll("/var/run/ztunnel", 0755)
		if err != nil {
			return nil, err
		}
	}

	err = os.RemoveAll("/var/run/ztunnel/ztunnel.sock")
	if err != nil {
		return nil, err
	}

	shim.l, err = net.Listen("unixpacket", "/var/run/ztunnel/ztunnel.sock")
	if err != nil {
		return nil, err
	}

	wg.Add(1)
	go shim.listen(ctx, wg)

	return shim, nil
}
