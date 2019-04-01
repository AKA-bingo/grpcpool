package grpcpool

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"sync"
	"time"

	"google.golang.org/grpc"
)

// Error defined
var (
	// ErrClosed is the error when the client pool is closed
	ErrClosed = errors.New("client pool is closed")
	// ErrTimeout is the error when the client pool timed out
	ErrTimeout = errors.New("get client timed out")
	// ErrFullPool is the error when the pool is already full
	ErrFullPool = errors.New("can't create ClientConn into a full pool")
)

// Factory function type to create a new gRPC client
type Factory func() (*grpc.ClientConn, error)

// Options Pool Options defined
type Options struct {
	Factory         Factory
	PoolSize        int
	MinIdleConns    int
	UsedPreConn     int
	MaxConnLifeTime time.Duration
	IdleTimeout     time.Duration
}

// Pool struct defined
type Pool struct {
	opt           *Options
	poolSize      int
	clients       []*ClientConn
	idleConnQueue *poolQueue
	requestQueue  *poolQueue
	mu            sync.RWMutex
}

// New create and init a new gRPC pool
func New(factory Factory, minIdleConns, poolSize, usedPreConn int, idleTimeout, maxLifetime time.Duration) (*Pool, error) {
	if poolSize <= 0 {
		poolSize = 1
	}

	if minIdleConns > poolSize {
		minIdleConns = poolSize
	}

	opt := Options{
		Factory:         factory,
		PoolSize:        poolSize,
		MinIdleConns:    minIdleConns,
		UsedPreConn:     usedPreConn,
		MaxConnLifeTime: maxLifetime,
		IdleTimeout:     idleTimeout,
	}

	p := &Pool{
		opt:           &opt,
		clients:       make([]*ClientConn, 0, poolSize),
		idleConnQueue: initQueue(poolSize),
		requestQueue:  initQueue(0),
	}

	// init client in pool
	for i := 0; i < opt.MinIdleConns; i++ {
		go p.checkMinIdleConns()
	}

	return p, nil
}

// check if need to supplement new IdleConn and just do it!
func (p *Pool) checkMinIdleConns() {
	if p.opt.MinIdleConns <= 0 {
		return
	}

	p.addIdleClient()
}

// Add a new Client into the pool which about to be used
func (p *Pool) addUsedClient() *ClientConn {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.poolSize >= p.opt.PoolSize {
		return nil
	}

	conn, err := p.newClient()
	if err != nil {
		log.Println(err)
		return nil
	}

	p.poolSize++
	p.clients = append(p.clients, conn)
	return conn
}

// addIdleClient will create a new IDLE conn into idleConnQueue if the queue not full
func (p *Pool) addIdleClient() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.poolSize >= p.opt.PoolSize || p.idleConnQueue.size() >= p.opt.MinIdleConns {
		return
	}

	conn, err := p.newClient()
	if err != nil {
		panic(err)
	}

	p.poolSize++
	p.clients = append(p.clients, conn)
	p.idleConnQueue.enqueue(conn)
}

// newClient return a new conn by the factory
func (p *Pool) newClient() (*ClientConn, error) {
	// client pool already close
	if p == nil {
		return nil, ErrClosed
	}

	// check if the pool already full
	if p.poolSize >= p.opt.PoolSize {
		return nil, ErrFullPool
	}

	// create new conn
	conn, err := p.opt.Factory()
	if err != nil {
		return nil, err
	}

	return &ClientConn{
		ClientConn:    conn,
		pool:          p,
		inUse:         0,
		timeUsed:      time.Now(),
		timeInitiated: time.Now(),
	}, nil
}

// Close the pool and release resources
func (p *Pool) Close() {
	p.mu.Lock()
	clients := p.clients
	requestQueue := p.requestQueue
	p.clients = nil //release clients
	p.idleConnQueue = nil
	p.requestQueue = nil // release requestQueue
	p.mu.Unlock()

	for !requestQueue.isEmpty() { // close all the wait channle in the queue
		close(requestQueue.dequeue().(chan *ClientConn))
	}

	for _, conn := range clients { // close all the conn in the pool
		_ = conn.ClientConn.Close()
	}
}

// Get a IdleConn for the idleConnQueue
func (p *Pool) popIdleClient() *ClientConn {
	conn := p.idleConnQueue.dequeue()
	if conn == nil {
		return nil
	}
	go p.checkMinIdleConns()
	return conn.(*ClientConn)
}

// Put the IdleConn back to idleConnQueue
func (p *Pool) pushIdleClient(client *ClientConn) {
	if p.opt.MinIdleConns == 0 || p.idleConnQueue.size() == p.opt.MinIdleConns {
		_ = p.CloseClient(client)
		return
	}

	client.mu.RLock()
	defer client.mu.RUnlock()
	if client.inUse > 0 {
		return
	}
	p.idleConnQueue.enqueue(client)
}

// Get the client from the pool
func (p *Pool) getClient() *ClientConn {
	var bestConn *ClientConn
	for i, client := range p.clients {
		if client.inUse < p.opt.UsedPreConn && (bestConn == nil || client.inUse < bestConn.inUse) {
			if client.active() {
				bestConn = p.clients[i]
			} else {
				go client.waitForReady()
			}
		}
	}

	if bestConn != nil {
		return bestConn
	}

	newConn := p.addUsedClient()
	if newConn != nil {
		if newConn.active() {
			return newConn
		}
		go newConn.waitForReady()
	}

	return nil
}

// Check if the conn is stable (about timeout)
func (p *Pool) checkStableConn(cn *ClientConn) bool {
	now := time.Now()

	if p.opt.IdleTimeout > 0 && now.Sub(cn.timeUsed) >= p.opt.IdleTimeout {
		_ = p.CloseClient(cn)
		return false
	}

	if p.opt.MaxConnLifeTime > 0 && now.Sub(cn.timeInitiated) >= p.opt.MaxConnLifeTime {
		_ = p.CloseClient(cn)
		return false
	}

	return true
}

// CloseClient will remove the client And close it
func (p *Pool) CloseClient(client *ClientConn) error {
	p.removeClient(client)
	go p.checkMinIdleConns()
	return client.close()
}

// removeClient will remove the client from the pool
func (p *Pool) removeClient(client *ClientConn) {
	for i, c := range p.clients {
		if c == client {
			p.mu.Lock()
			p.clients = append(p.clients[:i], p.clients[i+1:]...)
			p.poolSize--
			p.mu.Unlock()
			break
		}
	}
}

// Get an Available client to use
func (p *Pool) Get(ctx context.Context) (*ClientConn, error) {
	if p == nil {
		return nil, ErrClosed
	}

	client := make(chan *ClientConn, 1)
	p.clientQueue(client)

	select {
	case conn := <-client:
		conn.use()
		return conn, nil
	case <-ctx.Done():
		return nil, ErrTimeout
	}
}

// clientQueue try to get an Available conn form pool or put the request into requestQueue to wait
func (p *Pool) clientQueue(client chan *ClientConn) {
	for { // Get conn from idle pool
		conn := p.popIdleClient()
		if conn == nil {
			break
		}

		if !p.checkStableConn(conn) {
			continue
		}

		if !conn.active() {
			go conn.waitForReady()
			break
		}

		client <- conn
		close(client)
		return
	}

	conn := p.getClient() // Get conn from the entire pool
	if conn != nil {
		client <- conn
		close(client)
		return
	}

	//put into the request queue
	p.requestQueue.enqueue(client)
}

// Print the pool to monitor Usage
func (p *Pool) Print() []byte {
	type ClientShow struct {
		Inuse    int       `json:"inuse"`
		TimeUsed time.Time `json:"time_used"`
		TimeInit time.Time `json:"time_init"`
		Active   bool      `json:"active"`
	}
	type PoolShow struct {
		PoolSize    int          `json:"pool_size"`
		IdleConnLen int          `json:"idleConn_len"`
		Clients     []ClientShow `json:"clients"`
		QueueSize   int          `json:"queue_size"`
	}

	rep := new(PoolShow)
	rep.PoolSize = p.poolSize
	rep.IdleConnLen = p.idleConnQueue.size()
	rep.QueueSize = p.requestQueue.size()
	for _, conn := range p.clients {
		newClient := ClientShow{
			Inuse:    conn.inUse,
			TimeInit: conn.timeInitiated,
			TimeUsed: conn.timeUsed,
			Active:   conn.active(),
		}
		rep.Clients = append(rep.Clients, newClient)
	}
	result, err := json.Marshal(rep)
	if err != nil {
		log.Println(err)
	}
	return result
}
