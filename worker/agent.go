package worker

import (
	"io"
	"net"
	"strings"
	"sync"
	"bufio"
)

// The agent of job server.
type agent struct {
	sync.Mutex
	conn      net.Conn
	rw		*bufio.ReadWriter
	worker    *Worker
	in        chan []byte
	net, addr string
}

// Create the agent of job server.
func newAgent(net, addr string, worker *Worker) (a *agent, err error) {
	a = &agent{
		net:    net,
		addr:   addr,
		worker: worker,
		in:     make(chan []byte, queueSize),
	}
	return
}

func (a *agent) Connect() (err error) {
	a.Lock()
	defer a.Unlock()
	a.conn, err = net.Dial(a.net, a.addr)
	if err != nil {
		return
	}
	a.rw = bufio.NewReadWriter(bufio.NewReader(a.conn),
		bufio.NewWriter(a.conn))
	go a.work()
	return
}

func (a *agent) work() {
	var inpack *inPack
	var l int
	var err error
	var data, leftdata []byte
	for {
		if data, err = a.read(bufferSize); err != nil {
			if err == ErrLostConn {
				break
			}
			a.worker.err(err)
			// If it is unexpected error and the connection wasn't
			// closed by Gearmand, the agent should close the conection
			// and reconnect to job server.
			a.Close()
			a.conn, err = net.Dial(a.net, a.addr)
			if err != nil {
				a.worker.err(err)
				break
			}
			a.rw = bufio.NewReadWriter(bufio.NewReader(a.conn),
				bufio.NewWriter(a.conn))
		}
		if len(leftdata) > 0 { // some data left for processing
			data = append(leftdata, data...)
		}
		if len(data) < minPacketLength { // not enough data
                        leftdata = data
		        continue
		}
		if inpack, l, err = decodeInPack(data); err != nil {
			//a.worker.err(err)
                        leftdata = data
			continue
		}
		leftdata = nil
		inpack.a = a
		a.worker.in <- inpack
		if len(data) > l {
			leftdata = data[l:]
		}
	}
}

func (a *agent) Close() {
	a.Lock()
	defer a.Unlock()
	if a.conn != nil {
		a.conn.Close()
		a.conn = nil
	}
}

func (a *agent) Grab() {
	a.Lock()
	defer a.Unlock()
	outpack := getOutPack()
	outpack.dataType = dtGrabJobUniq
	a.write(outpack)
}

func (a *agent) PreSleep() {
	a.Lock()
	defer a.Unlock()
	outpack := getOutPack()
	outpack.dataType = dtPreSleep
	a.write(outpack)
}

func isClosed(err error) bool {
	switch {
	case err == io.EOF:
		fallthrough
	case strings.Contains(err.Error(), "use of closed network connection"):
		return true
	}
	return false
}

// read length bytes from the socket
func (a *agent) read(length int) (data []byte, err error) {
	n := 0
	buf := getBuffer(bufferSize)
	// read until data can be unpacked
	for i := length; i > 0 || len(data) < minPacketLength; i -= n {
		if n, err = a.rw.Read(buf); err != nil {
			if isClosed(err) {
				err = ErrLostConn
			}
			return
		}
		data = append(data, buf[0:n]...)
		if n < bufferSize {
			break
		}
	}
	return
}

// Internal write the encoded job.
func (a *agent) write(outpack *outPack) (err error) {
	var n int
	buf := outpack.Encode()
	for i := 0; i < len(buf); i += n {
		n, err = a.rw.Write(buf[i:])
		if err != nil {
			return err
		}
	}
	return a.rw.Flush()
}
