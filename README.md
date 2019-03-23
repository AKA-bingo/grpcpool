# gRPC-pool

![](https://img.shields.io/badge/language-golang-blue.svg)
[![Go Report Card](https://goreportcard.com/badge/github.com/AKA-bingo/grpc-pool)](https://goreportcard.com/report/github.com/AKA-bingo/grpc-pool)
[![LICENSE](https://img.shields.io/badge/license-Apache2.0-orange.svg)](LICENSE)

gRPC-pool is Used to manage and reuse gRPc connections. You can limit the Usage amount for per connections. 

## Install

```sh
go get -u github.com/AKA-bingo/grpcpool
```

## Usage

```go
import "github.com/AKA-bingo/grpcpool"
```

## Example

```go
func main()  {
  // Create a new pool
	pool, err := grpcpool.New(func() (*grpc.ClientConn, error) {
		return grpc.Dial(addr, grpc.WithInsecure())
	}, 2, 5, 1000, time.Hour, 0)
  
  // Error handle
  if err != nil{
    log.Printf("Create gRPC pool:%v\n", err)
  }
  
  // Release pool
	defer pool.Close()

	// Print the pool info
	log.Println(pool.Print())

	// Get connection from the pool
	conn, err := pool.Get(context.Background())
	if err != nil {
		log.Printf("Get connection fail:%v", err)
	}

	// Get connection from the pool with timeout
	ctx, _ := context.WithDeadline(context.Background(), time.Now().Add(time.Second))
	conn, err := pool.Get(ctx)
	if err != grpcpool.ErrTimeout {
		log.Printf("Time out")
	}

	// Create a gRPC server client by conn
	client := pb.NewgRPCServerClient(conn.ClientConn)
	
	// Put back the Conn
	conn.Put()
  
}
```
