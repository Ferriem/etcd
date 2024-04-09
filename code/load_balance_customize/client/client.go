package main

import (
	"context"
	pb "etcd/code/load_balance/proto"
	"etcd/code/load_balance_customize/etcdv3"
	"fmt"
	"log"
	"strconv"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/resolver"
)

const grpcServiceConfig = `{"loadBalancingPolicy": "weight"}`

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
