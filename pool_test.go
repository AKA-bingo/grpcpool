package grpcpool

import (
	"context"
	"google.golang.org/grpc"
	"testing"
	"time"
)

func TestNew(t *testing.T) {
	p, err := New(func() (conn *grpc.ClientConn, e error) {
		return grpc.Dial("127.0.0.1:50051", grpc.WithInsecure())
	}, 1, 5, 100, time.Hour, 0)
	defer p.Close()
	if err != nil {
		t.Errorf("The pool returned an error: %s", err.Error())
	}

	// Get a client
	client, err := p.Get(context.Background())
	defer client.Put()
	if err != nil {
		t.Errorf("Get returned an error: %s", err.Error())
	}
	if client == nil {
		t.Error("client was nil")
	}

}
