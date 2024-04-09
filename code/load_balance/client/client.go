package main

import (
	"context"
	"etcd/code/load_balance/etcdv3"
	pb "etcd/code/load_balance/proto"
	"fmt"
	"log"
	"strconv"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/resolver"
)

const grpcServiceConfig = `{"loadBalancingPolicy": "round_robin"}`

var (
	EtcdEndpoints = []string{"localhost:2379"}
	SerName       = "simple_grpc"
	grpcClient    pb.SimpleClient
)

func main() {
	r := etcdv3.NewServiceDiscovery(EtcdEndpoints)

	resolver.Register(r)

	conn, err := grpc.NewClient(
		fmt.Sprintf("%s:///%s", r.Scheme(), SerName),
		grpc.WithDefaultServiceConfig(grpcServiceConfig),
		grpc.WithInsecure(),
	)
	if err != nil {
		log.Fatal("grpc.NewClient err:", err)
	}
	defer conn.Close()

	grpcClient = pb.NewSimpleClient(conn)
	for i := 0; i < 100; i++ {
		route(i)
		time.Sleep(1 * time.Second)
	}
}

func route(i int) {
	req := pb.SimpleRequest{
		Data: "grpc " + strconv.Itoa(i),
	}
	res, err := grpcClient.Route(context.Background(), &req)
	if err != nil {
		log.Fatal("Call Route err: %v", err)
	}

	log.Println(res)
}
