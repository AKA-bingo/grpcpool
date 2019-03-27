package grpcpool

import (
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/connectivity"
)

// ClientConn is the basic unit in the pool
type ClientConn struct {
	*grpc.ClientConn
	pool          *Pool
	inUse         int
	timeUsed      time.Time
	timeInitiated time.Time
	mu            sync.RWMutex
}

// close a ClientConn when inuse == 1
// if inuse != 1 there is a new goroutine to watch the ClientConn until close it
func (client *ClientConn) close() error {
	if client.inUse <= 0 {
		return client.ClientConn.Close()
	}

	go client.closeWatch()
	return nil
}

// The watch function is used to close the gRPC conn when inuse == 0 or timeout
func (client *ClientConn) closeWatch() {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	for range ticker.C {
		if client.inUse <= 0 {
			_ = client.ClientConn.Close()
			break
		}

		if time.Now().Sub(client.timeUsed) > time.Minute { //long time no used so force to close
			_ = client.ClientConn.Close()
		}
	}
}

func (client *ClientConn) use() {
	client.mu.Lock()
	client.timeUsed = time.Now()
	client.inUse++
	client.mu.Unlock()
}

// Put return the ClientConn when client call finish
func (client *ClientConn) Put() {
	client.mu.Lock()
	client.inUse--
	client.mu.Unlock()

	if !client.pool.requestQueue.isEmpty() && client.inUse < client.pool.opt.UsedPreConn {
		clientChan := client.pool.requestQueue.dequeue().(chan *ClientConn)
		clientChan <- client
		close(clientChan)
		return
	}

	if client.inUse == 0 {
		client.pool.pushIdleClient(client)
	}

}

// check if the gRPC conn state
func (client *ClientConn) active() bool {
	switch client.ClientConn.GetState() {
	case connectivity.Idle:
		return true
	case connectivity.Connecting:
		return true
	case connectivity.Ready:
		return true
	case connectivity.TransientFailure:
		return false
	case connectivity.Shutdown:
		return false
	default:
		return false
	}
}

func (client *ClientConn) waitForReady() {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			if client == nil {
				break
			} else if client.active() {
				if client.pool.requestQueue.size() != 0 {
					client.pool.clientQueue(client.pool.requestQueue.dequeue().(chan *ClientConn))
				}
				break
			}
		}
	}
}
